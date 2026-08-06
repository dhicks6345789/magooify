package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	openRouterDefaultURL = "https://openrouter.ai/api/v1/chat/completions"
	defaultModel         = "google/gemini-3.1-flash-lite-image"
	defaultOutputDir     = "processed"
	fallbackPrompt       = "Describe the image in detail, including any text, people, objects and how they are arranged. Be specific and thorough."
	maxImageUploadBytes  = 25 << 20
	maxOpenRouterBytes   = 25 << 20
)

// processPrompt returns the image-processing instructions sent to the model.
// If a prompt file was supplied with -prompt-file, its text is read from disk;
// otherwise the text embedded from PROMPT.md is used. If neither is available
// (or is empty) a built-in prompt is used.
func (a *api) processPrompt() string {
	if a.promptFile != "" {
		if data, err := os.ReadFile(a.promptFile); err == nil {
			if p := strings.TrimSpace(string(data)); p != "" {
				return p
			}
		}
	}
	if p := strings.TrimSpace(promptMD); p != "" {
		return p
	}
	return fallbackPrompt
}

// errOpenRouterNotConfigured is returned when the OpenRouter API key has not
// been supplied via the -openrouter-key command-line option.
var errOpenRouterNotConfigured = errors.New("OpenRouter API key not configured; start with -openrouter-key=<key>")

// Item is an application resource managed through the API.
type Item struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateItemRequest is the payload accepted when creating a new item.
type CreateItemRequest struct {
	Name string `json:"name" validate:"required" example:"Sample Item"`
}

// UserInfo describes the currently authenticated user.
type UserInfo struct {
	Username string `json:"username" example:"alice"`
	AuthType string `json:"auth_type" example:"local_env"`
	Mode     string `json:"mode" example:"desktop"`
}

// HealthResponse describes the service health status.
type HealthResponse struct {
	Status    string `json:"status" example:"ok"`
	Timestamp string `json:"timestamp" example:"2026-08-04T12:00:00Z"`
}

// SystemInfoResponse describes system information.
type SystemInfoResponse struct {
	Mode      string `json:"mode" example:"desktop"`
	GoVersion string `json:"go_version" example:"go1.24.4"`
	OS        string `json:"os" example:"linux"`
	Arch      string `json:"arch" example:"amd64"`
	Uptime    string `json:"uptime" example:"12m30s"`
}

// ItemsResponse is the payload returned when listing items.
type ItemsResponse struct {
	Items []Item `json:"items"`
}

// ErrorResponse describes an error payload.
type ErrorResponse struct {
	Error string `json:"error" example:"Invalid request payload"`
}

// ProcessImageRequest is the JSON payload accepted by the image processing
// endpoint. The Image field holds the image as base64 (optionally wrapped in a
// data URL) and may be omitted when sending multipart form data instead.
// Prompt overrides the prompt sent to the model; Output names the file the
// processed image is written to (overwriting any existing file).
type ProcessImageRequest struct {
	Image    string `json:"image" example:"data:image/jpeg;base64,..."`
	Filename string `json:"filename" example:"photo.jpg"`
	Prompt   string `json:"prompt" example:"Clean this scanned image"`
	Output   string `json:"output" example:"img-20260806-123000-a1b2c3d4.jpg"`
}

// ProcessImageResponse describes the outcome of processing an image.
type ProcessImageResponse struct {
	Filename    string    `json:"filename" example:"img-20260806-123000-a1b2c3d4.jpg"`
	Model       string    `json:"model" example:"google/gemini-3.1-flash-lite-image"`
	ProcessedAt time.Time `json:"processed_at"`
}

// StoredImage describes a processed image stored on the file system.
type StoredImage struct {
	Filename string    `json:"filename" example:"img-20260806-123000-a1b2c3d4.jpg"`
	Size     int64     `json:"size" example:"48291"`
	Modified time.Time `json:"modified"`
}

// ImagesResponse is the payload returned when listing stored images.
type ImagesResponse struct {
	Images []StoredImage `json:"images"`
}

// proxyHeaders lists headers set by authenticating reverse proxies
// (Pangolin, Traefik, Cloudflare Tunnel, Authelia, Tailscale, etc.).
var proxyHeaders = []string{
	"X-Forwarded-User",
	"Remote-User",
	"X-User",
	"CF-Access-Authenticated-User-Email",
	"X-Auth-Request-User",
	"Pangolin-User",
}

