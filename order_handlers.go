package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type checkoutItem struct {
	ProductID string `json:"product_id"`
	Quantity  int    `json:"quantity"`
	Size      string `json:"size,omitempty"`
	Color     string `json:"color,omitempty"`
}

type checkoutRequest struct {
	Email           string           `json:"email"`
	CustomerName    string           `json:"customer_name"`
	CustomerPhone   string           `json:"customer_phone"`
	ShippingAddress *ShippingAddress `json:"shipping_address"`
	Items           []checkoutItem   `json:"items"`
	Channel         string           `json:"channel,omitempty"`
	SessionID       string           `json:"session_id,omitempty"`
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToUpper(hex.EncodeToString(b))
}

func newReference(prefix string) string {
	return fmt.Sprintf("%s-%d%s", prefix, time.Now().UTC().Unix(), randHex(4))
}

func (a *App) checkout(w http.ResponseWriter, r *http.Request) {
	var req checkoutRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if !strings.Contains(req.Email, "@") {
		writeError(w, http.StatusBadRequest, "a valid customer email is required")
		return
	}
	req.CustomerName = strings.TrimSpace(req.CustomerName)
	if req.CustomerName == "" {
		writeError(w, http.StatusBadRequest, "customer name is required")
		return
	}
	req.CustomerPhone = strings.TrimSpace(req.CustomerPhone)
	if req.CustomerPhone == "" {
		writeError(w, http.StatusBadRequest, "customer phone number is required")
		return
	}
	if req.ShippingAddress == nil || strings.TrimSpace(req.ShippingAddress.Address) == "" || strings.TrimSpace(req.ShippingAddress.City) == "" {
		writeError(w, http.StatusBadRequest, "shipping address (address and city) is required for delivery")
		return
	}
	if len(req.Items) == 0 {
		writeError(w, http.StatusBadRequest, "cart is empty")
		return
	}
	req.Channel = normalizeChannel(req.Channel)
	if req.Channel != "" && !validChannel(req.Channel) {
		writeError(w, http.StatusBadRequest, "channel must be one of: card, bank_transfer, ussd")
		return
	}

	var items []OrderItem
	var total int64
	for _, it := range req.Items {
		if it.ProductID == "" {
			writeError(w, http.StatusBadRequest, "product_id is required for each item")
			return
		}
		if it.Quantity < 1 {
			writeError(w, http.StatusBadRequest, "quantity must be at least 1")
			return
		}
		var p Product
		err := a.products.FindOne(r.Context(), bson.M{"_id": it.ProductID}).Decode(&p)
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "product "+it.ProductID+" not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load product")
			return
		}
		if !p.Active {
			writeError(w, http.StatusBadRequest, "product "+p.Name+" is not available")
			return
		}
		total += int64(it.Quantity) * p.Price
		items = append(items, OrderItem{
			ProductID: p.ID,
			Name:      p.Name,
			Quantity:  it.Quantity,
			Price:     p.Price,
			Size:      it.Size,
			Color:     it.Color,
		})
	}

	now := time.Now().UTC()
	ref := newReference("LNT")
	order := Order{
		ID:              bson.NewObjectID().Hex(),
		Reference:       ref,
		SessionID:       strings.TrimSpace(req.SessionID),
		CustomerEmail:   req.Email,
		CustomerName:    req.CustomerName,
		CustomerPhone:   req.CustomerPhone,
		ShippingAddress: req.ShippingAddress,
		Items:           items,
		Subtotal:        total,
		Total:           total,
		Currency:        "NGN",
		Channel:         req.Channel,
		Status:          OrderPending,
		Tracking:        &Tracking{TrackingNumber: newReference("LANT")},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if _, err := a.orders.InsertOne(r.Context(), order); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create order")
		return
	}

	channels := defaultChannels
	if req.Channel != "" {
		channels = []string{req.Channel}
	}
	init, err := a.paystack.Initialize(r.Context(), req.Email, total, "NGN", ref, a.callbackURL, channels)
	if err != nil {
		a.orders.UpdateOne(r.Context(), bson.M{"_id": order.ID}, bson.M{"$set": bson.M{"status": OrderFailed}})
		writeError(w, http.StatusBadGateway, "could not initialize payment: "+err.Error())
		return
	}

	order.Payment = &PaymentInfo{
		PaystackReference: init.Reference,
		AccessCode:        init.AccessCode,
		AuthorizationURL:  init.AuthorizationURL,
	}
	if _, err := a.orders.UpdateOne(r.Context(), bson.M{"_id": order.ID}, bson.M{"$set": bson.M{"payment": order.Payment, "updated_at": now}}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save payment details")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"reference":         init.Reference,
		"authorization_url": init.AuthorizationURL,
		"access_code":       init.AccessCode,
		"amount":            total,
		"currency":          "NGN",
		"status":            OrderPending,
		"tracking_number":   order.Tracking.TrackingNumber,
	})
}

