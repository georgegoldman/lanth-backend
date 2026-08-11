package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ResendClient struct {
	apiKey string
	from   string
	http   *http.Client
}

func NewResendClient(apiKey, from string) *ResendClient {
	return &ResendClient{
		apiKey: apiKey,
		from:   from,
		http:   &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *ResendClient) Enabled() bool {
	return c.apiKey != ""
}

func (c *ResendClient) Send(ctx context.Context, to, subject, html string) error {
	if !c.Enabled() {
		fmt.Printf("[mailer] skipped email to %s (subject: %q) — RESEND_API_KEY not configured\n", to, subject)
		return nil
	}
	payload := map[string]string{
		"from":    c.from,
		"to":      to,
		"subject": subject,
		"html":    html,
	}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("resend returned %d: %s", resp.StatusCode, string(body))
		fmt.Printf("[mailer] FAILED email to %s (subject: %q): %v\n", to, subject, err)
		return err
	}
	fmt.Printf("[mailer] sent email to %s (subject: %q) — %s\n", to, subject, resp.Status)
	return nil
}

func channelLabel(ch string) string {
	switch ch {
	case "bank_transfer":
		return "Bank Transfer"
	case "ussd":
		return "USSD"
	case "card":
		return "Card"
	}
	return "Card"
}

func variantHTML(it OrderItem) string {
	var parts []string
	if it.Size != "" {
		parts = append(parts, "Size: "+htmlEscape(it.Size))
	}
	if it.Color != "" {
		parts = append(parts, "Color: "+htmlEscape(it.Color))
	}
	if len(parts) == 0 {
		return ""
	}
	return `<br><span style="color:#9ca3af;font-size:12px">` + strings.Join(parts, " · ") + `</span>`
}

