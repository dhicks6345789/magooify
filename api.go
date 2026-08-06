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
	defaultModel         = "openai/gpt-4o-mini"
	defaultOutputDir     = "processed"
	imageProcessPrompt   = "Describe the image in detail, including any text, people, objects and how they are arranged. Be specific and thorough."
	maxImageUploadBytes  = 25 << 20
)

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
type ProcessImageRequest struct {
	Image    string `json:"image" example:"data:image/jpeg;base64,..."`
	Filename string `json:"filename" example:"photo.jpg"`
}

// ProcessImageResponse describes the outcome of processing an image.
type ProcessImageResponse struct {
	Filename    string    `json:"filename" example:"img-20260806-123000-a1b2c3d4.jpg"`
	TextFile    string    `json:"text_file" example:"img-20260806-123000-a1b2c3d4.txt"`
	Description string    `json:"description" example:"A sunny garden with a wooden bench."`
	Model       string    `json:"model" example:"openai/gpt-4o-mini"`
	ProcessedAt time.Time `json:"processed_at"`
}

// StoredImage describes a processed image stored on the file system.
type StoredImage struct {
	Filename    string    `json:"filename" example:"img-20260806-123000-a1b2c3d4.jpg"`
	TextFile    string    `json:"text_file,omitempty" example:"img-20260806-123000-a1b2c3d4.txt"`
	Size        int64     `json:"size" example:"48291"`
	Modified    time.Time `json:"modified"`
	Description string    `json:"description,omitempty" example:"A sunny garden with a wooden bench."`
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

// processImage accepts an image (multipart upload or JSON base64/data URL),
// sends it to an OpenRouter vision model for processing, and stores the image
// and the resulting description on the file system.
// @Summary Process an Image with OpenRouter
// @Description Accepts an image captured by the camera or uploaded by the user, sends it to an OpenRouter vision model for processing, then stores the image and the resulting description in the configured output directory.
// @Accept mpfd
// @Produce json
// @Param image formData file true "Image to process"
// @Success 200 {object} ProcessImageResponse
// @Failure 400 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /api/v1/process [post]
func (a *api) processImage(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImageUploadBytes)

	img, filename, contentType, err := readImagePayload(r)
	if err != nil {
		a.jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Trust a declared image content type, otherwise sniff the bytes (browsers
	// send application/octet-stream for multipart file parts).
	if !strings.HasPrefix(contentType, "image/") {
		contentType = http.DetectContentType(img)
	}
	if !strings.HasPrefix(contentType, "image/") {
		a.jsonError(w, http.StatusBadRequest, "Uploaded file is not a recognised image")
		return
	}

	description, err := a.processWithOpenRouter(img, contentType, filename)
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errOpenRouterNotConfigured) {
			code = http.StatusServiceUnavailable
		}
		a.jsonError(w, code, err.Error())
		return
	}

	imageFile, textFile, err := a.storeResult(img, contentType, description)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, "Failed to store processed image: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ProcessImageResponse{
		Filename:    imageFile,
		TextFile:    textFile,
		Description: description,
		Model:       a.model,
		ProcessedAt: time.Now().UTC(),
	})
}

// readImagePayload extracts the image bytes, original filename and content
// type from either a JSON payload (base64 or data URL in the "image" field) or
// a multipart form upload (the "image" file part).
func readImagePayload(r *http.Request) ([]byte, string, string, error) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var req ProcessImageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Image == "" {
			return nil, "", "", errors.New("Missing image payload")
		}
		data := req.Image
		if idx := strings.Index(data, ","); idx >= 0 && strings.HasPrefix(data, "data:") {
			data = data[idx+1:]
		}
		img, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
		if err != nil {
			return nil, "", "", errors.New("Invalid base64 image payload")
		}
		return img, req.Filename, "", nil
	}

	if err := r.ParseMultipartForm(maxImageUploadBytes); err != nil {
		return nil, "", "", errors.New("Invalid multipart form data")
	}
	f, hdr, err := r.FormFile("image")
	if err != nil {
		return nil, "", "", errors.New("Missing 'image' file field")
	}
	defer f.Close()
	img, err := io.ReadAll(f)
	if err != nil {
		return nil, "", "", errors.New("Failed to read uploaded image")
	}
	filename := hdr.Filename
	if filename == "" {
		filename = "upload.jpg"
	}
	return img, filename, hdr.Header.Get("Content-Type"), nil
}

// processWithOpenRouter sends the image to the configured OpenRouter vision
// model as a chat completion and returns the model's textual description.
func (a *api) processWithOpenRouter(img []byte, contentType, filename string) (string, error) {
	if a.openRouterKey == "" {
		return "", errOpenRouterNotConfigured
	}

	dataURL := "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(img)
	payload := map[string]any{
		"model": a.model,
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": imageProcessPrompt},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("Failed to build OpenRouter request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, a.openRouterURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("Failed to create OpenRouter request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.openRouterKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("OpenRouter request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("Failed to read OpenRouter response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OpenRouter returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var chat struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &chat); err != nil {
		return "", fmt.Errorf("Failed to parse OpenRouter response: %w", err)
	}
	if len(chat.Choices) == 0 {
		return "", errors.New("OpenRouter returned no choices")
	}

	desc := strings.TrimSpace(chat.Choices[0].Message.Content)
	if desc == "" {
		desc = "No description returned"
	}
	return desc, nil
}

// storeResult writes the processed image and its textual description to the
// configured output directory under a unique timestamped base name.
func (a *api) storeResult(img []byte, contentType, description string) (string, string, error) {
	if err := os.MkdirAll(a.outputDir, 0o755); err != nil {
		return "", "", err
	}

	base := "img-" + uniqueName()
	imageFile := base + extensionForContentType(contentType)
	textFile := base + ".txt"

	if err := os.WriteFile(filepath.Join(a.outputDir, imageFile), img, 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(filepath.Join(a.outputDir, textFile), []byte(description), 0o644); err != nil {
		return "", "", err
	}
	return imageFile, textFile, nil
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
// newest first, each with the stored description text if available.
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
		img := StoredImage{
			Filename: e.Name(),
			Size:     info.Size(),
			Modified: info.ModTime(),
		}
		base := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if desc, err := os.ReadFile(filepath.Join(a.outputDir, base+".txt")); err == nil {
			if s := strings.TrimSpace(string(desc)); s != "" {
				img.TextFile = base + ".txt"
				if len(s) > 2000 {
					s = s[:2000] + "…"
				}
				img.Description = s
			}
		}
		images = append(images, img)
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