func (a *App) listOrders(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	filter := bson.M{}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		pattern := regexp.QuoteMeta(q)
		filter["$or"] = []bson.M{
			{"reference": bson.M{"$regex": pattern, "$options": "i"}},
			{"_id": bson.M{"$regex": pattern, "$options": "i"}},
			{"customer_email": bson.M{"$regex": pattern, "$options": "i"}},
		}
	}
	cursor, err := a.orders.Find(r.Context(), filter, findSortNewest().SetLimit(int64(limit)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list orders")
		return
	}
	var orders []Order
	if err := cursor.All(r.Context(), &orders); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decode orders")
		return
	}
	if orders == nil {
		orders = []Order{}
	}
	writeJSON(w, http.StatusOK, orders)
}

func (a *App) getOrder(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("reference")
	var o Order
	if err := a.orders.FindOne(r.Context(), bson.M{"reference": ref}).Decode(&o); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "order not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch order")
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func sanitizeOrderForCustomer(o Order) Order {
	if o.Payment != nil {
		cpy := *o.Payment
		cpy.AccessCode = ""
		cpy.AuthorizationURL = ""
		o.Payment = &cpy
	}
	return o
}

func (a *App) listMyOrders(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id is required")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	cursor, err := a.orders.Find(r.Context(), bson.M{"session_id": sessionID}, findSortNewest().SetLimit(int64(limit)))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list orders")
		return
	}
	var orders []Order
	if err := cursor.All(r.Context(), &orders); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decode orders")
		return
	}
	if orders == nil {
		orders = []Order{}
	}
	for i := range orders {
		orders[i] = sanitizeOrderForCustomer(orders[i])
	}
	writeJSON(w, http.StatusOK, orders)
}

func (a *App) verifyOrder(w http.ResponseWriter, r *http.Request) {
	ref := r.PathValue("reference")
	var o Order
	if err := a.orders.FindOne(r.Context(), bson.M{"reference": ref}).Decode(&o); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "order not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch order")
		return
	}

	ver, err := a.paystack.Verify(r.Context(), ref)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not verify payment: "+err.Error())
		return
	}

	status := OrderPending
	switch ver.Status {
	case "success":
		status = OrderPaid
	case "abandoned":
		status = OrderCancelled
	case "failed":
		status = OrderFailed
	}
	if o.Status == OrderCancelled {
		if ver.Status == "success" {
			a.refundLatePayment(r.Context(), &o)
		}
		writeJSON(w, http.StatusOK, o)
		return
	}
	if o.Status != status {
		a.applyPaymentResult(r.Context(), &o, ver.Status, ver.Amount, ver.Channel, ver.PaidAt)
	}
	writeJSON(w, http.StatusOK, o)
}

type cancelErr struct {
	code int
	msg  string
}

func (e *cancelErr) Error() string { return e.msg }