func paymentInvoiceHTML(o *Order, storeURL string) string {
	var itemsHTML string
	for _, it := range o.Items {
		lineTotal := int64(it.Quantity) * it.Price
		desc := ""
		if it.Description != "" {
			desc = `<br><span style="color:#6b7280;font-size:12px">` + htmlEscape(it.Description) + `</span>`
		}
		img := ""
		if it.Image != "" {
			img = `<img src="` + htmlEscape(it.Image) + `" alt="" width="44" height="44" style="width:44px;height:44px;object-fit:cover;border-radius:8px;vertical-align:middle;margin-right:12px"/>`
		}
		itemsHTML += fmt.Sprintf(
			`<tr><td style="padding:10px 0;font-size:14px;color:#111827">%s<span>%s%s%s</span></td>
<td style="padding:10px 0;text-align:center;font-size:14px;color:#374151">%d</td>
<td style="padding:10px 0;text-align:right;font-size:14px;color:#374151">₦%.2f</td>
<td style="padding:10px 0;text-align:right;font-size:14px;color:#111827;font-weight:600">₦%.2f</td></tr>`,
			img, htmlEscape(it.Name), desc, variantHTML(it), it.Quantity, naira(it.Price), naira(lineTotal),
		)
	}

	paid := o.Total
	channel := ""
	if o.Payment != nil {
		if o.Payment.AmountPaid > 0 {
			paid = o.Payment.AmountPaid
		}
		channel = channelLabel(o.Payment.Channel)
	}
	if channel == "" {
		channel = channelLabel(o.Channel)
	}

	addr := ""
	if o.ShippingAddress != nil {
		s := o.ShippingAddress
		addr = htmlEscape(strings.TrimSpace(s.Address + ", " + s.City + ", " + s.State + " " + s.Zip + ", " + s.Country))
	}

	tracking := trackingNumber(o)
	trackLink := strings.TrimRight(storeURL, "/") + "/tracking?number=" + tracking

	return fmt.Sprintf(`
<html><body style="margin:0;padding:0;background:#f4f4f5;font-family:Arial,sans-serif">
<div style="max-width:560px;margin:24px auto;background:#ffffff;border-radius:12px;overflow:hidden">
<div style="background:#111827;color:#ffffff;padding:24px 32px;display:flex;justify-content:space-between;align-items:center">
<div><h2 style="margin:0;font-size:20px">LANTH</h2><p style="margin:4px 0 0;color:#fdba74;font-size:12px">lanthwear</p></div>
<div style="text-align:right"><p style="margin:0;font-size:11px;color:#9ca3af;text-transform:uppercase;letter-spacing:1px">Transaction invoice</p>
<p style="margin:2px 0 0;font-size:15px;font-weight:700;color:#ffffff">%s</p></div></div>
<div style="padding:32px">
<p style="margin:0 0 4px;font-size:16px;font-weight:600;color:#111827">Payment received — thank you for your order!</p>
<p style="margin:0;color:#6b7280;font-size:13px">Hi %s, your order below has been paid for successfully.</p>

<table style="width:100%%;margin:20px 0 0;border-collapse:collapse;font-size:13px">
<tr><td style="padding:6px 0;color:#6b7280">Invoice no.</td><td style="padding:6px 0;text-align:right;font-weight:600;color:#111827">%s</td></tr>
<tr><td style="padding:6px 0;color:#6b7280">Order date</td><td style="padding:6px 0;text-align:right;color:#111827">%s</td></tr>
<tr><td style="padding:6px 0;color:#6b7280">Payment method</td><td style="padding:6px 0;text-align:right;color:#111827">%s</td></tr>
<tr><td style="padding:6px 0;color:#6b7280">Payment status</td><td style="padding:6px 0;text-align:right;color:#16a34a;font-weight:600">Paid</td></tr>
</table>

%s

<table style="width:100%%;border-collapse:collapse;margin:16px 0 4px;font-size:14px">
<thead><tr style="color:#6b7280;font-size:11px;text-transform:uppercase;letter-spacing:0.5px;border-bottom:2px solid #e5e7eb">
<th style="text-align:left;padding:8px 0">Item</th><th style="text-align:center;padding:8px 0">Qty</th>
<th style="text-align:right;padding:8px 0">Unit price</th><th style="text-align:right;padding:8px 0">Amount</th></tr></thead>
<tbody style="border-bottom:2px solid #e5e7eb">%s</tbody></table>

<table style="width:100%%;border-collapse:collapse;font-size:14px">
<tr><td style="padding:6px 0;color:#6b7280">Subtotal</td><td style="padding:6px 0;text-align:right;color:#374151">₦%.2f</td></tr>
<tr><td style="padding:6px 0;color:#6b7280">Delivery</td><td style="padding:6px 0;text-align:right;color:#374151">Free</td></tr>
<tr><td style="padding:8px 0;color:#111827;font-weight:700;border-top:2px solid #e5e7eb;font-size:15px">Total paid</td>
<td style="padding:8px 0;text-align:right;font-weight:700;border-top:2px solid #e5e7eb;font-size:15px;color:#111827">₦%.2f</td></tr></table>

<p style="background:#ffedd5;border:1px solid #fdba74;border-radius:8px;padding:16px;font-size:14px;color:#7c2d12;margin:20px 0 0">
<strong style="color:#c2410c">Track your order</strong><br>
Tracking ID: <span style="font-size:16px;font-weight:700;letter-spacing:0.5px">%s</span><br>
<a href="%s" style="color:#c2410c">Click here to track this order</a></p>

<p style="background:#f3f4f6;border-radius:8px;padding:16px;font-size:14px;margin:12px 0 0">
<strong>Delivery address</strong><br>%s<br>%s</p>

<p style="margin-top:24px;color:#6b7280;font-size:13px">Keep this invoice for your records. You can also view all your orders from the shop at any time.</p>
</div></div></body></html>`,
		o.Reference, o.CustomerName, o.Reference,
		o.CreatedAt.UTC().Format("2006-01-02 15:04 MST"),
		channel, shippingBlockHTML(o), itemsHTML, naira(o.Subtotal), naira(paid),
		tracking, trackLink, addr, htmlEscape(o.CustomerPhone))
}

func (a *App) emailPaymentConfirmation(ctx context.Context, o *Order) {
	html := paymentInvoiceHTML(o, a.storeURL)
	_ = a.mailer.Send(ctx, o.CustomerEmail, fmt.Sprintf("Order invoice %s — payment confirmed", o.Reference), html)
}

func shippingBlockHTML(o *Order) string {
	if o.ShippingAddress == nil {
		return ""
	}
	addr := o.ShippingAddress
	line := strings.TrimSpace(addr.Address + ", " + addr.City + ", " + addr.State + " " + addr.Zip + ", " + addr.Country)
	return fmt.Sprintf(`<p style="background:#f3f4f6;border-radius:8px;padding:16px;font-size:14px">
<strong>Delivery address:</strong><br>%s<br>%s</p>`, htmlEscape(line), htmlEscape(o.CustomerPhone))
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;").Replace(s)
}

