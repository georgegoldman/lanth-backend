package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const paystackBaseURL = "https://api.paystack.co"

var defaultChannels = []string{"card", "bank_transfer", "ussd"}

type PaystackClient struct {
	secret string
	http   *http.Client
}

func NewPaystackClient(secret string) *PaystackClient {
	return &PaystackClient{
		secret: secret,
		http:   &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *PaystackClient) do(ctx context.Context, method, path string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, paystackBaseURL+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("paystack returned %d: %s", resp.StatusCode, string(respBody))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return err
		}
	}
	return nil
}

type InitializeResult struct {
	AuthorizationURL string `json:"authorization_url"`
	AccessCode       string `json:"access_code"`
	Reference        string `json:"reference"`
}

func (c *PaystackClient) Initialize(ctx context.Context, email string, amount int64, currency, reference, callbackURL string, channels []string) (*InitializeResult, error) {
	if len(channels) == 0 {
		channels = defaultChannels
	}
	payload := map[string]any{
		"email":     email,
		"amount":    amount,
		"currency":  currency,
		"reference": reference,
		"channels":  channels,
	}
	if callbackURL != "" {
		payload["callback_url"] = callbackURL
	}

	var resp struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			AuthorizationURL string `json:"authorization_url"`
			AccessCode       string `json:"access_code"`
			Reference        string `json:"reference"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/transaction/initialize", payload, &resp); err != nil {
		return nil, err
	}
	if !resp.Status {
		return nil, fmt.Errorf("paystack initialize failed: %s", resp.Message)
	}
	return &InitializeResult{
		AuthorizationURL: resp.Data.AuthorizationURL,
		AccessCode:       resp.Data.AccessCode,
		Reference:        resp.Data.Reference,
	}, nil
}

type Verification struct {
	Status    string
	Reference string
	Amount    int64
	Channel   string
	PaidAt    time.Time
}

func (c *PaystackClient) Verify(ctx context.Context, reference string) (*Verification, error) {
	var resp struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Status    string    `json:"status"`
			Reference string    `json:"reference"`
			Amount    int64     `json:"amount"`
			Channel   string    `json:"channel"`
			PaidAt    time.Time `json:"paid_at"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/transaction/verify/"+reference, nil, &resp); err != nil {
		return nil, err
	}
	if !resp.Status {
		return nil, fmt.Errorf("paystack verify failed: %s", resp.Message)
	}
	return &Verification{
		Status:    resp.Data.Status,
		Reference: resp.Data.Reference,
		Amount:    resp.Data.Amount,
		Channel:   resp.Data.Channel,
		PaidAt:    resp.Data.PaidAt,
	}, nil
}

func (c *PaystackClient) VerifySignature(signature string, body []byte) bool {
	if signature == "" {
		return false
	}
	mac := hmac.New(sha512.New, []byte(c.secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func (c *PaystackClient) Refund(ctx context.Context, transactionReference string) error {
	var resp struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/refund", map[string]string{"transaction": transactionReference}, &resp); err != nil {
		return err
	}
	if !resp.Status {
		return fmt.Errorf("paystack refund failed: %s", resp.Message)
	}
	return nil
}
