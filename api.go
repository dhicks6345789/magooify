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
	"math"
	"mime"
	"net/http"
"net/url"
	"os"
	"path/filepath"
	"regexp"
	"image"
	"image/png"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	openRouterChatURL        = "https://openrouter.ai/api/v1/chat/completions"
	openRouterImagesURL      = "https://openrouter.ai/api/v1/images"
	openRouterImageModelsURL = "https://openrouter.ai/api/v1/images/models"
	openRouterCreditsURL     = "https://openrouter.ai/api/v1/credits"
	modelsCacheTTL           = 30 * time.Minute
	defaultModel             = "google/gemini-3.1-flash-lite-image"
	defaultOutputDir         = "processed"
	fallbackPrompt           = "Describe the image in detail, including any text, people, objects and how they are arranged. Be specific and thorough."
	maxImageUploadBytes      = 25 << 20
	maxOpenRouterBytes       = 25 << 20
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

// palettePromptRegexp matches any base-prompt sentence that specifies a colour
// palette size (e.g. "Limit the colour palette to 16 colours, rendering ..."),
// so applyPalettePrompt can drop it before appending the palette-specific line.
var palettePromptRegexp = regexp.MustCompile(`(?i)\s*Limit the colour palette to[^.]*\.\s*`)

// applyPalettePrompt returns the prompt that should be sent to the model when
// the request was made with a fixed-colour palette. The base prompt's
// "Limit the colour palette to N colours, rendering..." line is replaced with
// one that names the palette colours specifically, so the model draws using
// only those colours (the vectorise pass will quantise to them as well).
func applyPalettePrompt(basePrompt, paletteID string) string {
	pal, ok := findPalette(paletteID)
	if !ok {
		return basePrompt
	}
	trimmed := palettePromptRegexp.ReplaceAllString(basePrompt, " ")
	if strings.TrimSpace(trimmed) == "" {
		return palettePromptLine(pal)
	}
	trimmed = strings.TrimRight(trimmed, " ")
	if !strings.HasSuffix(trimmed, ".") {
		trimmed += "."
	}
	return trimmed + " " + palettePromptLine(pal)
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

// SetModelRequest is the payload accepted when switching the model used to
// process images.
type SetModelRequest struct {
	Model string `json:"model" example:"recraft/recraft-v4.1-vector"`
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
	// OpenRouterConfigured reports whether the OpenRouter API key has been
	// supplied via -openrouter-key. When false the application can only run
	// the local vectorisation pipeline and the UI hides AI-only controls.
	OpenRouterConfigured bool `json:"openrouter_configured" example:"true"`
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
	Output   string `json:"output" example:"img-20260806-123000-a1b2c3d4.png"`
	// Vectorise traces the processed image into a resolution-independent SVG
	// document with one colour layer per colour in the artwork, on a
	// transparent background. Models that already return SVG are passed
	// through unchanged.
	Vectorise bool `json:"vectorise" example:"false"`
	// Palette restricts the vectorised SVG to the named palette (one of the
	// IDs returned by /api/v1/palettes). Implies Vectorise. Each foreground
	// pixel is mapped to its nearest palette colour, so the SVG looks like it
	// was coloured in with the named pen pack.
	Palette string `json:"palette,omitempty" example:"crayola-12"`
}

// ProcessImageResponse describes the outcome of processing an image. Cost is
// the amount this request billed to the OpenRouter account in US dollars (0
// when OpenRouter did not report a cost), SessionCost is the total spent since
// the application started.
type ProcessImageResponse struct {
	Filename    string    `json:"filename" example:"img-20260806-123000-a1b2c3d4.svg"`
	Model       string    `json:"model" example:"google/gemini-3.1-flash-lite-image"`
	ProcessedAt time.Time `json:"processed_at"`
	Cost        float64   `json:"cost"`
	SessionCost float64   `json:"session_cost"`
	// Vectorised reports whether the stored result is an SVG document (either
	// traced from the processed bitmap or passed through because the model
	// returned SVG already).
	Vectorised bool `json:"vectorised" example:"false"`
}

// CreditsResponse describes the OpenRouter account balance and the spend since
// the application started. CreditsAvailable is false when no OpenRouter
// management key is configured, in which case the balance fields are zero.
type CreditsResponse struct {
	CreditsAvailable bool    `json:"credits_available"`
	TotalCredits     float64 `json:"total_credits,omitempty"`
	TotalUsage       float64 `json:"total_usage,omitempty"`
	RemainingCredits float64 `json:"remaining_credits,omitempty"`
	SessionCost      float64 `json:"session_cost"`
}

// ModelInfo describes an OpenRouter model that can process an image and return
// a processed image, together with the cost of a single processing run. The
// costs are exact when OpenRouter publishes a per-image price (unit "image")
// and otherwise estimated from the per-token rate using
// imageInputTokenEstimate and imageOutputTokenEstimate. InputImageCostKnown
// and OutputImageCostKnown report which of the two costs is an exact published
// per-image price rather than an estimate. EstimatedImageCost is the sum used
// for a single image-in/image-out run.
type ModelInfo struct {
	ID                   string  `json:"id" example:"google/gemini-3.1-flash-lite-image"`
	Name                 string  `json:"name" example:"Google: Nano Banana 2 Lite (Gemini 3.1 Flash Lite Image)"`
	InputImageCost       float64 `json:"input_image_cost" example:"0.0004"`
	InputImageCostKnown  bool    `json:"input_image_cost_known" example:"false"`
	OutputImageCost      float64 `json:"output_image_cost" example:"0.03"`
	OutputImageCostKnown bool    `json:"output_image_cost_known" example:"true"`
	PromptPerMillion     float64 `json:"prompt_per_million" example:"0.25"`
	CompletionPerMillion float64 `json:"completion_per_million" example:"1.5"`
	RequestCost          float64 `json:"request_cost" example:"0"`
	EstimatedImageCost   float64 `json:"estimated_image_cost" example:"0.0304"`
}

// ModelsResponse is the payload returned when listing image-capable models.
type ModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

// StoredImage describes a processed image stored on the file system.
type StoredImage struct {
Filename    string    `json:"filename" example:"img-20260806-123000-a1b2c3d4.png"`
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
	startTime         time.Time
	isServerMode      bool
	items             []Item
	nextID            int
	mu                sync.RWMutex
	docsFS            fs.FS
	docsFileServer    http.Handler
	openRouterKey     string
	managementKey     string
	openRouterURL     string
	imagesGenerateURL string
	imagesURL         string
	creditsURL        string
	model             string
	outputDir         string
	promptFile        string
	httpClient        *http.Client
	sessionCost       float64
	modelsCache       []ModelInfo
	modelsCachedAt    time.Time
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
		nextID:            2,
		docsFS:            docsFS,
		openRouterURL:     openRouterChatURL,
		imagesGenerateURL: openRouterImagesURL + "/generations",
		imagesURL:         openRouterImageModelsURL,
		creditsURL:        openRouterCreditsURL,
		model:             defaultModel,
		outputDir:         defaultOutputDir,
		httpClient:        &http.Client{Timeout: 120 * time.Second},
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
// @Description Returns system metrics, Go version, OS/Arch, uptime, the configured
// @Description model, and whether the OpenRouter API key has been configured.
// @Produce json
// @Success 200 {object} SystemInfoResponse
// @Router /api/v1/info [get]
func (a *api) info(w http.ResponseWriter, r *http.Request) {
	mode := "desktop"
	if a.isServerMode {
		mode = "server"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"mode":                 mode,
		"go_version":           runtime.Version(),
		"os":                   runtime.GOOS,
		"arch":                 runtime.GOARCH,
		"uptime":               time.Since(a.startTime).Truncate(time.Second).String(),
		"model":                a.model,
		"openrouter_configured": a.openRouterKey != "",
	})
}

// credits returns the OpenRouter account balance (remaining credits) and the
// total spend accumulated during this application session. The balance is only
// available when an OpenRouter management key is configured; otherwise the
// response reports credits_available: false and the session cost is returned
// on its own.
// @Summary Get Credits
// @Description Returns the remaining OpenRouter credits for the account (when a management key is configured) and the session's accumulated spend in US dollars.
// @Produce json
// @Success 200 {object} CreditsResponse
// @Router /api/v1/credits [get]
func (a *api) credits(w http.ResponseWriter, r *http.Request) {
	remaining, total, used, ok := a.fetchCredits()

	a.mu.RLock()
	session := a.sessionCost
	a.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(CreditsResponse{
		CreditsAvailable: ok,
		TotalCredits:     total,
		TotalUsage:       used,
		RemainingCredits: remaining,
		SessionCost:      session,
	})
}

// fetchCredits asks the OpenRouter API for the account's total credits and
// usage. It returns the remaining balance, the total credits, the usage and
// whether the query succeeded. When no management key is configured, or the
// OpenRouter request fails for any reason, ok is false so callers can report
// the balance as unavailable rather than exposing an error to the UI.
func (a *api) fetchCredits() (remaining, total, used float64, ok bool) {
	if a.managementKey == "" {
		return 0, 0, 0, false
	}

	req, err := http.NewRequest(http.MethodGet, a.creditsURL, nil)
	if err != nil {
		return 0, 0, 0, false
	}
	req.Header.Set("Authorization", "Bearer "+a.managementKey)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return 0, 0, 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, 0, false
	}

	var body struct {
		Data struct {
			TotalCredits float64 `json:"total_credits"`
			TotalUsage   float64 `json:"total_usage"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, 0, 0, false
	}

	used = body.Data.TotalUsage
	total = body.Data.TotalCredits
	remaining = total - used
	return remaining, total, used, true
}

// imageInputTokenEstimate and imageOutputTokenEstimate are the token counts
// used to estimate a per-image processing cost for models that bill by the
// token instead of publishing a fixed per-image price. A typical 1024x1024
// image is commonly estimated at around 1,600 input tokens, and a generated
// image at roughly 1,290 output tokens. These are estimates: providers bill
// images differently, so exact per-image prices are used whenever OpenRouter
// publishes them.
const (
	imageInputTokenEstimate  = 1600.0
	imageOutputTokenEstimate = 1290.0
)

// openRouterImageModel mirrors the subset of OpenRouter's image models listing
// consumed by fetchModels. The listing covers every model that can generate an
// image, including image-to-image models that the general /models endpoint
// omits (such as the Recraft family).
type openRouterImageModel struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Architecture struct {
		Input  []string `json:"input_modalities"`
		Output []string `json:"output_modalities"`
	} `json:"architecture"`
	EndpointsURL string `json:"endpoints"`
}

// openRouterImagesResponse is the envelope returned by the image models
// listing.
type openRouterImagesResponse struct {
	Data []openRouterImageModel `json:"data"`
}

// openRouterImageEndpoints carries the per-provider pricing for one model.
type openRouterImageEndpoints struct {
	Endpoints []openRouterImageEndpoint `json:"endpoints"`
}

// openRouterImageEndpoint describes one provider endpoint serving a model.
type openRouterImageEndpoint struct {
	ProviderName string                 `json:"provider_name"`
	Pricing      []openRouterImagePrice `json:"pricing"`
}

// openRouterImagePrice is one billable pricing line. Billable is either
// "output_image" or "input_image"/"input_reference"; Unit is "image" (an exact
// per-image price), "megapixel", or "token" (a per-token rate). Costs are in
// US dollars.
type openRouterImagePrice struct {
	Billable string  `json:"billable"`
	Unit     string  `json:"unit"`
	CostUSD  float64 `json:"cost_usd"`
}

// models returns the OpenRouter models that can process an image and return a
// processed image, together with the cost of processing a single image with
// each one, cheapest first.
// @Summary List Image Processing Models
// @Description Lists OpenRouter models that can process an image and return a processed image, with the cost of processing a single image, so cheaper alternatives to the configured model are easy to spot. Exact per-image prices are used when OpenRouter publishes them; otherwise costs are estimated from the per-token rates.
// @Produce json
// @Success 200 {object} ModelsResponse
// @Failure 502 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Router /api/v1/models [get]
func (a *api) models(w http.ResponseWriter, r *http.Request) {
	models, err := a.fetchModels()
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errOpenRouterNotConfigured) {
			code = http.StatusServiceUnavailable
		}
		a.jsonError(w, code, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ModelsResponse{Models: models})
}

// setModel switches the model used to process images. The model must be one of
// the listed image processing models, so the request is validated against the
// current catalogue before the change is applied.
// @Summary Set Processing Model
// @Description Switches the model used to process images to one of the listed image processing models.
// @Accept json
// @Produce json
// @Param body body SetModelRequest true "Model to use"
// @Success 200 {object} map[string]string
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Router /api/v1/model [put]
func (a *api) setModel(w http.ResponseWriter, r *http.Request) {
	var req SetModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Model) == "" {
		a.jsonError(w, http.StatusBadRequest, "Missing model ID")
		return
	}
	model := strings.TrimSpace(req.Model)

	known, err := a.fetchModels()
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errOpenRouterNotConfigured) {
			code = http.StatusServiceUnavailable
		}
		a.jsonError(w, code, "Cannot validate the model: "+err.Error())
		return
	}
	for _, m := range known {
		if m.ID == model {
			a.mu.Lock()
			a.model = model
			a.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"model": model})
			return
		}
	}
	a.jsonError(w, http.StatusNotFound, "Unknown model: "+model)
}

// fetchModels returns the list of OpenRouter models that accept image input
// and return a processed image, cheapest first. The listing requires the
// OpenRouter API key. Image models are discovered through OpenRouter's
// dedicated images endpoint, which covers image-to-image models (such as the
// Recraft family) that the general models endpoint omits; per-image prices
// come from each model's endpoints record. The result is cached briefly so the
// UI can reload without hammering OpenRouter.
func (a *api) fetchModels() ([]ModelInfo, error) {
	a.mu.RLock()
	if a.modelsCache != nil && time.Since(a.modelsCachedAt) < modelsCacheTTL {
		models := a.modelsCache
		a.mu.RUnlock()
		return models, nil
	}
	a.mu.RUnlock()

	if a.openRouterKey == "" {
		return nil, errOpenRouterNotConfigured
	}

	models, err := a.fetchImageModels()
	if err != nil {
		return nil, err
	}

	sort.Slice(models, func(i, j int) bool {
		if models[i].EstimatedImageCost != models[j].EstimatedImageCost {
			return models[i].EstimatedImageCost < models[j].EstimatedImageCost
		}
		return models[i].ID < models[j].ID
	})

	a.mu.Lock()
	a.modelsCache = models
	a.modelsCachedAt = time.Now()
	a.mu.Unlock()

	return models, nil
}

// fetchImageModels loads the image models listing and, for each model, its
// per-provider pricing records. Pricing is fetched concurrently (bounded so
// the whole refresh completes quickly); a model whose pricing record fails to
// load is still listed, with its costs left at zero.
func (a *api) fetchImageModels() ([]ModelInfo, error) {
	list, err := a.fetchImageModelList()
	if err != nil {
		return nil, err
	}

	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	var mu sync.Mutex
	models := make([]ModelInfo, 0, len(list))
	for _, m := range list {
		if !containsString(m.Architecture.Input, "image") ||
			!containsString(m.Architecture.Output, "image") {
			continue
		}
		wg.Add(1)
		go func(m openRouterImageModel) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var pricing []openRouterImageEndpoint
			if m.EndpointsURL != "" {
				if eps, err := a.fetchImageModelEndpoints(m.EndpointsURL); err == nil {
					pricing = eps
				}
			}
			mu.Lock()
			models = append(models, buildImageModelInfo(m, pricing))
			mu.Unlock()
		}(m)
	}
	wg.Wait()
	return models, nil
}

// fetchImageModelList retrieves the full image models listing.
func (a *api) fetchImageModelList() ([]openRouterImageModel, error) {
	req, err := http.NewRequest(http.MethodGet, a.imagesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("Failed to build OpenRouter image models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.openRouterKey)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch image models from OpenRouter: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenRouter image models request returned status %d", resp.StatusCode)
	}
	var body openRouterImagesResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOpenRouterBytes)).Decode(&body); err != nil {
		return nil, fmt.Errorf("Failed to parse OpenRouter image models response: %w", err)
	}
	return body.Data, nil
}

// fetchImageModelEndpoints retrieves the per-provider pricing for one image
// model from the URL published in the model listing.
func (a *api) fetchImageModelEndpoints(endpointsURL string) ([]openRouterImageEndpoint, error) {
	listURL, err := url.Parse(a.imagesURL)
	if err != nil {
		return nil, err
	}
	rel, err := url.Parse(endpointsURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, listURL.ResolveReference(rel).String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.openRouterKey)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch model pricing from OpenRouter: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenRouter model pricing request returned status %d", resp.StatusCode)
	}
	var body openRouterImageEndpoints
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxOpenRouterBytes)).Decode(&body); err != nil {
		return nil, fmt.Errorf("Failed to parse OpenRouter model pricing response: %w", err)
	}
	return body.Endpoints, nil
}

// buildImageModelInfo turns an image model and its pricing records into the
// ModelInfo view used by the UI. Costs are exact when OpenRouter publishes a
// per-image price (unit "image") and otherwise estimated from the per-token
// rate using imageInputTokenEstimate / imageOutputTokenEstimate. The cheapest
// provider price is used for each line.
func buildImageModelInfo(m openRouterImageModel, pricing []openRouterImageEndpoint) ModelInfo {
	mi := ModelInfo{ID: m.ID, Name: m.Name}

	inputExact, inputToken := bestImagePrice(pricing, "input_image")
	refExact, refToken := bestImagePrice(pricing, "input_reference")
	if refExact > 0 && (inputExact == 0 || refExact < inputExact) {
		inputExact = refExact
	}
	if refToken > 0 && (inputToken == 0 || refToken < inputToken) {
		inputToken = refToken
	}
	outputExact, outputToken := bestImagePrice(pricing, "output_image")

	mi.InputImageCost, mi.InputImageCostKnown = perImageCost(inputExact, inputToken, imageInputTokenEstimate)
	mi.OutputImageCost, mi.OutputImageCostKnown = perImageCost(outputExact, outputToken, imageOutputTokenEstimate)
	mi.PromptPerMillion = inputToken * 1e6
	mi.CompletionPerMillion = outputToken * 1e6
	mi.EstimatedImageCost = roundCost(mi.InputImageCost + mi.OutputImageCost)

	return mi
}

// bestImagePrice finds the cheapest published price for a billable line across
// a model's provider endpoints. exact is the per-image price (unit "image" or
// "megapixel") when OpenRouter publishes one; perToken is the lowest per-token
// rate. Either may be zero when no such price is published.
func bestImagePrice(pricing []openRouterImageEndpoint, billable string) (exact, perToken float64) {
	for _, ep := range pricing {
		for _, p := range ep.Pricing {
			if p.Billable != billable || p.CostUSD <= 0 {
				continue
			}
			switch p.Unit {
			case "image", "megapixel":
				if exact == 0 || p.CostUSD < exact {
					exact = p.CostUSD
				}
			case "token":
				if perToken == 0 || p.CostUSD < perToken {
					perToken = p.CostUSD
				}
			}
		}
	}
	return exact, perToken
}

// perImageCost converts a billable line into the cost of one image. An exact
// per-image price is used when published; otherwise the cost is estimated from
// the per-token rate and the expected token count for one image. known reports
// whether the returned cost is an exact published price.
func perImageCost(exact, perToken, tokenEstimate float64) (cost float64, known bool) {
	if exact > 0 {
		return exact, true
	}
	return perToken * tokenEstimate, false
}

// roundCost rounds a computed cost to six decimal places to avoid floating
// point noise in JSON output.
func roundCost(c float64) float64 {
	return math.Round(c*1e6) / 1e6
}

// containsString reports whether list contains want.
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
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

// palettes returns the catalogue of fixed colour palettes the vectoriser can
// confine its output to (felt-tip pen packs from Berol, Crayola, Sharpie and
// Staedtler). The frontend fetches these on page load to populate the "Use a
// limited colour palette" picker.
// @Summary List Colour Palettes
// @Description Returns the fixed colour palettes (Berol, Crayola, Sharpie, Staedtler felt-tip packs) available to the vectoriser.
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/palettes [get]
func (a *api) palettes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"palettes": allPalettes})
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
	vectorise   bool
	palette     string
}

// processImage accepts an image (multipart upload or JSON base64/data URL),
// sends it to an OpenRouter vision model for processing, and stores the image
// and the resulting description on the file system. When no OpenRouter key
// has been configured the AI step is skipped and only the local pipeline
// (vectorising the input image into an SVG) is available; in that mode the
// request must carry vectorise=true or a palette id so the work is meaningful.
// @Summary Process an Image
// @Description Accepts an image captured by the camera or uploaded by the user. When an OpenRouter
// @Description API key is configured the image is sent to the configured vision model for
// @Description processing, then stored in the configured output directory. When no key is
// @Description configured only the local vectorisation pipeline is available: the request must
// @Description carry vectorise=true (or a palette id) so the input image is traced into SVG.
// @Accept mpfd
// @Produce json
// @Param image formData file true "Image to process"
// @Param prompt formData string false "Prompt to send with the image; defaults to the configured prompt. Ignored when no OpenRouter key is configured."
// @Param output formData string false "Optional filename to write the processed image to, replacing any existing file with that name"
// @Param vectorise formData bool false "Trace the processed image into an SVG document (true/1/on/yes)"
// @Param palette formData string false "Restrict the vectorised SVG to this palette ID (returned by /api/v1/palettes); implies vectorise"
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

	if pl.palette != "" {
		if _, ok := findPalette(pl.palette); !ok {
			a.jsonError(w, http.StatusBadRequest, "Unknown palette: "+pl.palette)
			return
		}
	}

	var processed []byte
	var cost float64
	var model string

	if a.openRouterKey != "" {
		prompt := pl.prompt
		if strings.TrimSpace(prompt) == "" {
			prompt = a.processPrompt()
		}
		if pl.palette != "" {
			prompt = applyPalettePrompt(prompt, pl.palette)
		}

		var perr error
		processed, cost, perr = a.processWithOpenRouter(pl.img, pl.contentType, prompt)
		if perr != nil {
			code := http.StatusBadGateway
			if errors.Is(perr, errOpenRouterNotConfigured) {
				code = http.StatusServiceUnavailable
			}
			a.jsonError(w, code, perr.Error())
			return
		}
		model = a.model
	} else {
		// No OpenRouter key: only the local vectorisation pipeline is available.
		// Require vectorise or a palette so the request actually does something.
		if !pl.vectorise && pl.palette == "" {
			a.jsonError(w, http.StatusServiceUnavailable,
				"No OpenRouter API key configured; only local vectorisation is available. Restart with -openrouter-key=<key> or enable vectorisation.")
			return
		}
		// Skip the AI step entirely: the input image is the source the
		// vectoriser traces.
		processed = pl.img
		pl.vectorise = true
	}

	if pl.vectorise {
		if vectorised, verr := vectoriseBitmap(processed, pl.palette); verr == nil {
			processed = vectorised
		} else {
			a.jsonError(w, http.StatusBadRequest, "Cannot vectorise the processed image: "+verr.Error())
			return
		}
	} else {
		png, perr := forcePNG(processed)
		if perr != nil {
			a.jsonError(w, http.StatusBadRequest, "Cannot re-encode the processed image to PNG: "+perr.Error())
			return
		}
		processed = png
	}

	a.mu.Lock()
	a.sessionCost += cost
	sessionCost := a.sessionCost
	a.mu.Unlock()

	imageFile, err := a.storeResult(processed, pl.output)
	if err != nil {
		a.jsonError(w, http.StatusInternalServerError, "Failed to store processed image: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ProcessImageResponse{
		Filename:    imageFile,
		Model:       model,
		ProcessedAt: time.Now().UTC(),
		Cost:        cost,
		SessionCost: sessionCost,
		Vectorised:  isSVGDocument(processed),
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
			img:       img,
			filename:  req.Filename,
			prompt:    req.Prompt,
			output:    req.Output,
			vectorise: req.Vectorise,
			palette:   req.Palette,
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
		vectorise:   parseBoolValue(r.FormValue("vectorise")),
		palette:     r.FormValue("palette"),
	}, nil
}

// parseBoolValue interprets a form value as a boolean, accepting "true", "1",
// "on" and "yes" (case-insensitive) as true and everything else as false.
func parseBoolValue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "on", "yes":
		return true
	}
	return false
}

// processWithOpenRouter sends the image to the configured OpenRouter image
// model and returns the processed image returned by the model, together with
// the cost of the request in US dollars (0 when OpenRouter did not report
// one). When prompt is empty the configured prompt is used.
func (a *api) processWithOpenRouter(img []byte, contentType, prompt string) ([]byte, float64, error) {
	if a.openRouterKey == "" {
		return nil, 0, errOpenRouterNotConfigured
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
		return nil, 0, fmt.Errorf("Failed to build OpenRouter request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, a.openRouterURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("Failed to create OpenRouter request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.openRouterKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("OpenRouter request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenRouterBytes+1))
	if err != nil {
		return nil, 0, fmt.Errorf("Failed to read OpenRouter response: %w", err)
	}
	if len(respBody) > maxOpenRouterBytes {
		return nil, 0, errors.New("OpenRouter response exceeded the 25 MB size limit")
	}
	if resp.StatusCode != http.StatusOK {
		// Models such as the OpenAI gpt-image family and qwen-image only run
		// through OpenRouter's dedicated images endpoint. When OpenRouter
		// rejects the chat/completions call for the selected model, retry the
		// processing through the images endpoint instead.
		if resp.StatusCode == http.StatusNotFound && isImageModelChatRejection(respBody) {
			return a.processWithImagesAPI(img, contentType, prompt)
		}
		return nil, 0, fmt.Errorf("OpenRouter returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if len(respBody) == 0 {
		return nil, 0, errors.New("OpenRouter returned an empty response (HTTP 200); the model may have failed to produce an image. Please try again")
	}

	img, err = extractImageFromResponse(respBody)
	if err != nil {
		return nil, 0, err
	}
	return img, extractCost(respBody, resp.Header), nil
}

// isImageModelChatRejection reports whether OpenRouter refused the
// chat/completions call because the selected model only runs through the
// dedicated images endpoint. The refusal appears either as an explicit "cannot
// be used with the chat/completions endpoint" message or, when the request
// asks for image output modalities, as a "no endpoints found" 404.
func isImageModelChatRejection(respBody []byte) bool {
	lower := bytes.ToLower(respBody)
	return bytes.Contains(lower, []byte("chat/completions")) ||
		bytes.Contains(lower, []byte("output modalities"))
}

// processWithImagesAPI sends the image to the configured OpenRouter image model
// through the dedicated images endpoint, used by models (such as the gpt-image
// family) that refuse the chat/completions endpoint, and returns the processed
// image together with the cost of the request in US dollars (0 when OpenRouter
// did not report one).
func (a *api) processWithImagesAPI(img []byte, contentType, prompt string) ([]byte, float64, error) {
	dataURL := "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(img)
	payload := map[string]any{
		"model":  a.model,
		"prompt": prompt,
		"image_reference": []any{
			map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": dataURL},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("Failed to build OpenRouter image request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, a.imagesGenerateURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("Failed to create OpenRouter image request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.openRouterKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("OpenRouter image request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenRouterBytes+1))
	if err != nil {
		return nil, 0, fmt.Errorf("Failed to read OpenRouter image response: %w", err)
	}
	if len(respBody) > maxOpenRouterBytes {
		return nil, 0, errors.New("OpenRouter image response exceeded the 25 MB size limit")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("OpenRouter image generation returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if len(respBody) == 0 {
		return nil, 0, errors.New("OpenRouter returned an empty image generation response; the model may have failed to produce an image. Please try again")
	}

	img, err = a.extractImagesAPIOutput(respBody)
	if err != nil {
		return nil, 0, err
	}
	return img, extractCost(respBody, resp.Header), nil
}

// extractImagesAPIOutput pulls the generated image out of an OpenRouter images
// endpoint response. Output is an array of image objects that carry either a
// base64-encoded payload (b64_json) or a URL (a data URL or a remote URL).
func (a *api) extractImagesAPIOutput(respBody []byte) ([]byte, error) {
	var out struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("OpenRouter returned an image response that could not be parsed (%v); the model may have failed or returned an unexpected format. Please try again", err)
	}
	if len(out.Data) == 0 {
		return nil, errors.New("OpenRouter image generation returned no images")
	}
	for _, d := range out.Data {
		if d.B64JSON != "" {
			if img, err := base64.StdEncoding.DecodeString(strings.TrimSpace(d.B64JSON)); err == nil {
				return img, nil
			}
		}
		if d.URL != "" {
			if img, ok := decodeDataURL(d.URL); ok {
				return img, nil
			}
			return a.downloadImage(d.URL)
		}
	}
	return nil, errors.New("OpenRouter image generation returned no usable image")
}

// downloadImage fetches a generated image served from a remote URL returned by
// the OpenRouter images endpoint.
func (a *api) downloadImage(imageURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned status %d", resp.StatusCode)
	}
	img, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenRouterBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read image: %w", err)
	}
	if len(img) > maxOpenRouterBytes {
		return nil, errors.New("generated image exceeded the 25 MB size limit")
	}
	return img, nil
}

// extractCost returns the US-dollar cost of an OpenRouter request, taken from
// the usage.cost field of the response body and falling back to the
// X-Request-Cost response header when the body does not carry it.
func extractCost(respBody []byte, hdr http.Header) float64 {
	var chat struct {
		Usage struct {
			Cost *float64 `json:"cost"`
		} `json:"usage"`
	}
	if json.Unmarshal(respBody, &chat) == nil && chat.Usage.Cost != nil {
		return *chat.Usage.Cost
	}
	if v := hdr.Get("X-Request-Cost"); v != "" {
		if c, err := strconv.ParseFloat(v, 64); err == nil {
			return c
		}
	}
	return 0
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
// one is supplied (overwriting any existing file with that name). The stored
// extension always matches the content: when a supplied output name carries an
// extension that does not fit the bytes (for example a .jpg name for SVG
// output after toggling vectorisation), the same base name is used with the
// correct extension and the stale file left over from the previous extension
// is removed first.
func (a *api) storeResult(img []byte, output string) (string, error) {
	if err := os.MkdirAll(a.outputDir, 0o755); err != nil {
		return "", err
	}

	imageFile := output
	if imageFile == "" {
		imageFile = "img-" + uniqueName() + extensionForContent(img)
	} else {
		// The filename is matched against a single path segment so directory
		// traversal is not possible.
		if filepath.Base(imageFile) != imageFile || imageFile == "." || imageFile == ".." {
			return "", errors.New("invalid output filename")
		}
		if wantExt := extensionForContent(img); wantExt != "" {
			oldExt := filepath.Ext(imageFile)
			if strings.ToLower(oldExt) != wantExt {
				stale := filepath.Join(a.outputDir, imageFile)
				if _, err := os.Stat(stale); err == nil {
					if err := os.Remove(stale); err != nil {
						return "", errors.New("failed to remove the previous version: " + err.Error())
					}
				}
				imageFile = strings.TrimSuffix(imageFile, oldExt) + wantExt
			}
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

// extensionForContent picks the file extension for a generated image, detecting
// vector (SVG) output by its markup before falling back to content sniffing.
func extensionForContent(img []byte) string {
	if isSVGDocument(img) {
		return ".svg"
	}
	return extensionForContentType(http.DetectContentType(img))
}

// forcePNG re-encodes a bitmap image as PNG so the saved file (and the
// download the user gets) is always PNG rather than JPEG, even when the
// underlying model returned a JPEG/WEBP/GIF image. SVG documents are
// returned unchanged because they aren’t a raster format that PNG could
// represent losslessly.
func forcePNG(img []byte) ([]byte, error) {
	if isSVGDocument(img) {
		return img, nil
	}
	decoded, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return nil, fmt.Errorf("cannot re-encode processed image to PNG: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, decoded); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
	case strings.Contains(ct, "svg"):
		return ".svg"
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
