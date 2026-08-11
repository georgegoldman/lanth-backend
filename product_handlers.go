package main

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type productInput struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Price           int64    `json:"price"`
	Currency        string   `json:"currency"`
	CollectionID    string   `json:"collection_id"`
	Collection      string   `json:"collection"`
	Images          []string `json:"images"`
	Sizes           []string `json:"sizes"`
	Colors          []string `json:"colors"`
	Brand           string   `json:"brand"`
	Merchant        string   `json:"merchant"`
	StoreCity       string   `json:"store_city"`
	Condition       string   `json:"condition"`
	ProductType     string   `json:"product_type"`
	DiscountPercent int      `json:"discount_percent"`
	SoldCount       int      `json:"sold_count"`
	Rating          float64  `json:"rating"`
	ReviewCount     int      `json:"review_count"`
	Stock           int      `json:"stock"`
	Active          bool     `json:"active"`
}

func (a *App) resolveCollection(r *http.Request, id, slug string) (*Collection, error) {
	if id != "" {
		c, err := a.collectionByID(r.Context(), id)
		if err != nil {
			return nil, err
		}
		if c != nil && !c.Active {
			return nil, nil
		}
		return c, nil
	}
	if slug != "" {
		c, err := a.collectionBySlug(r.Context(), slugify(slug))
		if err != nil {
			return nil, err
		}
		return c, nil
	}
	return nil, nil
}

func (a *App) createProduct(w http.ResponseWriter, r *http.Request) {
	var in productInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if in.Price <= 0 {
		writeError(w, http.StatusBadRequest, "price must be greater than 0 (in kobo, e.g. 50000 = ₦500)")
		return
	}
	if in.Stock < 0 {
		writeError(w, http.StatusBadRequest, "stock cannot be negative")
		return
	}
	if in.DiscountPercent < 0 || in.DiscountPercent > 100 {
		writeError(w, http.StatusBadRequest, "discount_percent must be between 0 and 100")
		return
	}
	if in.Rating < 0 || in.Rating > 5 {
		writeError(w, http.StatusBadRequest, "rating must be between 0 and 5")
		return
	}
	if in.SoldCount < 0 || in.ReviewCount < 0 {
		writeError(w, http.StatusBadRequest, "sold_count and review_count cannot be negative")
		return
	}

	c, err := a.resolveCollection(r, in.CollectionID, in.Collection)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collection")
		return
	}
	if c == nil {
		writeError(w, http.StatusBadRequest, "collection_id (or a valid collection slug) is required")
		return
	}
	path, err := a.buildCollectionPath(r.Context(), c.ID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	now := time.Now().UTC()
	p := Product{
		ID:              bson.NewObjectID().Hex(),
		Name:            strings.TrimSpace(in.Name),
		Description:     in.Description,
		Price:           in.Price,
		Currency:        strings.ToUpper(in.Currency),
		CollectionID:    c.ID,
		CollectionPath:  path,
		Images:          in.Images,
		Sizes:           in.Sizes,
		Colors:          in.Colors,
		Brand:           strings.TrimSpace(in.Brand),
		Merchant:        strings.TrimSpace(in.Merchant),
		StoreCity:       strings.TrimSpace(in.StoreCity),
		Condition:       strings.TrimSpace(in.Condition),
		ProductType:     strings.TrimSpace(in.ProductType),
		DiscountPercent: in.DiscountPercent,
		SoldCount:       in.SoldCount,
		Rating:          in.Rating,
		ReviewCount:     in.ReviewCount,
		Stock:           in.Stock,
		Active:          true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if p.Currency == "" {
		p.Currency = "NGN"
	}

	if _, err := a.products.InsertOne(r.Context(), p); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save product")
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (a *App) listProducts(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	skip := 0
	if v := r.URL.Query().Get("skip"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			skip = n
		}
	}

	filter := bson.M{"active": true}
	if slug := r.URL.Query().Get("collection"); slug != "" {
		filter["collection_path"] = slugify(slug)
	}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		pattern := regexp.QuoteMeta(q)
		filter["$or"] = []bson.M{
			{"name": bson.M{"$regex": pattern, "$options": "i"}},
			{"description": bson.M{"$regex": pattern, "$options": "i"}},
		}
	}

	cursor, err := a.products.Find(
		r.Context(),
		filter,
		findSortNewest().SetLimit(int64(limit)).SetSkip(int64(skip)),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list products")
		return
	}
	var products []Product
	if err := cursor.All(r.Context(), &products); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decode products")
		return
	}
	if products == nil {
		products = []Product{}
	}
	writeJSON(w, http.StatusOK, products)
}

