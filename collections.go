package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '_' || r == '-' || r == '/' || r == '&':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = strings.ToLower(randHex(4))
	}
	return out
}

func (a *App) allCollections(ctx context.Context) ([]Collection, error) {
	cursor, err := a.collections.Find(ctx, bson.M{"active": true}, findSortByName())
	if err != nil {
		return nil, err
	}
	var all []Collection
	if err := cursor.All(ctx, &all); err != nil {
		return nil, err
	}
	if all == nil {
		all = []Collection{}
	}
	return all, nil
}

func (a *App) collectionBySlug(ctx context.Context, slug string) (*Collection, error) {
	var c Collection
	if err := a.collections.FindOne(ctx, bson.M{"active": true, "slug": slug}).Decode(&c); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (a *App) collectionByID(ctx context.Context, id string) (*Collection, error) {
	var c Collection
	if err := a.collections.FindOne(ctx, bson.M{"_id": id}).Decode(&c); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (a *App) buildCollectionPath(ctx context.Context, leafID string) ([]string, error) {
	all, err := a.allCollections(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Collection, len(all))
	for _, c := range all {
		byID[c.ID] = c
	}
	return collectionPathOf(byID, leafID)
}

func collectionPathOf(byID map[string]Collection, id string) ([]string, error) {
	var path []string
	seen := make(map[string]bool)
	cur := id
	for cur != "" {
		if seen[cur] {
			return nil, errors.New("cycle detected in collection tree")
		}
		seen[cur] = true
		c, ok := byID[cur]
		if !ok {
			return nil, errors.New("collection not found: " + cur)
		}
		path = append([]string{c.Slug}, path...)
		cur = c.ParentID
	}
	return path, nil
}

func descendantsOf(all []Collection, rootID string) map[string]bool {
	children := make(map[string][]string)
	for _, c := range all {
		if c.ParentID != "" {
			children[c.ParentID] = append(children[c.ParentID], c.ID)
		}
	}
	desc := make(map[string]bool)
	queue := children[rootID]
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if desc[id] {
			continue
		}
		desc[id] = true
		queue = append(queue, children[id]...)
	}
	return desc
}

func (a *App) productCounts(ctx context.Context) (map[string]int, error) {
	cursor, err := a.products.Aggregate(ctx, bson.A{
		bson.M{"$match": bson.M{"active": true}},
		bson.M{"$unwind": "$collection_path"},
		bson.M{"$group": bson.M{"_id": "$collection_path", "count": bson.M{"$sum": 1}}},
	})
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	var rows []struct {
		Slug  string `bson:"_id"`
		Count int    `bson:"count"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		counts[r.Slug] = r.Count
	}
	return counts, nil
}

func buildTree(all []Collection, counts map[string]int) []*Collection {
	byID := make(map[string]*Collection, len(all))
	for i := range all {
		c := all[i]
		c.ProductCount = counts[c.Slug]
		if c.Children == nil {
			c.Children = []*Collection{}
		}
		byID[c.ID] = &c
	}
	var roots []*Collection
	for _, c := range byID {
		if c.ParentID == "" || byID[c.ParentID] == nil {
			roots = append(roots, c)
			continue
		}
		byID[c.ParentID].Children = append(byID[c.ParentID].Children, c)
	}
	return roots
}

func (a *App) listCollections(w http.ResponseWriter, r *http.Request) {
	all, err := a.allCollections(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collections")
		return
	}
	counts, err := a.productCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count products")
		return
	}
	roots := buildTree(all, counts)
	writeJSON(w, http.StatusOK, roots)
}

func (a *App) getCollection(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	c, err := a.collectionBySlug(r.Context(), slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collection")
		return
	}
	if c == nil {
		writeError(w, http.StatusNotFound, "collection not found")
		return
	}

	all, err := a.allCollections(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collections")
		return
	}
	for _, child := range all {
		if child.ParentID == c.ID {
			c.Children = append(c.Children, &child)
		}
	}

	products, err := a.productsByCollectionSlug(r.Context(), slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load products")
		return
	}
	c.ProductCount = len(products)
	if c.Image == "" && len(products) > 0 && len(products[0].Images) > 0 {
		c.Image = products[0].Images[0]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"collection": c,
		"products":   products,
	})
}

func (a *App) productsByCollectionSlug(ctx context.Context, slug string) ([]Product, error) {
	cursor, err := a.products.Find(ctx, bson.M{"active": true, "collection_path": slug}, findSortNewest())
	if err != nil {
		return nil, err
	}
	var products []Product
	if err := cursor.All(ctx, &products); err != nil {
		return nil, err
	}
	if products == nil {
		products = []Product{}
	}
	return products, nil
}

type collectionRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
	ParentID    string `json:"parent_id,omitempty"`
	Image       string `json:"image,omitempty"`
}

func (a *App) createCollection(w http.ResponseWriter, r *http.Request) {
	var req collectionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	slug := slugify(req.Name)
	if strings.TrimSpace(req.Slug) != "" {
		slug = slugify(req.Slug)
	}

	var other Collection
	err := a.collections.FindOne(r.Context(), bson.M{"slug": slug}).Decode(&other)
	if err == nil {
		writeError(w, http.StatusConflict, "a collection with slug "+slug+" already exists")
		return
	}
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		writeError(w, http.StatusInternalServerError, "failed to check slug")
		return
	}

	parent := ""
	if req.ParentID != "" {
		pc, err := a.collectionByID(r.Context(), req.ParentID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load parent")
			return
		}
		if pc == nil || !pc.Active {
			writeError(w, http.StatusBadRequest, "parent collection not found or inactive")
			return
		}
		parent = pc.ID
	}

	now := time.Now().UTC()
	c := Collection{
		ID:          bson.NewObjectID().Hex(),
		Slug:        slug,
		Name:        strings.TrimSpace(req.Name),
		Description: req.Description,
		ParentID:    parent,
		Image:       req.Image,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if _, err := a.collections.InsertOne(r.Context(), c); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create collection")
		return
	}
	c.Children = []*Collection{}
	writeJSON(w, http.StatusCreated, c)
}

func (a *App) updateCollection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name        *string `json:"name"`
		Slug        *string `json:"slug"`
		Description *string `json:"description"`
		ParentID    *string `json:"parent_id"`
		Image       *string `json:"image"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	cur, err := a.collectionByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collection")
		return
	}
	if cur == nil {
		writeError(w, http.StatusNotFound, "collection not found")
		return
	}

	set := bson.M{"updated_at": time.Now().UTC()}

	if req.Name != nil {
		if strings.TrimSpace(*req.Name) == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		set["name"] = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		set["description"] = *req.Description
	}
	if req.Image != nil {
		set["image"] = *req.Image
	}

	newParent := cur.ParentID
	if req.ParentID != nil {
		newParent = *req.ParentID
		if newParent == id {
			writeError(w, http.StatusBadRequest, "a collection cannot be its own parent")
			return
		}
		if newParent != "" {
			all, err := a.allCollections(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load collections")
				return
			}
			pc, err := a.collectionByID(r.Context(), newParent)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load parent")
				return
			}
			if pc == nil || !pc.Active {
				writeError(w, http.StatusBadRequest, "parent collection not found or inactive")
				return
			}
			if descendantsOf(all, id)[newParent] {
				writeError(w, http.StatusBadRequest, "cannot move a collection under its own descendant")
				return
			}
		}
		set["parent_id"] = newParent
	}

	newSlug := cur.Slug
	if req.Slug != nil {
		ns := slugify(*req.Slug)
		if ns == "" {
			writeError(w, http.StatusBadRequest, "slug cannot be empty")
			return
		}
		var other Collection
		err := a.collections.FindOne(r.Context(), bson.M{"slug": ns, "_id": bson.M{"$ne": id}}).Decode(&other)
		if err == nil {
			writeError(w, http.StatusConflict, "a collection with slug "+ns+" already exists")
			return
		}
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			writeError(w, http.StatusInternalServerError, "failed to check slug")
			return
		}
		newSlug = ns
		set["slug"] = ns
	}

	if _, err := a.collections.UpdateOne(r.Context(), bson.M{"_id": id}, bson.M{"$set": set}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update collection")
		return
	}

	if err := a.reindexSubtree(r.Context(), id, newSlug, newParent); err != nil {
		writeError(w, http.StatusInternalServerError, "collection updated but failed to reindex products: "+err.Error())
		return
	}

	updated, err := a.collectionByID(r.Context(), id)
	if err != nil || updated == nil {
		writeError(w, http.StatusInternalServerError, "failed to reload collection")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (a *App) reindexSubtree(ctx context.Context, rootID, newRootSlug, newParent string) error {
	all, err := a.allCollections(ctx)
	if err != nil {
		return err
	}

	byID := make(map[string]Collection, len(all))
	for _, c := range all {
		byID[c.ID] = c
	}
	all = append(all, byID[rootID])

	base, err := collectionPathOf(byID, newParent)
	if err != nil {
		return err
	}

	paths := make(map[string][]string)
	paths[rootID] = append(base, newRootSlug)
	queue := []string{rootID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range all {
			if c.ParentID == cur {
				paths[c.ID] = append(append([]string{}, paths[cur]...), c.Slug)
				queue = append(queue, c.ID)
			}
		}
	}

	for nodeID, path := range paths {
		if _, err := a.products.UpdateMany(
			ctx,
			bson.M{"collection_id": nodeID},
			bson.M{"$set": bson.M{"collection_path": path}},
		); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) deleteCollection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cur, err := a.collectionByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collection")
		return
	}
	if cur == nil {
		writeError(w, http.StatusNotFound, "collection not found")
		return
	}

	all, err := a.allCollections(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load collections")
		return
	}
	ids := append([]string{id}, keysOf(descendantsOf(all, id))...)

	filter := bson.M{"_id": bson.M{"$in": ids}}
	if _, err := a.collections.UpdateMany(r.Context(), filter, bson.M{"$set": bson.M{"active": false, "updated_at": time.Now().UTC()}}); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete collection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "collections": len(ids)})
}