// api holds the state shared by the API handlers.
type api struct {
	startTime      time.Time
	isServerMode   bool
	items          []Item
	nextID         int
	mu             sync.RWMutex
	docsFS         fs.FS
	docsFileServer http.Handler
	openRouterKey  string
	openRouterURL  string
	model          string
	outputDir      string
	promptFile     string
	httpClient     *http.Client
}

// @title Magooify API
// @description API documentation for Magooify, a self-contained Go application framework.
// @version 1.0.0
func newAPI(isServerMode bool, docsFS fs.FS) *api {
	a := &api{
		startTime:    time.Now(),
		isServerMode: isServerMode,
		items: []Item{
			{
				ID:        1,
				Name:      "Welcome to Magooify",
				CreatedBy: "system",
				CreatedAt: time.Now(),
			},
		},
		nextID:        2,
		docsFS:        docsFS,
		openRouterURL: openRouterDefaultURL,
		model:         defaultModel,
		outputDir:     defaultOutputDir,
		httpClient:    &http.Client{Timeout: 120 * time.Second},
	}

	if sub, err := fs.Sub(docsFS, "docs"); err == nil {
		a.docsFileServer = http.FileServer(http.FS(sub))
	}

	return a
}

// getUser resolves the current user from the local environment (desktop mode)
// or from headers set by the authenticating reverse proxy (server mode).
func (a *api) getUser(r *http.Request) UserInfo {
	if a.isServerMode {
		for _, header := range proxyHeaders {
			if val := r.Header.Get(header); val != "" {
				return UserInfo{
					Username: strings.TrimSpace(val),
					AuthType: "proxy_header (" + header + ")",
					Mode:     "server",
				}
			}
		}
		return UserInfo{
			Username: "anonymous",
			AuthType: "none",
			Mode:     "server",
		}
	}

	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}
	if username == "" {
		username = os.Getenv("LOGNAME")
	}
	if username == "" {
		username = "localuser"
	}

	return UserInfo{
		Username: username,
		AuthType: "local_env",
		Mode:     "desktop",
	}
}

// health returns the operational status of the service.
// @Summary Health Check
// @Description Returns operational status of the service.
// @Produce json
// @Success 200 {object} HealthResponse "Healthy response"
// @Router /api/v1/health [get]
func (a *api) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":    "ok",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// user returns the currently authenticated user.
// @Summary Get Current User
// @Description Returns authenticated user info based on environment or proxy headers.
// @Produce json
// @Success 200 {object} UserInfo
// @Router /api/v1/user [get]
func (a *api) user(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.getUser(r))
}

// info returns system information such as mode, Go version, OS/Arch and uptime.
// @Summary System Info
// @Description Returns system metrics, Go version, OS/Arch, and uptime.
// @Produce json
// @Success 200 {object} SystemInfoResponse
// @Router /api/v1/info [get]
func (a *api) info(w http.ResponseWriter, r *http.Request) {
	mode := "desktop"
	if a.isServerMode {
		mode = "server"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"mode":       mode,
		"go_version": runtime.Version(),
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"uptime":     time.Since(a.startTime).Truncate(time.Second).String(),
	})
}

// prompt returns the current processing prompt so the UI can show and let the
// user edit what is sent to the model.
// @Summary Get Processing Prompt
// @Description Returns the prompt text sent to the model with each image.
// @Produce json
// @Success 200 {object} map[string]string
// @Router /api/v1/prompt [get]
func (a *api) prompt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"prompt": a.processPrompt()})
}

// listItems returns all stored items.
// @Summary List Items
// @Description Returns all stored items.
// @Produce json
// @Success 200 {object} ItemsResponse
// @Router /api/v1/items [get]
func (a *api) listItems(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"items": a.items,
	})
}

