package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// miniPNG is a valid 1x1 transparent PNG used as test image data.
var miniPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

func TestRegisteredMimeTypes(t *testing.T) {
	registerMimeTypes()

	want := map[string]string{
		".html": "text/html; charset=utf-8",
		".css":  "text/css; charset=utf-8",
		".js":   "text/javascript; charset=utf-8",
		".json": "application/json",
	}
	for ext, wantType := range want {
		if got := mime.TypeByExtension(ext); got != wantType {
			t.Errorf("TypeByExtension(%q) = %q, want %q", ext, got, wantType)
		}
	}
}

func TestHealthEndpoint(t *testing.T) {
	a := newAPI(false, docsFS)
	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	rr := httptest.NewRecorder()

	a.health(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got %s", resp["status"])
	}
}

func TestUserEndpointDesktop(t *testing.T) {
	t.Setenv("USER", "tester")
	a := newAPI(false, docsFS)
	req := httptest.NewRequest("GET", "/api/v1/user", nil)
	rr := httptest.NewRecorder()

	a.user(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	var resp UserInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if resp.Mode != "desktop" {
		t.Errorf("expected mode 'desktop', got %s", resp.Mode)
	}
	if resp.Username != "tester" {
		t.Errorf("expected username 'tester', got %s", resp.Username)
	}
}

func TestUserEndpointServerProxyHeader(t *testing.T) {
	a := newAPI(true, docsFS)
	req := httptest.NewRequest("GET", "/api/v1/user", nil)
	req.Header.Set("Remote-User", "alice_proxy")
	rr := httptest.NewRecorder()

	a.user(rr, req)

	var resp UserInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if resp.Username != "alice_proxy" {
		t.Errorf("expected username 'alice_proxy', got %s", resp.Username)
	}
	if resp.Mode != "server" {
		t.Errorf("expected mode 'server', got %s", resp.Mode)
	}
}

func TestUserEndpointServerNoHeader(t *testing.T) {
	a := newAPI(true, docsFS)
	req := httptest.NewRequest("GET", "/api/v1/user", nil)
	rr := httptest.NewRecorder()

	a.user(rr, req)

	var resp UserInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if resp.Username != "anonymous" {
		t.Errorf("expected username 'anonymous', got %s", resp.Username)
	}
}

func TestItemsCRUD(t *testing.T) {
	t.Setenv("USER", "tester")
	a := newAPI(false, docsFS)

	// Test GET items
	reqGet := httptest.NewRequest("GET", "/api/v1/items", nil)
	rrGet := httptest.NewRecorder()
	a.listItems(rrGet, reqGet)

	if rrGet.Code != http.StatusOK {
		t.Errorf("GET /items returned %d", rrGet.Code)
	}

	// Test POST item
	payload := []byte(`{"name":"New Test Item"}`)
	reqPost := httptest.NewRequest("POST", "/api/v1/items", bytes.NewBuffer(payload))
	rrPost := httptest.NewRecorder()
	a.createItem(rrPost, reqPost)

	if rrPost.Code != http.StatusCreated {
		t.Errorf("POST /items returned %d", rrPost.Code)
	}

	var created Item
	if err := json.Unmarshal(rrPost.Body.Bytes(), &created); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}
	if created.Name != "New Test Item" {
		t.Errorf("expected item name 'New Test Item', got %s", created.Name)
	}
	if created.CreatedBy != "tester" {
		t.Errorf("expected created_by 'tester', got %s", created.CreatedBy)
	}

	// Test POST item with empty name
	reqPostBad := httptest.NewRequest("POST", "/api/v1/items", bytes.NewBuffer([]byte(`{"name":""}`)))
	rrPostBad := httptest.NewRecorder()
	a.createItem(rrPostBad, reqPostBad)
	if rrPostBad.Code != http.StatusBadRequest {
		t.Errorf("POST /items with empty name returned %d", rrPostBad.Code)
	}
}