func (a *App) emailCancellation(ctx context.Context, o *Order) {
	refunded := ""
	if o.Status == OrderCancelled && o.Payment != nil && o.Payment.AmountPaid > 0 {
		refunded = fmt.Sprintf("<p>Your payment of <strong>₦%.2f</strong> is being refunded to your original payment method. Refunds typically take a few business days to appear.</p>", naira(o.Payment.AmountPaid))
	}
	html := fmt.Sprintf(`
<html><body style="margin:0;padding:0;background:#f4f4f5;font-family:Arial,sans-serif">
<div style="max-width:560px;margin:24px auto;background:#ffffff;border-radius:12px;overflow:hidden">
<div style="background:#111827;color:#ffffff;padding:24px 32px"><h2 style="margin:0;font-size:20px">LANTH</h2>
<p style="margin:4px 0 0;color:#fdba74;font-size:12px">lanthwear</p></div>
<div style="padding:32px">
<p>Hi %s,</p>
<p>Your order <strong>%s</strong> has been cancelled.</p>
%s
<p>If you have any questions, reply to this email and we'll help you out.</p>
<p>— The Lanth team</p>
</div></div></body></html>`, htmlEscape(o.CustomerName), o.Reference, refunded)
	_ = a.mailer.Send(ctx, o.CustomerEmail, fmt.Sprintf("Order %s has been cancelled", o.Reference), html)
}

func (a *App) emailBackorder(ctx context.Context, o *Order) {
	html := fmt.Sprintf(`
<html><body style="margin:0;padding:0;background:#f4f4f5;font-family:Arial,sans-serif">
<div style="max-width:560px;margin:24px auto;background:#ffffff;border-radius:12px;overflow:hidden">
<div style="background:#b45309;color:#ffffff;padding:24px 32px"><h2 style="margin:0">Lanth Wear</h2>
<p style="margin:4px 0 0;color:#fde68a;font-size:13px">Part of your order is on backorder</p></div>
<div style="padding:32px">
<p>Hi there,</p>
<p>We couldn't fully allocate stock for order <strong>%s</strong>. We've kept your payment safe and will ship the rest as soon as it's restocked.</p>
<p>Tracking number: <strong>%s</strong></p>
<p>We'll send you an update when your items ship.</p>
</div></div></body></html>`, o.Reference, trackingNumber(o))
	_ = a.mailer.Send(ctx, o.CustomerEmail, fmt.Sprintf("Order %s is on backorder", o.Reference), html)
}

func (a *App) emailTrackingUpdate(ctx context.Context, o *Order, ev TrackingEvent) {
	loc := ev.Location
	if loc == "" {
		loc = "-"
	}
	html := fmt.Sprintf(`
<html><body style="margin:0;padding:0;background:#f4f4f5;font-family:Arial,sans-serif">
<div style="max-width:560px;margin:24px auto;background:#ffffff;border-radius:12px;overflow:hidden">
<div style="background:#111827;color:#ffffff;padding:24px 32px"><h2 style="margin:0">Lanth Wear</h2>
<p style="margin:4px 0 0;color:#9ca3af;font-size:13px">Your order is on the move</p></div>
<div style="padding:32px">
<p>Hi there,</p>
<p>Update on order <strong>%s</strong> (tracking <strong>%s</strong>):</p>
<p style="background:#f3f4f6;border-radius:8px;padding:16px;font-size:14px">
<strong>%s</strong><br>Location: %s<br>%s<br><small style="color:#6b7280">%s</small></p>
<p>You can keep tracking this item from the platform at any time.</p>
</div></div></body></html>`,
		o.Reference, trackingNumber(o), ev.Status, loc, ev.Note, ev.At.Format("2006-01-02 15:04 MST"))
	_ = a.mailer.Send(ctx, o.CustomerEmail, fmt.Sprintf("Tracking update — %s", ev.Status), html)
}

func naira(kobo int64) float64 {
	return float64(kobo) / 100
}

func trackingNumber(o *Order) string {
	if o.Tracking != nil {
		return o.Tracking.TrackingNumber
	}
	return ""
}
