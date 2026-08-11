package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"
)

type CloudinaryClient struct {
	cloudName string
	apiKey    string
	apiSecret string
	http      *http.Client
}

func NewCloudinaryClient(cloudName, apiKey, apiSecret string) *CloudinaryClient {
	return &CloudinaryClient{
		cloudName: cloudName,
		apiKey:    apiKey,
		apiSecret: apiSecret,
		http:      &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *CloudinaryClient) Enabled() bool {
	return c.cloudName != "" && c.apiKey != "" && c.apiSecret != ""
}

type UploadResult struct {
	SecureURL string `json:"secure_url"`
	PublicID  string `json:"public_id"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Format    string `json:"format"`
}

func (c *CloudinaryClient) Upload(ctx context.Context, folder string, imageData []byte) (*UploadResult, error) {
	if !c.Enabled() {
		return nil, errors.New("cloudinary is not configured (set CLOUDINARY_CLOUD_NAME, CLOUDINARY_KEY, CLOUDINARY_SECRET)")
	}

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := fmt.Sprintf("%x", sha1.Sum([]byte("folder="+folder+"&timestamp="+timestamp+c.apiSecret)))

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("folder", folder)
	_ = mw.WriteField("timestamp", timestamp)
	_ = mw.WriteField("api_key", c.apiKey)
	_ = mw.WriteField("signature", signature)
	part, err := mw.CreateFormFile("file", "image")
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(imageData); err != nil {
		return nil, err
	}
	_ = mw.Close()

	url := fmt.Sprintf("https://api.cloudinary.com/v1_1/%s/image/upload", c.cloudName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("cloudinary returned %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		UploadResult
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if out.Error != nil {
		return nil, errors.New(out.Error.Message)
	}
	return &out.UploadResult, nil
}
