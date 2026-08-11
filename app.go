package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

type App struct {
	db          *mongo.Database
	products    *mongo.Collection
	collections *mongo.Collection
	orders      *mongo.Collection
	paystack    *PaystackClient
	mailer      *ResendClient
	cloudinary  *CloudinaryClient
	callbackURL string
	storeURL    string
	adminKey    string
}

func (a *App) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.adminKey == "" {
			writeError(w, http.StatusServiceUnavailable, "admin API key is not configured on the server")
			return
		}
		if r.Header.Get("X-Admin-Key") != a.adminKey {
			writeError(w, http.StatusUnauthorized, "invalid or missing X-Admin-Key header")
			return
		}
		next(w, r)
	}
}

func (a *App) isAdminRequest(r *http.Request) bool {
	return a.adminKey != "" && r.Header.Get("X-Admin-Key") == a.adminKey
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return err
	}
	return nil
}

func validChannel(ch string) bool {
	switch strings.ToLower(ch) {
	case "card", "bank_transfer", "ussd":
		return true
	}
	return false
}

func normalizeChannel(ch string) string {
	return strings.ToLower(strings.TrimSpace(ch))
}