func TestServeDocs(t *testing.T) {
	a := newAPI(false, docsFS)
	req := httptest.NewRequest("GET", "/docs/api", nil)
	rr := httptest.NewRecorder()

	a.serveDocs(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("docs handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("expected text/html content type, got %q", ct)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("swagger-ui")) {
		t.Errorf("expected docs page to load Swagger UI")
	}
}

func TestServeOpenAPISpec(t *testing.T) {
	a := newAPI(false, docsFS)
	req := httptest.NewRequest("GET", "/docs/swagger.json", nil)
	rr := httptest.NewRecorder()

	a.serveDocs(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("spec handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json content type, got %q", ct)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"swagger": "2.0"`)) {
		t.Errorf("expected OpenAPI specification to be served")
	}
}

func TestServeSwaggerUIAsset(t *testing.T) {
	a := newAPI(false, docsFS)
	req := httptest.NewRequest("GET", "/docs/swagger-ui/swagger-ui.css", nil)
	rr := httptest.NewRecorder()

	a.serveDocs(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("asset handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/css; charset=utf-8" {
		t.Errorf("expected text/css content type, got %q", ct)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("swagger-ui")) {
		t.Errorf("expected Swagger UI stylesheet to be served")
	}
}

func TestNormalizeBasePath(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"   ":        "",
		"/":          "",
		"magooify":   "/magooify",
		"/magooify":  "/magooify",
		"/magooify/": "/magooify",
	}
	for in, want := range cases {
		if got := normalizeBasePath(in); got != want {
			t.Errorf("normalizeBasePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServeAllRoutes(t *testing.T) {
	registerMimeTypes()
	a := newAPI(true, docsFS)
	// A stripping proxy (Pangolin) removes the /magooify prefix before
	// forwarding, so the app sees the site-relative paths below.
	handler := buildHandler(a, nil)

	cases := []struct {
		path        string
		wantCode    int
		wantContent string
		wantBody    string
	}{
		{"/", http.StatusOK, "text/html", "Magooify"},
		{"/style.css", http.StatusOK, "text/css", ".brand-icon"},
		{"/vendor/bootstrap/bootstrap.min.css", http.StatusOK, "text/css", "bootstrap"},
		{"/app.js", http.StatusOK, "text/javascript", "DOMContentLoaded"},
		{"/api/v1/user", http.StatusOK, "application/json", `"auth_type"`},
		{"/api/v1/health", http.StatusOK, "application/json", `"ok"`},
		{"/docs/api", http.StatusOK, "text/html", "swagger-ui"},
		{"/docs/swagger.json", http.StatusOK, "application/json", `"swagger"`},
		{"/docs/swagger-ui/swagger-ui.css", http.StatusOK, "text/css", "swagger-ui"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != tc.wantCode {
			t.Errorf("%s: status = %d, want %d", tc.path, rr.Code, tc.wantCode)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, tc.wantContent) {
			t.Errorf("%s: content-type = %q, want %q", tc.path, ct, tc.wantContent)
		}
		if tc.wantBody != "" && !bytes.Contains(rr.Body.Bytes(), []byte(tc.wantBody)) {
			t.Errorf("%s: body does not contain %q", tc.path, tc.wantBody)
		}
	}
}

func TestServeIndexPlainRelativeLinks(t *testing.T) {
	registerMimeTypes()
	a := newAPI(true, docsFS)
	handler := buildHandler(a, nil)

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	body := rr.Body.String()
	if strings.Contains(body, "<base") {
		t.Errorf("index.html must not contain an injected <base> tag")
	}
	if !strings.Contains(body, `href="vendor/bootstrap/bootstrap.min.css"`) {
		t.Errorf("index.html must keep plain relative asset links")
	}
	if !strings.Contains(body, `src="app.js"`) {
		t.Errorf("index.html must keep plain relative script links")
	}
}

func TestIndexRedirectAddsTrailingSlash(t *testing.T) {
	registerMimeTypes()
	a := newAPI(true, docsFS)

	// The app is served from the site root; a proxy that passes paths through
	// unchanged (or direct access) reaches the SPA fallback. Only a stripping
	// proxy's empty path is redirected (see TestIndexRedirectWithStrippedPrefix);
	// files and SPA-fallback paths are served without a trailing-slash redirect.
	handler := buildHandler(a, nil)

	cases := []struct {
		path     string
		wantCode int
		wantHTML bool
		wantCT   string
	}{
		// Real files are served, never redirected.
		{"/app.js", http.StatusOK, false, "text/javascript"},
		{"/style.css", http.StatusOK, false, "text/css"},
		{"/vendor/bootstrap/bootstrap.min.css", http.StatusOK, false, "text/css"},
		// The index document and SPA-fallback paths are served as-is.
		{"/", http.StatusOK, true, "text/html"},
		{"/index.html", http.StatusOK, true, "text/html"},
		{"/magooify", http.StatusOK, true, "text/html"},
		{"/magooify?x=1", http.StatusOK, true, "text/html"},
		{"/foo", http.StatusOK, true, "text/html"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != tc.wantCode {
			t.Errorf("%s: status = %d, want %d", tc.path, rr.Code, tc.wantCode)
		}
		if loc := rr.Header().Get("Location"); loc != "" {
			t.Errorf("%s: unexpected Location %q", tc.path, loc)
		}
		if tc.wantHTML && !strings.Contains(rr.Body.String(), "Magooify") {
			t.Errorf("%s: expected index.html body", tc.path)
		}
		if tc.wantCT != "" {
			if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, tc.wantCT) {
				t.Errorf("%s: content-type = %q, want %q", tc.path, ct, tc.wantCT)
			}
		}
	}
}

func TestIndexRedirectWithStrippedPrefix(t *testing.T) {
	// Traefik/Pangolin stripPrefix on a bare "/magooify" forwards an empty
	// path with X-Forwarded-Prefix; the redirect must point at the external
	// URL with a trailing slash.
	req := httptest.NewRequest("GET", "/", nil)
	req.URL.Path = ""
	req.Header.Set("X-Forwarded-Prefix", "/magooify")
	if loc, ok := indexRedirectLocation(req); !ok || loc != "/magooify/" {
		t.Errorf("empty path: loc = %q, ok = %v, want %q, true", loc, ok, "/magooify/")
	}

	// An unsafe X-Forwarded-Prefix must not turn the redirect into an open
	// redirect; it falls back to the site root.
	req = httptest.NewRequest("GET", "/", nil)
	req.URL.Path = ""
	req.Header.Set("X-Forwarded-Prefix", "//evil.com")
	if loc, ok := indexRedirectLocation(req); !ok || loc != "/" {
		t.Errorf("unsafe prefix: loc = %q, ok = %v, want %q, true", loc, ok, "/")
	}

	// The root path already ends in "/" and needs no redirect, even through a
	// strip proxy.
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-Prefix", "/magooify")
	if _, ok := indexRedirectLocation(req); ok {
		t.Errorf("root path: expected no redirect")
	}
}

func TestSafeForwardedPrefix(t *testing.T) {
	cases := map[string]string{
		"":             "",
		"/magooify":    "/magooify",
		"/magooify/":   "/magooify",
		"magooify":     "",
		"//evil.com":   "",
		"/magooify/..": "",
		"/magooify?x":  "",
		"/magooify#x":  "",
		"/magooify\\x": "",
		" /sub ":       "/sub",
	}
	for in, want := range cases {
		if got := safeForwardedPrefix(in); got != want {
			t.Errorf("safeForwardedPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServeWithoutBasePathStillWorks(t *testing.T) {
	registerMimeTypes()
	a := newAPI(true, docsFS)
	handler := buildHandler(a, nil)

	req := httptest.NewRequest("GET", "/style.css", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Errorf("content-type = %q, want text/css", ct)
	}

	req = httptest.NewRequest("GET", "/", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
}

func TestServeUnderBasePath(t *testing.T) {
	registerMimeTypes()
	a := newAPI(true, docsFS)
	// With --base-path=/magooify the app serves the sub-path (a proxy passes
	// the full path through) while continuing to serve the site root.
	handler := buildHandler(a, []string{"/magooify"})

	cases := []struct {
		path        string
		wantCode    int
		wantContent string
		wantBody    string
	}{
		// Under the base path.
		{"/magooify/", http.StatusOK, "text/html", "Magooify"},
		{"/magooify/style.css", http.StatusOK, "text/css", ".brand-icon"},
		{"/magooify/vendor/bootstrap/bootstrap.min.css", http.StatusOK, "text/css", "bootstrap"},
		{"/magooify/app.js", http.StatusOK, "text/javascript", "DOMContentLoaded"},
		{"/magooify/api/v1/user", http.StatusOK, "application/json", `"auth_type"`},
		{"/magooify/api/v1/health", http.StatusOK, "application/json", `"ok"`},
		{"/magooify/docs/api", http.StatusOK, "text/html", "swagger-ui"},
		{"/magooify/docs/swagger.json", http.StatusOK, "application/json", `"swagger"`},
		{"/magooify/docs/swagger-ui/swagger-ui.css", http.StatusOK, "text/css", "swagger-ui"},
		// The site root keeps working alongside the base path.
		{"/", http.StatusOK, "text/html", "Magooify"},
		{"/style.css", http.StatusOK, "text/css", ".brand-icon"},
		{"/app.js", http.StatusOK, "text/javascript", "DOMContentLoaded"},
		{"/api/v1/health", http.StatusOK, "application/json", `"ok"`},
		{"/docs/api", http.StatusOK, "text/html", "swagger-ui"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != tc.wantCode {
			t.Errorf("%s: status = %d, want %d", tc.path, rr.Code, tc.wantCode)
		}
		if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, tc.wantContent) {
			t.Errorf("%s: content-type = %q, want %q", tc.path, ct, tc.wantContent)
		}
		if tc.wantBody != "" && !bytes.Contains(rr.Body.Bytes(), []byte(tc.wantBody)) {
			t.Errorf("%s: body does not contain %q", tc.path, tc.wantBody)
		}
	}

	// A bare base path (no trailing slash) must 301 to the slash form so
	// relative links in index.html resolve under the base path.
	req := httptest.NewRequest("GET", "/magooify", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMovedPermanently {
		t.Errorf("/magooify: status = %d, want %d (redirect)", rr.Code, http.StatusMovedPermanently)
	}
	if loc := rr.Header().Get("Location"); loc != "/magooify/" {
		t.Errorf("/magooify: Location = %q, want %q", loc, "/magooify/")
	}

	// The query string survives the redirect.
	req = httptest.NewRequest("GET", "/magooify?x=1", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusMovedPermanently {
		t.Errorf("/magooify?x=1: status = %d, want %d (redirect)", rr.Code, http.StatusMovedPermanently)
	}
	if loc := rr.Header().Get("Location"); loc != "/magooify/?x=1" {
		t.Errorf("/magooify?x=1: Location = %q, want %q", loc, "/magooify/?x=1")
	}
}

func TestServeMultipleBasePaths(t *testing.T) {
	registerMimeTypes()
	a := newAPI(true, docsFS)
	handler := buildHandler(a, []string{"/magooify", "/other"})

	for _, prefix := range []string{"/magooify", "/other"} {
		req := httptest.NewRequest("GET", prefix, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusMovedPermanently {
			t.Errorf("%s: status = %d, want 301", prefix, rr.Code)
		}
		if loc := rr.Header().Get("Location"); loc != prefix+"/" {
			t.Errorf("%s: Location = %q, want %q", prefix, loc, prefix+"/")
		}
	}
}

func TestParseBasePaths(t *testing.T) {
	cases := map[string][]string{
		"":                 nil,
		"/magooify":        {"/magooify"},
		"/magooify,/foo":   {"/magooify", "/foo"},
		" magooify , /x/ ": {"/magooify", "/x"},
		"/,":               nil,
	}
	for in, want := range cases {
		got := parseBasePaths(in)
		if len(got) != len(want) {
			t.Errorf("parseBasePaths(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("parseBasePaths(%q) = %v, want %v", in, got, want)
				break
			}
		}
	}
}

func TestProcessPromptFromFile(t *testing.T) {
	a := newAPI(false, docsFS)
	got := a.processPrompt()
	if got == "" {
		t.Fatal("processPrompt() returned an empty prompt")
	}
	if !strings.Contains(got, "Clean this scanned image") {
		t.Errorf("processPrompt() does not contain the PROMPT.md text: %q", got)
	}
}

func TestProcessPromptFromCustomFile(t *testing.T) {
	promptFile := filepath.Join(t.TempDir(), "my-prompt.txt")
	if err := os.WriteFile(promptFile, []byte("Custom instructions here."), 0o644); err != nil {
		t.Fatalf("failed to write prompt file: %v", err)
	}

	a := newAPI(false, docsFS)
	a.promptFile = promptFile
	if got := a.processPrompt(); got != "Custom instructions here." {
		t.Errorf("processPrompt() = %q, want custom prompt text", got)
	}
}

func TestProcessPromptFallsBackWhenCustomFileMissing(t *testing.T) {
	a := newAPI(false, docsFS)
	a.promptFile = filepath.Join(t.TempDir(), "does-not-exist.txt")
	got := a.processPrompt()
	if got == "" || !strings.Contains(got, "Clean this scanned image") {
		t.Errorf("processPrompt() should fall back to PROMPT.md text, got %q", got)
	}
}

func TestProcessImageNoKey(t *testing.T) {
	a := newAPI(false, docsFS)
	a.outputDir = t.TempDir()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("image", "photo.png")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	fw.Write(miniPNG)
	mw.Close()

	req := httptest.NewRequest("POST", "/api/v1/process", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()

	a.processImage(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}
	var resp ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}
	if !strings.Contains(resp.Error, "OpenRouter API key not configured") {
		t.Errorf("error = %q, want a key-not-configured message", resp.Error)
	}

	entries, _ := os.ReadDir(a.outputDir)
	if len(entries) != 0 {
		t.Errorf("expected nothing stored when processing fails, found %d files", len(entries))
	}
}

func TestProcessImageRejectsNonImage(t *testing.T) {
	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.outputDir = t.TempDir()

	b64 := base64.StdEncoding.EncodeToString([]byte("this is not an image"))
	payload := fmt.Sprintf(`{"image":"%s"}`, b64)
	req := httptest.NewRequest("POST", "/api/v1/process", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	a.processImage(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
}

func TestImageProcessingRoutes(t *testing.T) {
	registerMimeTypes()

	processedPNG := base64.StdEncoding.EncodeToString(miniPNG)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode OpenRouter request: %v", err)
		}
		if body["model"] != defaultModel {
			t.Errorf("model = %v, want %q", body["model"], defaultModel)
		}
		if mods, ok := body["modalities"].([]any); !ok || len(mods) == 0 {
			t.Errorf("expected image modalities to be requested, got %v", body["modalities"])
		}
		messages := body["messages"].([]any)
		content := messages[0].(map[string]any)["content"].([]any)
		text := content[0].(map[string]any)["text"].(string)
		if text != strings.TrimSpace(promptMD) {
			t.Errorf("prompt text = %q, want PROMPT.md text %q", text, strings.TrimSpace(promptMD))
		}
		imagePart := content[1].(map[string]any)["image_url"].(map[string]any)["url"].(string)
		if !strings.HasPrefix(imagePart, "data:image/png;base64,") {
			t.Errorf("image data URL = %q, want a PNG data URL", imagePart)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":[{"type":"text","text":"cleaned"},{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}}]}`, processedPNG)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.openRouterURL = fake.URL
	a.outputDir = t.TempDir()
	handler := buildHandler(a, nil)

	// Process a multipart image upload.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("image", "photo.png")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	fw.Write(miniPNG)
	mw.Close()

	req := httptest.NewRequest("POST", "/api/v1/process", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("process status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var processed ProcessImageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &processed); err != nil {
		t.Fatalf("failed to parse process response: %v", err)
	}
	if !strings.HasSuffix(processed.Filename, ".png") {
		t.Errorf("filename = %q, want a .png name", processed.Filename)
	}

	// The processed image must be on disk; no description text file.
	if _, err := os.Stat(filepath.Join(a.outputDir, processed.Filename)); err != nil {
		t.Errorf("stored image missing: %v", err)
	}
	stored, err := os.ReadFile(filepath.Join(a.outputDir, processed.Filename))
	if err != nil {
		t.Fatalf("failed to read stored image: %v", err)
	}
	if !bytes.Equal(stored, miniPNG) {
		t.Errorf("stored bytes do not match the processed image returned by the model")
	}
	entries, _ := os.ReadDir(a.outputDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".txt") {
			t.Errorf("unexpected .txt file stored: %s", e.Name())
		}
	}

	// The stored image must be listed, newest first.
	req2 := httptest.NewRequest("GET", "/api/v1/images", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", rr2.Code, http.StatusOK)
	}
	var list ImagesResponse
	if err := json.Unmarshal(rr2.Body.Bytes(), &list); err != nil {
		t.Fatalf("failed to parse list response: %v", err)
	}
	if len(list.Images) != 1 {
		t.Fatalf("list has %d images, want 1", len(list.Images))
	}
	if list.Images[0].Filename != processed.Filename {
		t.Errorf("listed filename = %q, want %q", list.Images[0].Filename, processed.Filename)
	}

	// The stored image must be served back with the correct content type.
	req3 := httptest.NewRequest("GET", "/api/v1/images/"+processed.Filename, nil)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d", rr3.Code, http.StatusOK)
	}
	if ct := rr3.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q, want %q", ct, "image/png")
	}
	if !bytes.Equal(rr3.Body.Bytes(), miniPNG) {
		t.Errorf("served image bytes do not match the processed image")
	}
}

