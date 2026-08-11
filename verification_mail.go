package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var (
	verificationCodeRE = regexp.MustCompile(`^\d{6}$`)
	emailRe             = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
)

type verificationCodeRequest struct {
	To   string `json:"to"`
	Code string `json:"code"`
}

func (a *App) sendVerificationCode(w http.ResponseWriter, r *http.Request) {
	var req verificationCodeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.To = strings.TrimSpace(req.To)
	req.Code = strings.TrimSpace(req.Code)
	if !emailRe.MatchString(req.To) {
		writeError(w, http.StatusBadRequest, "invalid recipient email")
		return
	}
	if !verificationCodeRE.MatchString(req.Code) {
		writeError(w, http.StatusBadRequest, "code must be exactly 6 digits")
		return
	}
	html := verificationCodeHTML(req.Code)
	if err := a.mailer.Send(r.Context(), req.To, "Verify your Lanth account", html); err != nil {
		fmt.Printf("[mailer] verification email to %s FAILED: %v\n", req.To, err)
		writeError(w, http.StatusBadGateway, "failed to send verification email")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func verificationCodeHTML(code string) string {
	return `
<html><body style="margin:0;padding:0;background:#f4f4f5;font-family:Arial,sans-serif">
<div style="max-width:560px;margin:24px auto;background:#ffffff;border-radius:12px;overflow:hidden">
<div style="background:#111827;color:#ffffff;padding:24px 32px">
<h2 style="margin:0;font-size:20px">LANTH</h2>
<p style="margin:4px 0 0;color:#fdba74;font-size:12px">lanthwear</p>
<p style="margin:12px 0 0;color:#ffffff;font-size:14px">Verify your email address</p></div>
<div style="padding:32px">
<p style="margin:0 0 16px;font-size:14px;color:#374151">Enter this code to verify your account. It expires in <strong>30 minutes</strong>.</p>
<div style="font-size:34px;font-weight:700;letter-spacing:12px;color:#111827;background:#f5f5f4;border-radius:12px;padding:16px 24px;text-align:center;margin:0 0 16px">` + code + `</div>
<p style="margin:0;color:#9ca3af;font-size:12px">If you didn't create an account with Lanth, you can ignore this email.</p>
</div></div></body></html>`
}
