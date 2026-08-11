package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	loadDotEnv(".env")

	mongoURI := getenv("MONGO_URI", "mongodb://localhost:27017")
	dbName := getenv("MONGO_DB", "lanth")
	port := getenv("PORT", "3002")
	adminKey := getenv("ADMIN_API_KEY", "")
	paystackSecret := getenv("PAYSTACK_SECRET_KEY", "")
	resendKey := getenv("RESEND_API_KEY", "")
	emailFrom := getenv("EMAIL_FROM", "Lanth Wear <onboarding@resend.dev>")
	callbackURL := getenv("PAYSTACK_CALLBACK_URL", "")
	cloudinaryName := getenv("CLOUDINARY_CLOUD_NAME", "")
	cloudinaryKey := getenv("CLOUDINARY_KEY", "")
	cloudinarySecret := getenv("CLOUDINARY_SECRET", "")
	storeURL := getenv("STORE_URL", "http://localhost:3000")

	if paystackSecret == "" {
		log.Println("WARNING: PAYSTACK_SECRET_KEY not set — checkout will fail at Paystack initialization")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err != nil {
		log.Fatalf("failed to create mongo client: %v", err)
	}
	defer client.Disconnect(context.Background())

	if err := client.Ping(ctx, nil); err != nil {
		log.Fatalf("failed to ping mongo: %v", err)
	}

	db := client.Database(dbName)
	app := &App{
		db:          db,
		products:    db.Collection("products"),
		collections: db.Collection("collections"),
		orders:      db.Collection("orders"),
		paystack:    NewPaystackClient(paystackSecret),
		mailer:      NewResendClient(resendKey, emailFrom),
		cloudinary:  NewCloudinaryClient(cloudinaryName, cloudinaryKey, cloudinarySecret),
		callbackURL: callbackURL,
		storeURL:    storeURL,
		adminKey:    adminKey,
	}

	if err := app.seedDefaultCollections(ctx); err != nil {
		log.Printf("WARNING: failed to seed default collections: %v", err)
	}

	mux := http.NewServeMux()

	// Public: catalog
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/products", app.listProducts)
	mux.HandleFunc("GET /api/products/search", app.searchProducts)
	mux.HandleFunc("GET /api/products/{id}", app.getProduct)
	mux.HandleFunc("GET /api/collections", app.listCollections)
	mux.HandleFunc("GET /api/collections/{slug}", app.getCollection)

	// Public: buying + tracking
	mux.HandleFunc("POST /api/checkout", app.checkout)
	mux.HandleFunc("POST /api/paystack/webhook", app.paystackWebhook)
	mux.HandleFunc("GET /api/orders/{reference}", app.getOrder)
	mux.HandleFunc("GET /api/orders/mine", app.listMyOrders)
	mux.HandleFunc("GET /api/orders/{reference}/verify", app.verifyOrder)
	mux.HandleFunc("GET /api/orders/{id}/tracking", app.getTracking)
	mux.HandleFunc("GET /api/tracking/{number}", app.getTrackingByNumber)

	// Admin (X-Admin-Key header)
	mux.HandleFunc("GET /api/orders", app.admin(app.listOrders))
	mux.HandleFunc("POST /api/products", app.admin(app.createProduct))
	mux.HandleFunc("PUT /api/products/{id}", app.admin(app.updateProduct))
	mux.HandleFunc("DELETE /api/products/{id}", app.admin(app.deleteProduct))
	mux.HandleFunc("POST /api/collections", app.admin(app.createCollection))
	mux.HandleFunc("PUT /api/collections/{id}", app.admin(app.updateCollection))
	mux.HandleFunc("DELETE /api/collections/{id}", app.admin(app.deleteCollection))
	mux.HandleFunc("POST /api/orders/bulk-status", app.admin(app.bulkOrderStatus))
	mux.HandleFunc("POST /api/orders/{id}/tracking", app.admin(app.updateTracking))
	mux.HandleFunc("POST /api/orders/bulk-cancel", app.admin(app.bulkCancelOrders))
	mux.HandleFunc("POST /api/orders/{id}/cancel", app.admin(app.cancelOrder))
	mux.HandleFunc("POST /api/uploads", app.admin(app.uploadImage))

	log.Printf("lanth-backend listening on :%s", port)
	if err := http.ListenAndServe(":"+port, corsMiddleware(getenv("CORS_ORIGINS", "http://localhost:3000"))(mux)); err != nil {
		log.Fatal(err)
	}
}
