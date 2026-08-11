package main

import "time"

const (
	OrderPending     = "PENDING"
	OrderPaid        = "PAID"
	OrderProcessing  = "PROCESSING"
	OrderFulfilled   = "FULFILLED"
	OrderBackordered = "BACKORDERED"
	OrderDelivered   = "DELIVERED"
	OrderCancelled   = "CANCELLED"
	OrderFailed      = "FAILED"
)

type Collection struct {
	ID           string        `json:"id" bson:"_id"`
	Slug         string        `json:"slug" bson:"slug"`
	Name         string        `json:"name" bson:"name"`
	Description  string        `json:"description,omitempty" bson:"description,omitempty"`
	ParentID     string        `json:"parent_id,omitempty" bson:"parent_id,omitempty"`
	Image        string        `json:"image,omitempty" bson:"image,omitempty"`
	Active       bool          `json:"active" bson:"active"`
	ProductCount int           `json:"product_count" bson:"-"`
	Children     []*Collection `json:"children,omitempty" bson:"-"`
	CreatedAt    time.Time     `json:"created_at" bson:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at" bson:"updated_at"`
}

type Product struct {
	ID              string    `json:"id" bson:"_id"`
	Name            string    `json:"name" bson:"name"`
	Description     string    `json:"description,omitempty" bson:"description,omitempty"`
	Price           int64     `json:"price" bson:"price"`
	Currency        string    `json:"currency" bson:"currency"`
	CollectionID    string    `json:"collection_id" bson:"collection_id"`
	CollectionPath  []string  `json:"collection_path,omitempty" bson:"collection_path,omitempty"`
	Images          []string  `json:"images,omitempty" bson:"images,omitempty"`
	Sizes           []string  `json:"sizes,omitempty" bson:"sizes,omitempty"`
	Colors          []string  `json:"colors,omitempty" bson:"colors,omitempty"`
	Brand           string    `json:"brand,omitempty" bson:"brand,omitempty"`
	Merchant        string    `json:"merchant,omitempty" bson:"merchant,omitempty"`
	StoreCity       string    `json:"store_city,omitempty" bson:"store_city,omitempty"`
	Condition       string    `json:"condition,omitempty" bson:"condition,omitempty"`
	ProductType     string    `json:"product_type,omitempty" bson:"product_type,omitempty"`
	DiscountPercent int       `json:"discount_percent" bson:"discount_percent"`
	SoldCount       int       `json:"sold_count" bson:"sold_count"`
	Rating          float64   `json:"rating" bson:"rating"`
	ReviewCount     int       `json:"review_count" bson:"review_count"`
	Stock           int       `json:"stock" bson:"stock"`
	Active          bool      `json:"active" bson:"active"`
	CreatedAt       time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" bson:"updated_at"`
}

type OrderItem struct {
	ProductID   string `json:"product_id" bson:"product_id"`
	Name        string `json:"name" bson:"name"`
	Quantity    int    `json:"quantity" bson:"quantity"`
	Price       int64  `json:"price" bson:"price"`
	Size        string `json:"size,omitempty" bson:"size,omitempty"`
	Color       string `json:"color,omitempty" bson:"color,omitempty"`
	Image       string `json:"image,omitempty" bson:"image,omitempty"`
	Description string `json:"description,omitempty" bson:"description,omitempty"`
}

type PaymentInfo struct {
	PaystackReference string     `json:"paystack_reference" bson:"paystack_reference"`
	AccessCode        string     `json:"access_code,omitempty" bson:"access_code,omitempty"`
	AuthorizationURL  string     `json:"authorization_url,omitempty" bson:"authorization_url,omitempty"`
	AmountPaid        int64      `json:"amount_paid,omitempty" bson:"amount_paid,omitempty"`
	Channel           string     `json:"channel,omitempty" bson:"channel,omitempty"`
	PaidAt            *time.Time `json:"paid_at,omitempty" bson:"paid_at,omitempty"`
}

type TrackingEvent struct {
	Status   string    `json:"status" bson:"status"`
	Location string    `json:"location,omitempty" bson:"location,omitempty"`
	Note     string    `json:"note,omitempty" bson:"note,omitempty"`
	At       time.Time `json:"at" bson:"at"`
}

type Tracking struct {
	TrackingNumber string          `json:"tracking_number" bson:"tracking_number"`
	Carrier        string          `json:"carrier,omitempty" bson:"carrier,omitempty"`
	Events         []TrackingEvent `json:"events" bson:"events"`
}

type ShippingAddress struct {
	Address string `json:"address" bson:"address"`
	City    string `json:"city" bson:"city"`
	State   string `json:"state,omitempty" bson:"state,omitempty"`
	Country string `json:"country,omitempty" bson:"country,omitempty"`
	Zip     string `json:"zip,omitempty" bson:"zip,omitempty"`
}

type Order struct {
	ID              string           `json:"id" bson:"_id"`
	Reference       string           `json:"reference" bson:"reference"`
	SessionID       string           `json:"session_id,omitempty" bson:"session_id,omitempty"`
	CustomerEmail   string           `json:"customer_email" bson:"customer_email"`
	CustomerName    string           `json:"customer_name,omitempty" bson:"customer_name,omitempty"`
	CustomerPhone   string           `json:"customer_phone,omitempty" bson:"customer_phone,omitempty"`
	ShippingAddress *ShippingAddress `json:"shipping_address,omitempty" bson:"shipping_address,omitempty"`
	Items           []OrderItem      `json:"items" bson:"items"`
	Subtotal        int64            `json:"subtotal" bson:"subtotal"`
	Total           int64            `json:"total" bson:"total"`
	Currency        string           `json:"currency" bson:"currency"`
	Channel         string           `json:"channel,omitempty" bson:"channel,omitempty"`
	Status          string           `json:"status" bson:"status"`
	Payment         *PaymentInfo     `json:"payment,omitempty" bson:"payment,omitempty"`
	Tracking        *Tracking        `json:"tracking,omitempty" bson:"tracking,omitempty"`
	CreatedAt       time.Time        `json:"created_at" bson:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at" bson:"updated_at"`
}
