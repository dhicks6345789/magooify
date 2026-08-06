package main

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

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
}

// @title Go Self-Contained App API
// @description API documentation for the self-contained Go application framework.
// @version 1.0.0
func newAPI(isServerMode bool, docsFS fs.FS) *api {
	a := &api{
		startTime:    time.Now(),
		isServerMode: isServerMode,
		items: []Item{
			{
				ID:        1,
				Name:      "Welcome to Go Self-Contained App",
				CreatedBy: "system",
				CreatedAt: time.Now(),
			},
		},
		nextID: 2,
		docsFS: docsFS,
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
<title>Go App - API Documentation</title>
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
  <h1>Go App - API Documentation</h1>
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
