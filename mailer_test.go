package main

import (
	"strings"
	"testing"
	"time"
)

func TestPaymentInvoiceHTML(t *testing.T) {
	paidAt := time.Date(2026, 8, 10, 14, 30, 0, 0, time.UTC)
	o := &Order{
		Reference:     "LNT-1786380947ABC12345",
		CustomerName:  "Ada Obi",
		CustomerPhone: "+2348012345678",
		ShippingAddress: &ShippingAddress{
			Address: "12 Admiralty Way, Lekki Phase 1",
			City:    "Lagos",
			State:   "Lagos State",
			Country: "Nigeria",
		},
		Items: []OrderItem{
			{ProductID: "1", Name: "Lanth Sprint Runner", Quantity: 1, Price: 2200000, Size: "42", Color: "Orange", Image: "https://cdn.example.com/runner.png", Description: "Lightweight running sneaker, orange mesh upper"},
			{ProductID: "2", Name: "Lanth Retro High", Quantity: 2, Price: 1900000},
		},
		Subtotal:  6000000,
		Total:     6000000,
		Currency:  "NGN",
		Channel:   "card",
		Status:    OrderPaid,
		Payment:   &PaymentInfo{PaystackReference: "LNT-1786380947ABC12345", AmountPaid: 6000000, Channel: "card", PaidAt: &paidAt},
		Tracking:  &Tracking{TrackingNumber: "LANT-17863809475CE9D1CC"},
		CreatedAt: paidAt,
	}

	html := paymentInvoiceHTML(o, "http://localhost:3000")

	checks := []string{
		"Transaction invoice",
		"LNT-1786380947ABC12345",
		"LANT-17863809475CE9D1CC",
		"Track your order",
		"/tracking?number=LANT-17863809475CE9D1CC",
		"Ada Obi",
		"Lanth Sprint Runner",
		"Lightweight running sneaker, orange mesh upper",
		"cdn.example.com/runner.png",
		"Size: 42",
		"Lanth Retro High",
		"Unit price",
		"₦22000.00",
		"₦19000.00",
		"₦60000.00",
		"Total paid",
		"Delivery",
		"Free",
		"12 Admiralty Way, Lekki Phase 1",
		"+2348012345678",
	}
	for _, c := range checks {
		if !strings.Contains(html, c) {
			t.Errorf("expected invoice HTML to contain %q", c)
		}
	}
	if !strings.Contains(html, ">Card</td>") {
		t.Error("expected payment method Card")
	}
}
