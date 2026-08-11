package main

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type facetItem struct {
	Label string `json:"label" bson:"_id"`
	Count int    `json:"count" bson:"count"`
}

type priceRange struct {
	Min *int64 `json:"min" bson:"min"`
	Max *int64 `json:"max" bson:"max"`
}

type searchFilters struct {
	Brands      []facetItem `json:"brands" bson:"brands"`
	Merchants   []facetItem `json:"merchants" bson:"merchants"`
	Conditions  []facetItem `json:"conditions" bson:"conditions"`
	ProductType []facetItem `json:"product_types" bson:"product_types"`
	Sizes       []facetItem `json:"sizes" bson:"sizes"`
	PriceRange  []priceRange `json:"price_range" bson:"price_range"`
}

func facetGroup(field string) bson.A {
	return bson.A{
		bson.D{
			{Key: "$group", Value: bson.D{
				{Key: "_id", Value: "$" + field},
				{Key: "count", Value: bson.M{"$sum": 1}},
			}},
		},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}, {Key: "_id", Value: 1}}}},
	}
}

func (a *App) searchProducts(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	collection := r.URL.Query().Get("collection")
	brand := r.URL.Query().Get("brand")
	merchant := r.URL.Query().Get("merchant")
	condition := r.URL.Query().Get("condition")
	productType := r.URL.Query().Get("product_type")
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "popular"
	}

	minPrice := int64(-1)
	if v := r.URL.Query().Get("min_price"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			minPrice = n
		}
	}
	maxPrice := int64(-1)
	if v := r.URL.Query().Get("max_price"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			maxPrice = n
		}
	}

	page := 1
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			page = n
		}
	}
	limit := 24
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	// Base filter shared by the results and the facets.
	base := bson.M{"active": true}
	if collection != "" {
		base["collection_path"] = slugify(collection)
	}
	if q != "" {
		pattern := regexp.QuoteMeta(q)
		base["$or"] = []bson.M{
			{"name": bson.M{"$regex": pattern, "$options": "i"}},
			{"description": bson.M{"$regex": pattern, "$options": "i"}},
			{"brand": bson.M{"$regex": pattern, "$options": "i"}},
			{"merchant": bson.M{"$regex": pattern, "$options": "i"}},
		}
	}

	// Result filter = base + active facet selections + price range.
	resultFilter := bson.M{}
	for k, v := range base {
		resultFilter[k] = v
	}
	if brand != "" {
		resultFilter["brand"] = brand
	}
	if merchant != "" {
		resultFilter["merchant"] = merchant
	}
	if condition != "" {
		resultFilter["condition"] = condition
	}
	if productType != "" {
		resultFilter["product_type"] = productType
	}
	if minPrice >= 0 {
		resultFilter["price"] = bson.M{"$gte": minPrice}
	}
	if maxPrice >= 0 {
		if cur, ok := resultFilter["price"].(bson.M); ok {
			cur["$lte"] = maxPrice
		} else {
			resultFilter["price"] = bson.M{"$lte": maxPrice}
		}
	}

	sort := bson.D{{Key: "sold_count", Value: -1}, {Key: "rating", Value: -1}}
	switch sortBy {
	case "newest":
		sort = bson.D{{Key: "created_at", Value: -1}}
	case "price_asc":
		sort = bson.D{{Key: "price", Value: 1}}
	case "price_desc":
		sort = bson.D{{Key: "price", Value: -1}}
	case "reviews":
		sort = bson.D{{Key: "review_count", Value: -1}}
	case "rating":
		sort = bson.D{{Key: "rating", Value: -1}}
	}

	total, err := a.products.CountDocuments(r.Context(), resultFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count products")
		return
	}
	totalPages := int((total + int64(limit) - 1) / int64(limit))
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	cursor, err := a.products.Find(
		r.Context(),
		resultFilter,
		options.Find().SetSort(sort).SetSkip(int64((page-1)*limit)).SetLimit(int64(limit)),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search products")
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

	// Facets: filter options available for the current search/collection.
	facet := mongo.Pipeline{
		{{Key: "$match", Value: base}},
		{{Key: "$facet", Value: bson.D{
			{Key: "brands", Value: facetGroup("brand")},
			{Key: "merchants", Value: facetGroup("merchant")},
			{Key: "conditions", Value: facetGroup("condition")},
			{Key: "product_types", Value: facetGroup("product_type")},
			{Key: "sizes", Value: bson.A{
				bson.D{{Key: "$unwind", Value: "$sizes"}},
				bson.D{
					{Key: "$group", Value: bson.D{
						{Key: "_id", Value: "$sizes"},
						{Key: "count", Value: bson.M{"$sum": 1}},
					}},
				},
				bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
			}},
			{Key: "price_range", Value: bson.A{
				bson.D{
					{Key: "$group", Value: bson.D{
						{Key: "_id", Value: nil},
						{Key: "min", Value: bson.M{"$min": "$price"}},
						{Key: "max", Value: bson.M{"$max": "$price"}},
					}},
				},
			}},
		}}},
	}

	cur, err := a.products.Aggregate(r.Context(), facet)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build filters")
		return
	}
	var fr []searchFilters
	if err := cur.All(r.Context(), &fr); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to decode filters")
		return
	}
	filters := searchFilters{Brands: []facetItem{}, Merchants: []facetItem{}, Conditions: []facetItem{}, ProductType: []facetItem{}, Sizes: []facetItem{}, PriceRange: []priceRange{}}
	if len(fr) > 0 {
		filters = fr[0]
	}
	if filters.Brands == nil {
		filters.Brands = []facetItem{}
	}
	if filters.Merchants == nil {
		filters.Merchants = []facetItem{}
	}
	if filters.Conditions == nil {
		filters.Conditions = []facetItem{}
	}
	if filters.ProductType == nil {
		filters.ProductType = []facetItem{}
	}
	if filters.Sizes == nil {
		filters.Sizes = []facetItem{}
	}
	if filters.PriceRange == nil {
		filters.PriceRange = []priceRange{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"products":      products,
		"total_results": total,
		"current_page":  page,
		"total_pages":   totalPages,
		"filters":       filters,
	})
}