func TestProcessImageJSONPayload(t *testing.T) {
	processedPNG := base64.StdEncoding.EncodeToString(miniPNG)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"data:image/png;base64,%s"}}]}`, processedPNG)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.openRouterURL = fake.URL
	a.outputDir = t.TempDir()

	b64 := base64.StdEncoding.EncodeToString(miniPNG)
	payload := fmt.Sprintf(`{"image":"data:image/png;base64,%s","filename":"test.png"}`, b64)
	req := httptest.NewRequest("POST", "/api/v1/process", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	a.processImage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var resp ProcessImageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !strings.HasSuffix(resp.Filename, ".png") {
		t.Errorf("filename = %q, want a .png name", resp.Filename)
	}
	if resp.Model != defaultModel {
		t.Errorf("model = %q, want %q", resp.Model, defaultModel)
	}
}

func TestGetImageRejectsPathTraversal(t *testing.T) {
	a := newAPI(false, docsFS)
	a.outputDir = t.TempDir()

	req := httptest.NewRequest("GET", "/api/v1/images/whatever", nil)
	req.SetPathValue("filename", "../../etc/passwd")
	rr := httptest.NewRecorder()

	a.getImage(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestOpenRouterUpstreamFailure(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"bad model"}}`))
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.openRouterURL = fake.URL
	a.outputDir = t.TempDir()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("image", "photo.png")
	fw.Write(miniPNG)
	mw.Close()

	req := httptest.NewRequest("POST", "/api/v1/process", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()

	a.processImage(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadGateway, rr.Body.String())
	}
	var resp ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !strings.Contains(resp.Error, "OpenRouter returned status") {
		t.Errorf("error = %q, want upstream failure message", resp.Error)
	}
}