func (a *App) cancelOne(ctx context.Context, id string) (Order, error) {
	var o Order
	if err := a.orders.FindOne(ctx, bson.M{"_id": id}).Decode(&o); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return o, &cancelErr{http.StatusNotFound, "order not found"}
		}
		return o, &cancelErr{http.StatusInternalServerError, "failed to fetch order"}
	}

	switch o.Status {
	case OrderPending, OrderPaid:
	default:
		return o, &cancelErr{http.StatusConflict, "only pending or paid orders can be cancelled (current status: " + o.Status + ")"}
	}

	if o.Status == OrderPaid {
		ref := o.Reference
		if o.Payment != nil && o.Payment.PaystackReference != "" {
			ref = o.Payment.PaystackReference
		}
		if err := a.paystack.Refund(ctx, ref); err != nil {
			return o, &cancelErr{http.StatusBadGateway, "refund failed: " + err.Error()}
		}
		a.releaseStock(ctx, &o)
	}

	set := bson.M{"status": OrderCancelled, "updated_at": time.Now().UTC()}
	if _, err := a.orders.UpdateOne(ctx, bson.M{"_id": o.ID}, bson.M{"$set": set}); err != nil {
		return o, &cancelErr{http.StatusInternalServerError, "failed to cancel order"}
	}
	o.Status = OrderCancelled
	a.emailCancellation(ctx, &o)
	return o, nil
}

func (a *App) cancelOrder(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	o, err := a.cancelOne(r.Context(), id)
	if err != nil {
		if ce, ok := err.(*cancelErr); ok {
			writeError(w, ce.code, ce.msg)
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, sanitizeOrderForCustomer(o))
}

func (a *App) bulkCancelOrders(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "at least one order id is required")
		return
	}

	res := bulkOrderStatusResult{Failed: []bulkFailure{}}
	for _, id := range req.IDs {
		o, err := a.cancelOne(r.Context(), id)
		if err != nil {
			res.Failed = append(res.Failed, bulkFailure{ID: id, Error: err.Error()})
			continue
		}
		res.Updated++
		_ = o
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *App) releaseStock(ctx context.Context, o *Order) {
	now := time.Now().UTC()
	for _, it := range o.Items {
		a.products.UpdateOne(ctx, bson.M{"_id": it.ProductID}, bson.M{"$inc": bson.M{"stock": it.Quantity}, "$set": bson.M{"updated_at": now}})
	}
}

func (a *App) refundLatePayment(ctx context.Context, o *Order) {
	if o.Status != OrderCancelled || o.Payment == nil || o.Payment.AmountPaid <= 0 {
		return
	}
	ref := o.Reference
	if o.Payment.PaystackReference != "" {
		ref = o.Payment.PaystackReference
	}
	if err := a.paystack.Refund(ctx, ref); err != nil {
		log.Printf("[orders] late payment refund failed for %s: %v", o.Reference, err)
		return
	}
	a.releaseStock(ctx, o)
	log.Printf("[orders] refunded late payment for cancelled order %s", o.Reference)
}