// createItem creates a new item and returns it.
// @Summary Create Item
// @Description Creates a new item.
// @Accept json
// @Produce json
// @Param body body CreateItemRequest true "Item to create"
// @Success 201 {object} Item
// @Failure 400 {object} ErrorResponse
// @Router /api/v1/items [post]
func (a *api) createItem(w http.ResponseWriter, r *http.Request) {
	var req CreateItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"Invalid request payload"}`))
		return
	}

	user := a.getUser(r)

	a.mu.Lock()
	newItem := Item{
		ID:        a.nextID,
		Name:      req.Name,
		CreatedBy: user.Username,
		CreatedAt: time.Now(),
	}
	a.nextID++
	a.items = append(a.items, newItem)
	a.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(newItem)
}

// imagePayload holds the parsed contents of an image processing request.
type imagePayload struct {
	img         []byte
	filename    string
	contentType string
	prompt      string
	output      string
}

// processImage accepts an image (multipart upload or JSON base64/data URL),
// sends it to an OpenRouter vision model for processing, and stores the image
// and the resulting description on the file system.
// @Summary Process an Image with OpenRouter
// @Description Accepts an image captured by the camera or uploaded by the user, sends it to an OpenRouter vision model for processing, then stores the processed version of the image in the configured output directory.
// @Accept mpfd
// @Produce json
// @Param image formData file true "Image to process"
// @Param prompt formData string false "Prompt to send with the image; defaults to the configured prompt"
// @Param output formData string false "Optional filename to write the processed image to, replacing any existing file with that name"
// @Success 200 {object} ProcessImageResponse
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /api/v1/process [post]
func (a *api) processImage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImageUploadBytes)

	pl, err := readImagePayload(r)
	if err != nil {
		a.jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Trust a declared image content type, otherwise sniff the bytes (browsers
	// send application/octet-stream for multipart file parts).
	if !strings.HasPrefix(pl.contentType, "image/") {
		pl.contentType = http.DetectContentType(pl.img)
	}
	if !strings.HasPrefix(pl.contentType, "image/") {
		a.jsonError(w, http.StatusBadRequest, "Uploaded file is not a recognised image")
		return
	}

	processed, err := a.processWithOpenRouter(pl.img, pl.contentType, pl.prompt)
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errOpenRouterNotConfigured) {
			code = http.StatusServiceUnavailable
		}
		a.jsonError(w, code, err.Error())
		return
	}

	imageFile, err := a.storeResult(processed, pl.output)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, "Failed to store processed image: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ProcessImageResponse{
		Filename:    imageFile,
		Model:       a.model,
		ProcessedAt: time.Now().UTC(),
	})
}

// readImagePayload extracts the image bytes, original filename, content type,
// prompt and output filename from either a JSON payload (base64 or data URL in
// the "image" field) or a multipart form upload (the "image" file part).
func readImagePayload(r *http.Request) (*imagePayload, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var req ProcessImageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Image == "" {
			return nil, errors.New("Missing image payload")
		}
		data := req.Image
		if idx := strings.Index(data, ","); idx >= 0 && strings.HasPrefix(data, "data:") {
			data = data[idx+1:]
		}
		img, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
		if err != nil {
			return nil, errors.New("Invalid base64 image payload")
		}
		return &imagePayload{
			img:      img,
			filename: req.Filename,
			prompt:   req.Prompt,
			output:   req.Output,
		}, nil
	}

	if err := r.ParseMultipartForm(maxImageUploadBytes); err != nil {
		return nil, errors.New("Invalid multipart form data")
	}
	f, hdr, err := r.FormFile("image")
	if err != nil {
		return nil, errors.New("Missing 'image' file field")
	}
	defer f.Close()
	img, err := io.ReadAll(f)
	if err != nil {
		return nil, errors.New("Failed to read uploaded image")
	}
	filename := hdr.Filename
	if filename == "" {
		filename = "upload.jpg"
	}
	return &imagePayload{
		img:         img,
		filename:    filename,
		contentType: hdr.Header.Get("Content-Type"),
		prompt:      r.FormValue("prompt"),
		output:      r.FormValue("output"),
	}, nil
}

// processWithOpenRouter sends the image to the configured OpenRouter image
// model and returns the processed image returned by the model. When prompt is
// empty the configured prompt is used.
func (a *api) processWithOpenRouter(img []byte, contentType, prompt string) ([]byte, error) {
	if a.openRouterKey == "" {
		return nil, errOpenRouterNotConfigured
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = a.processPrompt()
	}

	dataURL := "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(img)
	payload := map[string]any{
		"model":      a.model,
		"modalities": []string{"image", "text"},
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": prompt},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("Failed to build OpenRouter request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, a.openRouterURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("Failed to create OpenRouter request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.openRouterKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("OpenRouter request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenRouterBytes+1))
	if err != nil {
		return nil, fmt.Errorf("Failed to read OpenRouter response: %w", err)
	}
	if len(respBody) > maxOpenRouterBytes {
		return nil, errors.New("OpenRouter response exceeded the 25 MB size limit")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenRouter returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if len(respBody) == 0 {
		return nil, errors.New("OpenRouter returned an empty response (HTTP 200); the model may have failed to produce an image. Please try again")
	}

	return extractImageFromResponse(respBody)
}

// extractImageFromResponse pulls the processed image out of an OpenRouter
// response. Image-capable models return the generated image inside the message
// content as an image_url part (a base64 data URL) or in a top-level "images"
// array; some models return the data URL directly as the content string.
func extractImageFromResponse(respBody []byte) ([]byte, error) {
	var chat struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
				Images  []struct {
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"images"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chat); err != nil {
		return nil, fmt.Errorf("OpenRouter returned a response that could not be parsed (%v); the model may have failed or returned an unexpected format. Please try again", err)
	}
	if len(chat.Choices) == 0 {
		return nil, errors.New("OpenRouter returned no choices")
	}

	msg := chat.Choices[0].Message

	for _, im := range msg.Images {
		if img, ok := decodeDataURL(im.ImageURL.URL); ok {
			return img, nil
		}
	}

	if len(msg.Content) == 0 {
		return nil, errors.New("OpenRouter returned no image output")
	}

	// content may be a JSON string (a data URL) or an array of content parts.
	var contentStr string
	if json.Unmarshal(msg.Content, &contentStr) == nil {
		if img, ok := decodeDataURL(contentStr); ok {
			return img, nil
		}
		return nil, errors.New("OpenRouter returned no image output")
	}

	var parts []struct {
		Type     string `json:"type"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if err := json.Unmarshal(msg.Content, &parts); err != nil {
		return nil, fmt.Errorf("Failed to parse OpenRouter content parts: %w", err)
	}
	for _, p := range parts {
		if p.ImageURL != nil {
			if img, ok := decodeDataURL(p.ImageURL.URL); ok {
				return img, nil
			}
		}
	}
	return nil, errors.New("OpenRouter returned no image output")
}