func (a *App) getProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var p Product
	if err := a.products.FindOne(r.Context(), bson.M{"_id": id}).Decode(&p); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusNotFound, "product not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to fetch product")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *App) updateProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var u struct {
		Name            *string  `json:"name"`
		Description     *string  `json:"description"`
		Price           *int64   `json:"price"`
		Currency        *string  `json:"currency"`
		CollectionID    *string  `json:"collection_id"`
		Collection      *string  `json:"collection"`
		Images          []string `json:"images"`
		Sizes           []string `json:"sizes"`
		Colors          []string `json:"colors"`
		Brand           *string  `json:"brand"`
		Merchant        *string  `json:"merchant"`
		StoreCity       *string  `json:"store_city"`
		Condition       *string  `json:"condition"`
		ProductType     *string  `json:"product_type"`
		DiscountPercent *int     `json:"discount_percent"`
		SoldCount       *int     `json:"sold_count"`
		Rating          *float64 `json:"rating"`
		ReviewCount     *int     `json:"review_count"`
		Stock           *int     `json:"stock"`
		Active          *bool    `json:"active"`
	}
	if err := decodeJSON(w, r, &u); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	set := bson.M{"updated_at": time.Now().UTC()}
	if u.Name != nil {
		if strings.TrimSpace(*u.Name) == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		set["name"] = strings.TrimSpace(*u.Name)
	}
	if u.Description != nil {
		set["description"] = *u.Description
	}
	if u.Price != nil {
		if *u.Price <= 0 {
			writeError(w, http.StatusBadRequest, "price must be greater than 0 (in kobo)")
			return
		}
		set["price"] = *u.Price
	}
	if u.Currency != nil {
		set["currency"] = strings.ToUpper(*u.Currency)
	}
	if u.Images != nil {
		set["images"] = u.Images
	}
	if u.Sizes != nil {
		set["sizes"] = u.Sizes
	}
	if u.Colors != nil {
		set["colors"] = u.Colors
	}
	if u.Brand != nil {
		set["brand"] = strings.TrimSpace(*u.Brand)
	}
	if u.Merchant != nil {
		set["merchant"] = strings.TrimSpace(*u.Merchant)
	}
	if u.StoreCity != nil {
		set["store_city"] = strings.TrimSpace(*u.StoreCity)
	}
	if u.Condition != nil {
		set["condition"] = strings.TrimSpace(*u.Condition)
	}
	if u.ProductType != nil {
		set["product_type"] = strings.TrimSpace(*u.ProductType)
	}
	if u.DiscountPercent != nil {
		if *u.DiscountPercent < 0 || *u.DiscountPercent > 100 {
			writeError(w, http.StatusBadRequest, "discount_percent must be between 0 and 100")
			return
		}
		set["discount_percent"] = *u.DiscountPercent
	}
	if u.SoldCount != nil {
		if *u.SoldCount < 0 {
			writeError(w, http.StatusBadRequest, "sold_count cannot be negative")
			return
		}
		set["sold_count"] = *u.SoldCount
	}
	if u.Rating != nil {
		if *u.Rating < 0 || *u.Rating > 5 {
			writeError(w, http.StatusBadRequest, "rating must be between 0 and 5")
			return
		}
		set["rating"] = *u.Rating
	}
	if u.ReviewCount != nil {
		if *u.ReviewCount < 0 {
			writeError(w, http.StatusBadRequest, "review_count cannot be negative")
			return
		}
		set["review_count"] = *u.ReviewCount
	}
	if u.Stock != nil {
		if *u.Stock < 0 {
			writeError(w, http.StatusBadRequest, "stock cannot be negative")
			return
		}
		set["stock"] = *u.Stock
	}
	if u.Active != nil {
		set["active"] = *u.Active
	}

	if u.CollectionID != nil || u.Collection != nil {
		cid := ""
		slug := ""
		if u.CollectionID != nil {
			cid = *u.CollectionID
		}
		if u.Collection != nil {
			slug = *u.Collection
		}
		c, err := a.resolveCollection(r, cid, slug)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load collection")
			return
		}
		if c == nil {
			writeError(w, http.StatusBadRequest, "collection not found or inactive")
			return
		}
		path, err := a.buildCollectionPath(r.Context(), c.ID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		set["collection_id"] = c.ID
		set["collection_path"] = path
	}

	res, err := a.products.UpdateOne(r.Context(), bson.M{"_id": id}, bson.M{"$set": set})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update product")
		return
	}
	if res.MatchedCount == 0 {
		writeError(w, http.StatusNotFound, "product not found")
		return
	}
	var p Product
	if err := a.products.FindOne(r.Context(), bson.M{"_id": id}).Decode(&p); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch updated product")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *App) deleteProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := a.products.UpdateOne(r.Context(), bson.M{"_id": id}, bson.M{"$set": bson.M{"active": false, "updated_at": time.Now().UTC()}})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete product")
		return
	}
	if res.MatchedCount == 0 {
		writeError(w, http.StatusNotFound, "product not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
