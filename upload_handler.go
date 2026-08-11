package main

import (
	"io"
	"net/http"
)

func (a *App) uploadImage(w http.ResponseWriter, r *http.Request) {
	if !a.cloudinary.Enabled() {
		writeError(w, http.StatusServiceUnavailable, "cloudinary is not configured on the server")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "file too large or malformed form (max 10MB)")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing 'file' field in multipart form")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read file")
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty file")
		return
	}

	ct := http.DetectContentType(data)
	switch ct {
	case "image/jpeg", "image/png", "image/webp", "image/gif":
	default:
		writeError(w, http.StatusBadRequest, "unsupported image type "+ct+", expected jpeg, png, webp or gif")
		return
	}

	folder := "lanth"
	if f := r.FormValue("folder"); f != "" {
		folder = f
	}

	res, err := a.cloudinary.Upload(r.Context(), folder, data)
	if err != nil {
		writeError(w, http.StatusBadGateway, "cloudinary upload failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, res)
}