// decodeDataURL decodes a base64 data URL into its raw bytes. The second
// return value reports whether the string looked like a valid data URL.
func decodeDataURL(s string) ([]byte, bool) {
	if !strings.HasPrefix(s, "data:") {
		return nil, false
	}
	comma := strings.Index(s, ",")
	if comma < 0 {
		return nil, false
	}
	meta := s[:comma]
	payload := s[comma+1:]
	if !strings.Contains(meta, ";base64") {
		if decoded, err := url.PathUnescape(payload); err == nil {
			return []byte(decoded), true
		}
		return nil, false
	}
	img, err := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if err != nil {
		return nil, false
	}
	return img, true
}

// storeResult writes the processed image to the configured output directory
// under a unique timestamped base name, or under the given output filename when
// one is supplied (overwriting any existing file with that name).
func (a *api) storeResult(img []byte, output string) (string, error) {
	if err := os.MkdirAll(a.outputDir, 0o755); err != nil {
		return "", err
	}

	imageFile := output
	if imageFile == "" {
		contentType := http.DetectContentType(img)
		imageFile = "img-" + uniqueName() + extensionForContentType(contentType)
	} else {
		// The filename is matched against a single path segment so directory
		// traversal is not possible.
		if filepath.Base(imageFile) != imageFile || imageFile == "." || imageFile == ".." {
			return "", errors.New("invalid output filename")
		}
	}

	if err := os.WriteFile(filepath.Join(a.outputDir, imageFile), img, 0o644); err != nil {
		return "", err
	}
	return imageFile, nil
}