func keysOf(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func findSortNewest() *options.FindOptionsBuilder {
	return options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
}

func findSortByName() *options.FindOptionsBuilder {
	return options.Find().SetSort(bson.D{{Key: "name", Value: 1}})
}

func (a *App) seedDefaultCollections(ctx context.Context) error {
	n, err := a.collections.CountDocuments(ctx, bson.M{})
	if err != nil {
		return fmt.Errorf("failed to count collections: %w", err)
	}
	if n > 0 {
		return nil
	}

	now := time.Now().UTC()
	mk := func(slug, name, desc string) Collection {
		return Collection{
			ID:          bson.NewObjectID().Hex(),
			Slug:        slug,
			Name:        name,
			Description: desc,
			Active:      true,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}

	watches := mk("watches", "Watches", "High-end wristwatches crafted to amplify your daily motion.")
	shoes := mk("shoes", "Shoes", "Signature footwear for the road ahead.")
	jerseys := mk("jerseys", "Jerseys", "Performance jerseys for game day and every day.")
	clothing := mk("clothing", "Clothing", "The cloth behind the motion — tees, hoodies and more.")

	top := []any{watches, shoes, jerseys, clothing}
	if _, err := a.collections.InsertMany(ctx, top); err != nil {
		return err
	}

	sneakers := mk("sneakers", "Sneakers", "Premium sneakers built for effortless movement.")
	sneakers.ParentID = shoes.ID
	trackers := mk("active-trackers", "Active Trackers", "Movement gear that keeps pace with you.")
	trackers.ParentID = watches.ID
	shirts := mk("shirts", "Shirts", "Sharp shirts to amplify your everyday motion.")
	shirts.ParentID = clothing.ID

	if _, err := a.collections.InsertMany(ctx, []any{sneakers, trackers, shirts}); err != nil {
		return err
	}
	return nil
}