func (a *App) paystackWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	if !a.paystack.VerifySignature(r.Header.Get("x-paystack-signature"), body) {
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	var evt struct {
		Event string `json:"event"`
		Data  struct {
			Reference string `json:"reference"`
			Amount    int64  `json:"amount"`
			Channel   string `json:"channel"`
			PaidAt    string `json:"paid_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &evt); err != nil {
		writeError(w, http.StatusBadRequest, "invalid webhook payload")
		return
	}

	if evt.Event == "charge.success" {
		var o Order
		err := a.orders.FindOne(r.Context(), bson.M{"reference": evt.Data.Reference}).Decode(&o)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				writeJSON(w, http.StatusOK, map[string]string{"status": "ignored", "reason": "unknown reference"})
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to fetch order")
			return
		}
		if o.Status != OrderPaid {
			paidAt, _ := time.Parse(time.RFC3339, evt.Data.PaidAt)
			if o.Status == OrderCancelled {
				a.refundLatePayment(r.Context(), &o)
			} else {
				a.applyPaymentResult(r.Context(), &o, "success", evt.Data.Amount, evt.Data.Channel, paidAt)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) applyPaymentResult(ctx context.Context, o *Order, paystackStatus string, amount int64, channel string, paidAt time.Time) {
	if paystackStatus != "success" {
		a.orders.UpdateOne(ctx, bson.M{"_id": o.ID}, bson.M{"$set": bson.M{"status": OrderFailed, "updated_at": time.Now().UTC()}})
		return
	}

	now := time.Now().UTC()
	set := bson.M{
		"status":              OrderPaid,
		"updated_at":          now,
		"payment.amount_paid": amount,
		"payment.channel":     channel,
	}
	if !paidAt.IsZero() {
		set["payment.paid_at"] = paidAt
	}
	if _, err := a.orders.UpdateOne(ctx, bson.M{"_id": o.ID}, set); err != nil {
		return
	}
	o.Status = OrderPaid
	if o.Payment == nil {
		o.Payment = &PaymentInfo{}
	}
	o.Payment.AmountPaid = amount
	o.Payment.Channel = channel
	if !paidAt.IsZero() {
		o.Payment.PaidAt = &paidAt
	}

	if err := a.allocateStock(ctx, o); err != nil {
		a.orders.UpdateOne(ctx, bson.M{"_id": o.ID}, bson.M{"$set": bson.M{"status": OrderBackordered, "updated_at": time.Now().UTC()}})
		o.Status = OrderBackordered
		a.emailBackorder(ctx, o)
		return
	}
	a.emailPaymentConfirmation(ctx, o)
}

func (a *App) allocateStock(ctx context.Context, o *Order) error {
	now := time.Now().UTC()
	for _, it := range o.Items {
		res, err := a.products.UpdateOne(
			ctx,
			bson.M{"_id": it.ProductID, "stock": bson.M{"$gte": it.Quantity}},
			bson.M{"$inc": bson.M{"stock": -it.Quantity}, "$set": bson.M{"updated_at": now}},
		)
		if err != nil {
			return err
		}
		if res.MatchedCount == 0 {
			return errors.New("insufficient stock for product " + it.ProductID)
		}
	}
	return nil
}

type trackingUpdate struct {
	Status   string `json:"status"`
	Location string `json:"location,omitempty"`
	Note     string `json:"note,omitempty"`
	Carrier  string `json:"carrier,omitempty"`
}

func (a *App) updateTracking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var u trackingUpdate
	if err := decodeJSON(w, r, &u); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	u.Status = strings.TrimSpace(u.Status)
	if u.Status == "" {
		writeError(w, http.StatusBadRequest, "status is required")
		return
	}

	var o Order
	if err := a.orders.FindOne(r.Context(), bson.M{"_id": id}).Decode(&o); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "order not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch order")
		return
	}

	ev := TrackingEvent{
		Status:   u.Status,
		Location: u.Location,
		Note:     u.Note,
		At:       time.Now().UTC(),
	}
	if o.Tracking == nil {
		o.Tracking = &Tracking{TrackingNumber: newReference("LANT")}
	}
	if u.Carrier != "" {
		o.Tracking.Carrier = u.Carrier
	}
	o.Tracking.Events = append(o.Tracking.Events, ev)

	set := bson.M{
		"tracking":   o.Tracking,
		"updated_at": time.Now().UTC(),
	}
	if status := orderStatusFromTracking(u.Status); status != "" {
		o.Status = status
		set["status"] = status
	}

	if _, err := a.orders.UpdateOne(r.Context(), bson.M{"_id": o.ID}, bson.M{"$set": set}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update tracking")
		return
	}
	a.emailTrackingUpdate(r.Context(), &o, ev)
	writeJSON(w, http.StatusOK, o.Tracking)
}

func orderStatusFromTracking(status string) string {
	s := strings.ToLower(status)
	switch {
	case strings.Contains(s, "delivered"):
		return OrderDelivered
	case strings.Contains(s, "shipped") || strings.Contains(s, "in transit") || strings.Contains(s, "dispatch") || strings.Contains(s, "out for delivery") || strings.Contains(s, "delivery"):
		return OrderFulfilled
	case strings.Contains(s, "process") || strings.Contains(s, "prepar") || strings.Contains(s, "pack"):
		return OrderProcessing
	}
	return ""
}

type bulkOrderStatusRequest struct {
	IDs      []string `json:"ids"`
	Status   string   `json:"status"`
	Location string   `json:"location,omitempty"`
	Note     string   `json:"note,omitempty"`
}

type bulkFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type bulkOrderStatusResult struct {
	Updated int           `json:"updated"`
	Failed  []bulkFailure `json:"failed"`
}

func (a *App) bulkOrderStatus(w http.ResponseWriter, r *http.Request) {
	var req bulkOrderStatusRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Status = strings.TrimSpace(req.Status)
	if req.Status == "" {
		writeError(w, http.StatusBadRequest, "status is required")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "at least one order id is required")
		return
	}

	now := time.Now().UTC()
	res := bulkOrderStatusResult{Failed: []bulkFailure{}}
	for _, id := range req.IDs {
		var o Order
		if err := a.orders.FindOne(r.Context(), bson.M{"_id": id}).Decode(&o); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				res.Failed = append(res.Failed, bulkFailure{ID: id, Error: "order not found"})
			} else {
				res.Failed = append(res.Failed, bulkFailure{ID: id, Error: "failed to load order"})
			}
			continue
		}
		if o.Status == OrderCancelled || o.Status == OrderFailed {
			res.Failed = append(res.Failed, bulkFailure{ID: id, Error: "order " + o.Reference + " is already " + strings.ToLower(o.Status)})
			continue
		}

		ev := TrackingEvent{Status: req.Status, Location: req.Location, Note: req.Note, At: now}
		if o.Tracking == nil {
			o.Tracking = &Tracking{TrackingNumber: newReference("LANT")}
		}
		o.Tracking.Events = append(o.Tracking.Events, ev)

		set := bson.M{"tracking": o.Tracking, "updated_at": now}
		if status := orderStatusFromTracking(req.Status); status != "" {
			o.Status = status
			set["status"] = status
		}

		if _, err := a.orders.UpdateOne(r.Context(), bson.M{"_id": o.ID}, bson.M{"$set": set}); err != nil {
			res.Failed = append(res.Failed, bulkFailure{ID: id, Error: "failed to update order " + o.Reference})
			continue
		}
		res.Updated++
		a.emailTrackingUpdate(r.Context(), &o, ev)
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *App) getTracking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var o Order
	if err := a.orders.FindOne(r.Context(), bson.M{"_id": id}).Decode(&o); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "order not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch order")
		return
	}
	if o.Tracking == nil {
		writeError(w, http.StatusNotFound, "no tracking yet")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tracking_number": o.Tracking.TrackingNumber,
		"carrier":         o.Tracking.Carrier,
		"events":          o.Tracking.Events,
		"order_status":    o.Status,
	})
}

func (a *App) getTrackingByNumber(w http.ResponseWriter, r *http.Request) {
	number := r.PathValue("number")
	var o Order
	if err := a.orders.FindOne(r.Context(), bson.M{"tracking.tracking_number": number}).Decode(&o); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "tracking not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch tracking")
		return
	}
	if o.Tracking == nil {
		writeError(w, http.StatusNotFound, "no tracking yet")
		return
	}
	if !a.isAdminRequest(r) {
		sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
		if o.SessionID == "" || sessionID == "" || o.SessionID != sessionID {
			writeError(w, http.StatusForbidden, "you can only track your own orders")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tracking_number": o.Tracking.TrackingNumber,
		"carrier":         o.Tracking.Carrier,
		"events":          o.Tracking.Events,
		"order_status":    o.Status,
	})
}