// uniqueName builds a collision-resistant name from a UTC timestamp and a
// random suffix, e.g. "20260806-120000-a1b2c3d4".
func uniqueName() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UTC().Format("20060102-150405-000000")
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// extensionForContentType maps a MIME content type to a file extension.
func extensionForContentType(ct string) string {
	switch {
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		return ".jpg"
	case strings.Contains(ct, "png"):
		return ".png"
	case strings.Contains(ct, "gif"):
		return ".gif"
	case strings.Contains(ct, "webp"):
		return ".webp"
	case strings.Contains(ct, "bmp"):
		return ".bmp"
	default:
		return ".img"
	}
}

// listImages returns the processed images stored in the output directory,
// newest first.
// @Summary List Stored Images
// @Description Lists the processed images stored in the configured output directory, newest first.
// @Produce json
// @Success 200 {object} ImagesResponse
// @Router /api/v1/images [get]
func (a *api) listImages(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(a.outputDir)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"images": []any{}})
		return
	}

	images := []StoredImage{}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		images = append(images, StoredImage{
			Filename: e.Name(),
			Size:     info.Size(),
			Modified: info.ModTime(),
		})
	}

	sort.Slice(images, func(i, j int) bool { return images[i].Modified.After(images[j].Modified) })

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"images": images})
}

// getImage serves a stored processed image from the output directory. The
// filename is matched against a single path segment so directory traversal is
// not possible.
// @Summary Get Stored Image
// @Description Serves a processed image stored in the output directory.
// @Produce octet-stream
// @Param filename path string true "Image filename"
// @Success 200 {file} binary
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Router /api/v1/images/{filename} [get]
func (a *api) getImage(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("filename"))
	if name == "." || name == ".." || name == "" {
		a.jsonError(w, http.StatusBadRequest, "Invalid filename")
		return
	}
	path := filepath.Join(a.outputDir, name)
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		a.jsonError(w, http.StatusNotFound, "Image not found")
		return
	}

	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}

// jsonError writes a JSON-encoded error response with the given status code.
func (a *api) jsonError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}

// serveDocs serves the Swagger UI documentation page, the raw OpenAPI
// specification and the Swagger UI assets. The embedded "docs" directory is
// exposed under /docs so that the page, spec and assets resolve consistently
// with a copy of the "docs" folder hosted on a static web site.
func (a *api) serveDocs(w http.ResponseWriter, r *http.Request) {
	if a.docsFileServer == nil {
		http.NotFound(w, r)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/docs")
	switch path {
	case "/api", "/api/":
		path = "/api.html"
	case "/api/swagger.json", "/api/openapi.json":
		path = "/swagger.json"
	}

	req := r.Clone(r.Context())
	req.URL.Path = path
	a.docsFileServer.ServeHTTP(w, req)
}

// swaggerUIHTML returns the Swagger UI documentation page. It references the
// Swagger UI assets and the OpenAPI specification with relative URLs so that
// the same page works both inside the application (at /docs/api) and when the
// contents of the "docs" folder are copied to a static web site.
func swaggerUIHTML() []byte {
	return []byte(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>Magooify - API Documentation</title>
<link rel="stylesheet" href="swagger-ui/swagger-ui.css"/>
<style>
body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; }
.topbar { background: #1e293b; border-bottom: 1px solid #334155; padding: 0.75rem 1.5rem; display: flex; align-items: center; gap: 1rem; flex-wrap: wrap; }
.topbar h1 { font-size: 1.1rem; font-weight: 600; color: #38bdf8; margin: 0; }
.topbar a { color: #94a3b8; font-size: 0.9rem; text-decoration: none; margin-left: auto; }
.topbar a:hover { color: #38bdf8; }
</style>
</head>
<body>
<div class="topbar">
  <h1>Magooify - API Documentation</h1>
  <a href="../">Back to Home</a>
</div>
<div id="swagger-ui"></div>
<script src="swagger-ui/swagger-ui-bundle.js"></script>
<script src="swagger-ui/swagger-ui-standalone-preset.js"></script>
<script>
window.onload = function () {
  window.ui = SwaggerUIBundle({
    url: "swagger.json",
    dom_id: "#swagger-ui",
    deepLinking: true,
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset],
    layout: "StandaloneLayout",
    docExpansion: "list",
    displayRequestDuration: true,
    filter: true,
  });
};
</script>
</body>
</html>`)
}
