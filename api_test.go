package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
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
		{"/style.css", http.StatusOK, "text/css", ".brand-logo"},
		{"/vendor/bootstrap/bootstrap.min.css", http.StatusOK, "text/css", "bootstrap"},
		{"/app.js", http.StatusOK, "text/javascript", "DOMContentLoaded"},
		{"/api/v1/user", http.StatusOK, "application/json", `"auth_type"`},
		{"/api/v1/health", http.StatusOK, "application/json", `"ok"`},
		{"/api/v1/prompt", http.StatusOK, "application/json", `"prompt"`},
		{"/api/v1/palettes", http.StatusOK, "application/json", `"brand"`},
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
		{"/magooify/style.css", http.StatusOK, "text/css", ".brand-logo"},
		{"/magooify/vendor/bootstrap/bootstrap.min.css", http.StatusOK, "text/css", "bootstrap"},
		{"/magooify/app.js", http.StatusOK, "text/javascript", "DOMContentLoaded"},
		{"/magooify/api/v1/user", http.StatusOK, "application/json", `"auth_type"`},
		{"/magooify/api/v1/health", http.StatusOK, "application/json", `"ok"`},
		{"/magooify/docs/api", http.StatusOK, "text/html", "swagger-ui"},
		{"/magooify/docs/swagger.json", http.StatusOK, "application/json", `"swagger"`},
		{"/magooify/docs/swagger-ui/swagger-ui.css", http.StatusOK, "text/css", "swagger-ui"},
		// The site root keeps working alongside the base path.
		{"/", http.StatusOK, "text/html", "Magooify"},
		{"/style.css", http.StatusOK, "text/css", ".brand-logo"},
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

func TestApplyPalettePromptReplacesColourLine(t *testing.T) {
	base := "Clean the image. Limit the colour palette to 16 colours, rendering any colours found to their nearest-fit colour."
	got := applyPalettePrompt(base, "crayola-4")
	if strings.Contains(got, "16 colours") {
		t.Errorf("palette prompt still has the original 16-colour line: %s", got)
	}
	for _, want := range []string{"white plus these 4 colours:", "#ed0a3f", "#0066cc", "rendering any colours found to their nearest-fit colour."} {
		if !strings.Contains(got, want) {
			t.Errorf("palette prompt missing %q: %s", want, got)
		}
	}
}

func TestApplyPalettePromptPreservesUnrelatedSentences(t *testing.T) {
	base := "Clean the image. Expand colours fully. Limit the colour palette to 16 colours, rendering any colours found to their nearest-fit colour."
	got := applyPalettePrompt(base, "berol-12")
	for _, want := range []string{"Clean the image.", "Expand colours fully.", "white plus these 12 colours:"} {
		if !strings.Contains(got, want) {
			t.Errorf("palette prompt missing %q: %s", want, got)
		}
	}
}

func TestApplyPalettePromptLeavesStringWhenNoPalette(t *testing.T) {
	base := "Limit the colour palette to 16 colours, rendering any colours found to their nearest-fit colour."
	if got := applyPalettePrompt(base, ""); got != base {
		t.Errorf("empty palette should leave the prompt unchanged, got %q", got)
	}
}

func TestApplyPalettePromptUnknownIDReturnsBase(t *testing.T) {
	base := "Limit the colour palette to 16 colours, rendering any colours found to their nearest-fit colour."
	if got := applyPalettePrompt(base, "not-a-palette"); got != base {
		t.Errorf("unknown palette should leave the prompt unchanged, got %q", got)
	}
}

func TestForcePNGReEncodesAsPNG(t *testing.T) {
	// Start with a small valid JPEG and confirm forcePNG returns PNG bytes.
	jpgSrc := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	var jpgBuf bytes.Buffer
	if err := jpeg.Encode(&jpgBuf, jpgSrc, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("failed to build JPEG test fixture: %v", err)
	}
	got, err := forcePNG(jpgBuf.Bytes())
	if err != nil {
		t.Fatalf("forcePNG returned error: %v", err)
	}
	if _, ft, err := image.DecodeConfig(bytes.NewReader(got)); err != nil {
		t.Fatalf("forcePNG output is not a valid image: %v", err)
	} else if ft != "png" {
		t.Errorf("forcePNG output format = %q, want png", ft)
	}
	// PNG input should round-trip cleanly too.
	pngBytes := pngOf(2, 2, func(x, y int) bool { return x == 0 && y == 0 })
	got, err = forcePNG(pngBytes)
	if err != nil {
		t.Fatalf("forcePNG on PNG returned error: %v", err)
	}
	if _, ft, _ := image.DecodeConfig(bytes.NewReader(got)); ft != "png" {
		t.Errorf("forcePNG(PNG) output format = %q, want png", ft)
	}
}

func TestForcePNGLeavesSVGAlone(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="4" height="4"></svg>`)
	got, err := forcePNG(svg)
	if err != nil {
		t.Fatalf("forcePNG on SVG returned error: %v", err)
	}
	if !bytes.Equal(got, svg) {
		t.Errorf("forcePNG modified an SVG document")
	}
}

func TestForcePNGRejectsGarbage(t *testing.T) {
	if _, err := forcePNG([]byte("not an image")); err == nil {
		t.Errorf("forcePNG should fail on undecodable input")
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
	// Bitmap output is always re-encoded as PNG before storage, so the file
	// must be a valid PNG that decodes to a 1x1 image regardless of what
	// the model returned.
	if _, ct, err := image.DecodeConfig(bytes.NewReader(stored)); err != nil {
		t.Errorf("stored image is not a valid image: %v", err)
	} else if ct != "png" {
		t.Errorf("stored format = %q, want png", ct)
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

	// The stored image must be served back with the correct content type and
	// it must be the same PNG-encoded file the server wrote.
	req3 := httptest.NewRequest("GET", "/api/v1/images/"+processed.Filename, nil)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("get status = %d, want %d", rr3.Code, http.StatusOK)
	}
	if ct := rr3.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q, want %q", ct, "image/png")
	}
	if !bytes.Equal(rr3.Body.Bytes(), stored) {
		t.Errorf("served image bytes do not match the stored image")
	}
	if _, ct, _ := image.DecodeConfig(bytes.NewReader(rr3.Body.Bytes())); ct != "png" {
		t.Errorf("served image format = %q, want png", ct)
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

func TestPromptEndpoint(t *testing.T) {
	a := newAPI(false, docsFS)
	req := httptest.NewRequest("GET", "/api/v1/prompt", nil)
	rr := httptest.NewRecorder()

	a.prompt(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if strings.TrimSpace(resp["prompt"]) != strings.TrimSpace(promptMD) {
		t.Errorf("prompt = %q, want PROMPT.md text", resp["prompt"])
	}
}

func TestProcessImageCustomPrompt(t *testing.T) {
	processedPNG := base64.StdEncoding.EncodeToString(miniPNG)
	var gotPrompt string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode OpenRouter request: %v", err)
		}
		messages := body["messages"].([]any)
		content := messages[0].(map[string]any)["content"].([]any)
		gotPrompt = content[0].(map[string]any)["text"].(string)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"data:image/png;base64,%s"}}]}`, processedPNG)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.openRouterURL = fake.URL
	a.outputDir = t.TempDir()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("image", "photo.png")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	fw.Write(miniPNG)
	mw.WriteField("prompt", "Clean the scan and sharpen the text")
	mw.Close()

	req := httptest.NewRequest("POST", "/api/v1/process", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()

	a.processImage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if gotPrompt != "Clean the scan and sharpen the text" {
		t.Errorf("prompt sent to model = %q, want the custom prompt", gotPrompt)
	}
}

func TestProcessImageOverwriteOutput(t *testing.T) {
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

	// First process without an output name generates a fresh filename.
	req := httptest.NewRequest("POST", "/api/v1/process",
		strings.NewReader(`{"image":"data:image/png;base64,`+base64.StdEncoding.EncodeToString(miniPNG)+`"}`))
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
		t.Fatalf("filename = %q, want a .png name", resp.Filename)
	}

	// Reprocess with the same output name: the file must be replaced, not
	// duplicated.
	payload := fmt.Sprintf(`{"image":"data:image/png;base64,%s","output":%q}`,
		base64.StdEncoding.EncodeToString(miniPNG), resp.Filename)
	req2 := httptest.NewRequest("POST", "/api/v1/process", strings.NewReader(payload))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	a.processImage(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rr2.Code, http.StatusOK, rr2.Body.String())
	}
	var resp2 ProcessImageResponse
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp2.Filename != resp.Filename {
		t.Errorf("filename = %q, want %q", resp2.Filename, resp.Filename)
	}

	entries, _ := os.ReadDir(a.outputDir)
	if len(entries) != 1 {
		t.Fatalf("output dir has %d files, want 1 (reprocessed image replaces the original)", len(entries))
	}
	if entries[0].Name() != resp.Filename {
		t.Errorf("stored file = %q, want %q", entries[0].Name(), resp.Filename)
	}
}

func TestStoreResultRejectsTraversalOutput(t *testing.T) {
	a := newAPI(false, docsFS)
	a.outputDir = t.TempDir()

	for _, out := range []string{"../evil.png", "a/b.png", "..", "."} {
		if _, err := a.storeResult(miniPNG, out); err == nil {
			t.Errorf("storeResult(%q) expected an error, got none", out)
		}
	}
	entries, _ := os.ReadDir(a.outputDir)
	if len(entries) != 0 {
		t.Errorf("expected nothing stored for invalid filenames, found %d files", len(entries))
	}
}

func TestOpenRouterEmptyResponse(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(nil)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.openRouterURL = fake.URL
	a.outputDir = t.TempDir()

	req := httptest.NewRequest("POST", "/api/v1/process",
		strings.NewReader(`{"image":"data:image/png;base64,`+base64.StdEncoding.EncodeToString(miniPNG)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	a.processImage(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadGateway, rr.Body.String())
	}
	var resp ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !strings.Contains(resp.Error, "empty response") {
		t.Errorf("error = %q, want a clear empty-response message", resp.Error)
	}
	if strings.Contains(resp.Error, "unexpected end of JSON input") {
		t.Errorf("error = %q, should not leak JSON parse internals", resp.Error)
	}
}

func TestOpenRouterOversizedResponse(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"data:image/png;base64,%s"}}]}`,
			strings.Repeat("A", maxOpenRouterBytes))
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.openRouterURL = fake.URL
	a.outputDir = t.TempDir()

	req := httptest.NewRequest("POST", "/api/v1/process",
		strings.NewReader(`{"image":"data:image/png;base64,`+base64.StdEncoding.EncodeToString(miniPNG)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	a.processImage(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadGateway, rr.Body.String())
	}
	var resp ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !strings.Contains(resp.Error, "size limit") {
		t.Errorf("error = %q, want a size-limit message", resp.Error)
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

func TestCreditsEndpointWithoutManagementKey(t *testing.T) {
	a := newAPI(false, docsFS)
	req := httptest.NewRequest("GET", "/api/v1/credits", nil)
	rr := httptest.NewRecorder()

	a.credits(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	var resp CreditsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.CreditsAvailable {
		t.Errorf("credits_available = true, want false without a management key")
	}
	if resp.RemainingCredits != 0 || resp.TotalCredits != 0 || resp.TotalUsage != 0 {
		t.Errorf("balance fields should be zero without a management key, got %+v", resp)
	}
}

func TestCreditsEndpointWithManagementKey(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/credits" {
			t.Errorf("request path = %q, want /api/v1/credits", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer mgmt-test-key" {
			t.Errorf("authorization = %q, want the management key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"total_credits":100.5,"total_usage":25.75}}`)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.managementKey = "mgmt-test-key"
	a.creditsURL = fake.URL + "/api/v1/credits"

	remaining, total, used, ok := a.fetchCredits()
	if !ok {
		t.Fatalf("fetchCredits returned ok=false, want true")
	}
	if total != 100.5 {
		t.Errorf("total = %v, want 100.5", total)
	}
	if used != 25.75 {
		t.Errorf("usage = %v, want 25.75", used)
	}
	if remaining != 74.75 {
		t.Errorf("remaining = %v, want 74.75", remaining)
	}

	req := httptest.NewRequest("GET", "/api/v1/credits", nil)
	rr := httptest.NewRecorder()
	a.credits(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	var resp CreditsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp.CreditsAvailable {
		t.Errorf("credits_available = false, want true")
	}
	if resp.RemainingCredits != 74.75 {
		t.Errorf("remaining_credits = %v, want 74.75", resp.RemainingCredits)
	}
}

func TestCreditsEndpointManagementKeyFailsGracefully(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid management key"}`))
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.managementKey = "mgmt-bad-key"
	a.creditsURL = fake.URL + "/api/v1/credits"

	req := httptest.NewRequest("GET", "/api/v1/credits", nil)
	rr := httptest.NewRecorder()
	a.credits(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (balance errors are reported as unavailable)", rr.Code, http.StatusOK)
	}
	var resp CreditsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.CreditsAvailable {
		t.Errorf("credits_available = true, want false when the credits query fails")
	}
}

func TestProcessImageReturnsAndAccumulatesCost(t *testing.T) {
	processedPNG := base64.StdEncoding.EncodeToString(miniPNG)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"data:image/png;base64,%s"}}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,"cost":0.000123}}`, processedPNG)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.openRouterURL = fake.URL
	a.outputDir = t.TempDir()

	process := func() ProcessImageResponse {
		req := httptest.NewRequest("POST", "/api/v1/process",
			strings.NewReader(`{"image":"data:image/png;base64,`+base64.StdEncoding.EncodeToString(miniPNG)+`"}`))
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
		return resp
	}

	first := process()
	if first.Cost != 0.000123 {
		t.Errorf("cost = %v, want 0.000123", first.Cost)
	}
	if first.SessionCost != 0.000123 {
		t.Errorf("session_cost after one request = %v, want 0.000123", first.SessionCost)
	}

	second := process()
	if second.Cost != 0.000123 {
		t.Errorf("cost = %v, want 0.000123", second.Cost)
	}
	if second.SessionCost != 0.000246 {
		t.Errorf("session_cost after two requests = %v, want 0.000246", second.SessionCost)
	}
}

func TestProcessImageNoReportedCost(t *testing.T) {
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

	req := httptest.NewRequest("POST", "/api/v1/process",
		strings.NewReader(`{"image":"data:image/png;base64,`+base64.StdEncoding.EncodeToString(miniPNG)+`"}`))
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
	if resp.Cost != 0 {
		t.Errorf("cost = %v, want 0 when OpenRouter reports no cost", resp.Cost)
	}
}

func TestProcessImageFallsBackToImagesEndpoint(t *testing.T) {
	processedPNG := base64.StdEncoding.EncodeToString(miniPNG)
	chatPath := "/chat"
	imagesPath := "/images"
	imageRequests := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case chatPath:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"message":"This model cannot be used with the chat/completions endpoint. Use the /api/v1/images endpoint instead."}}`)
		case imagesPath:
			imageRequests++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode images request: %v", err)
			}
			if body["model"] != "openai/gpt-image-1-mini" {
				t.Errorf("images model = %v, want openai/gpt-image-1-mini", body["model"])
			}
			if body["prompt"] == "" {
				t.Errorf("images prompt missing")
			}
			if refs, ok := body["image_reference"].([]any); !ok || len(refs) != 1 {
				t.Errorf("expected an image_reference with the source image, got %v", body["image_reference"])
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Request-Cost", "0.02")
			fmt.Fprintf(w, `{"id":"gen-1","model":"openai/gpt-image-1-mini","data":[{"b64_json":"%s"}]}`, processedPNG)
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.model = "openai/gpt-image-1-mini"
	a.openRouterURL = fake.URL + chatPath
	a.imagesGenerateURL = fake.URL + imagesPath
	a.outputDir = t.TempDir()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("image", "photo.png")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	fw.Write(miniPNG)
	mw.Close()

	handler := buildHandler(a, nil)
	req := httptest.NewRequest("POST", "/api/v1/process", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("process status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if imageRequests != 1 {
		t.Errorf("images endpoint calls = %d, want 1", imageRequests)
	}
	var processed ProcessImageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &processed); err != nil {
		t.Fatalf("failed to parse process response: %v", err)
	}
	if processed.Cost != 0.02 {
		t.Errorf("cost = %v, want 0.02 from the images response header", processed.Cost)
	}
	stored, err := os.ReadFile(filepath.Join(a.outputDir, processed.Filename))
	if err != nil {
		t.Fatalf("failed to read stored image: %v", err)
	}
	// Bitmap output is re-encoded as PNG before storage; the file must be a
	// valid PNG regardless of the format returned by the model.
	if _, ct, err := image.DecodeConfig(bytes.NewReader(stored)); err != nil {
		t.Errorf("stored image is not a valid image: %v", err)
	} else if ct != "png" {
		t.Errorf("stored format = %q, want png", ct)
	}
}

func TestProcessImageNoChatFallbackForOtherErrors(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.openRouterURL = fake.URL
	a.imagesGenerateURL = fake.URL + "/images"
	a.outputDir = t.TempDir()

	req := httptest.NewRequest("POST", "/api/v1/process",
		strings.NewReader(`{"image":"data:image/png;base64,`+base64.StdEncoding.EncodeToString(miniPNG)+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	a.processImage(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadGateway, rr.Body.String())
	}
	var resp ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !strings.Contains(resp.Error, "status 429") {
		t.Errorf("error = %q, want the upstream 429 error surfaced", resp.Error)
	}
}

func TestExtractCost(t *testing.T) {
	hdr := http.Header{"X-Request-Cost": []string{"0.5"}}

	// usage.cost in the body wins over the header.
	if got := extractCost([]byte(`{"usage":{"cost":0.123}}`), hdr); got != 0.123 {
		t.Errorf("extractCost(body) = %v, want 0.123", got)
	}
	// The header is used when the body has no usage.cost.
	if got := extractCost([]byte(`{"usage":{}}`), hdr); got != 0.5 {
		t.Errorf("extractCost(header) = %v, want 0.5", got)
	}
	// Nothing reported anywhere yields zero.
	if got := extractCost([]byte(`{"choices":[]}`), http.Header{}); got != 0 {
		t.Errorf("extractCost(none) = %v, want 0", got)
	}
	// Malformed header is ignored.
	if got := extractCost([]byte(`{}`), http.Header{"X-Request-Cost": []string{"oops"}}); got != 0 {
		t.Errorf("extractCost(malformed) = %v, want 0", got)
	}
}

func TestBuildImageModelInfo(t *testing.T) {
	mk := func(input, output []string) openRouterImageModel {
		m := openRouterImageModel{ID: "model/id", Name: "Model"}
		m.Architecture.Input = input
		m.Architecture.Output = output
		return m
	}

	// Model with an exact per-image price published for both lines.
	exact := buildImageModelInfo(mk([]string{"image", "text"}, []string{"image", "text"}), []openRouterImageEndpoint{
		{ProviderName: "Provider A", Pricing: []openRouterImagePrice{
			{Billable: "input_image", Unit: "image", CostUSD: 0.0004},
			{Billable: "output_image", Unit: "image", CostUSD: 0.03},
		}},
	})
	if !exact.InputImageCostKnown || !exact.OutputImageCostKnown {
		t.Errorf("exact model flags = %+v, want all true", exact)
	}
	if exact.InputImageCost != 0.0004 || exact.OutputImageCost != 0.03 {
		t.Errorf("exact costs = input %v output %v, want 0.0004 / 0.03", exact.InputImageCost, exact.OutputImageCost)
	}
	if exact.EstimatedImageCost != 0.0304 {
		t.Errorf("estimated cost = %v, want 0.0304", exact.EstimatedImageCost)
	}

	// Model with only per-token rates: cost is estimated from the expected
	// token count for one image.
	token := buildImageModelInfo(mk([]string{"image"}, []string{"image", "text"}), []openRouterImageEndpoint{
		{ProviderName: "Provider A", Pricing: []openRouterImagePrice{
			{Billable: "input_image", Unit: "token", CostUSD: 0.0000001},
			{Billable: "output_image", Unit: "token", CostUSD: 0.0000004},
		}},
	})
	if token.InputImageCostKnown || token.OutputImageCostKnown {
		t.Errorf("token model should not have exact per-image prices")
	}
	if want := 0.0000001 * imageInputTokenEstimate; math.Abs(token.InputImageCost-want) > 1e-12 {
		t.Errorf("estimated input cost = %v, want %v", token.InputImageCost, want)
	}
	if want := 0.0000004 * imageOutputTokenEstimate; math.Abs(token.OutputImageCost-want) > 1e-12 {
		t.Errorf("estimated output cost = %v, want %v", token.OutputImageCost, want)
	}

	// The cheapest provider price wins for each line.
	cheap := buildImageModelInfo(mk([]string{"image"}, []string{"image"}), []openRouterImageEndpoint{
		{ProviderName: "A", Pricing: []openRouterImagePrice{{Billable: "output_image", Unit: "image", CostUSD: 0.5}}},
		{ProviderName: "B", Pricing: []openRouterImagePrice{{Billable: "output_image", Unit: "image", CostUSD: 0.08}}},
	})
	if cheap.OutputImageCost != 0.08 || !cheap.OutputImageCostKnown {
		t.Errorf("cheapest provider cost = %v (known %v), want 0.08/true", cheap.OutputImageCost, cheap.OutputImageCostKnown)
	}

	// An input_reference price stands in for the input image price.
	ref := buildImageModelInfo(mk([]string{"image"}, []string{"image"}), []openRouterImageEndpoint{
		{ProviderName: "A", Pricing: []openRouterImagePrice{
			{Billable: "input_reference", Unit: "image", CostUSD: 0.12},
			{Billable: "output_image", Unit: "image", CostUSD: 0.3},
		}},
	})
	if ref.InputImageCost != 0.12 || !ref.InputImageCostKnown {
		t.Errorf("input_reference cost = %v (known %v), want 0.12/true", ref.InputImageCost, ref.InputImageCostKnown)
	}

	// No pricing published: costs are zero and not marked exact.
	none := buildImageModelInfo(mk([]string{"image"}, []string{"image"}), nil)
	if none.InputImageCost != 0 || none.OutputImageCost != 0 || none.InputImageCostKnown || none.OutputImageCostKnown {
		t.Errorf("no-pricing model = %+v, want zero, unknown costs", none)
	}
}

func TestModelsEndpointAndCache(t *testing.T) {
	endpoints := map[string]string{
		"gemini-3.1-flash-lite-image": `{"endpoints":[{"provider_name":"OpenRouter","pricing":[
			{"billable":"input_image","unit":"token","cost_usd":0.00000025},
			{"billable":"output_image","unit":"token","cost_usd":0.00003}
		]}]}`,
		"gpt-4o": `{"endpoints":[{"provider_name":"OpenRouter","pricing":[
			{"billable":"input_image","unit":"token","cost_usd":0.0000025},
			{"billable":"output_image","unit":"token","cost_usd":0.00001}
		]}]}`,
	}
	const listBody = `{"data":[
		{"id":"google/gemini-3.1-flash-lite-image","name":"Nano Banana 2 Lite",
		 "architecture":{"input_modalities":["image","text"],"output_modalities":["image","text"]},
		 "endpoints":"/api/v1/images/models/google/gemini-3.1-flash-lite-image/endpoints"},
		{"id":"openai/gpt-4o","name":"GPT-4o",
		 "architecture":{"input_modalities":["image","text"],"output_modalities":["image","text"]},
		 "endpoints":"/api/v1/images/models/openai/gpt-4o/endpoints"},
		{"id":"anthropic/claude-text","name":"Text Only",
		 "architecture":{"input_modalities":["text"],"output_modalities":["text"]},
		 "endpoints":""}
	]}`
	hits := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("request Authorization header = %q, want the configured key", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/endpoints") {
			for id, body := range endpoints {
				if strings.Contains(r.URL.Path, id) {
					fmt.Fprint(w, body)
					return
				}
			}
			http.Error(w, "no such model", http.StatusNotFound)
			return
		}
		fmt.Fprint(w, listBody)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.imagesURL = fake.URL
	a.openRouterKey = "test-key"

	// First request hits the upstream and returns models sorted by cost.
	req := httptest.NewRequest("GET", "/api/v1/models", nil)
	rr := httptest.NewRecorder()
	a.models(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var resp ModelsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp.Models) != 2 {
		t.Fatalf("got %d models, want 2 (text-only filtered out)", len(resp.Models))
	}
	// The cheaper model (0.0169, per-token estimate) sorts before the
	// image-output model (0.0391, which pays for a generated image at
	// 0.00003/token).
	if resp.Models[0].ID != "openai/gpt-4o" {
		t.Errorf("models[0] = %q, want the cheaper model first", resp.Models[0].ID)
	}
	if resp.Models[1].ID != "google/gemini-3.1-flash-lite-image" {
		t.Errorf("models[1] = %q, want the image-output model second", resp.Models[1].ID)
	}

	// A second request within the TTL must come from the cache. The first pass
	// made one list request plus one endpoints request per image model (3).
	rr2 := httptest.NewRecorder()
	a.models(rr2, req)
	if hits != 3 {
		t.Errorf("upstream hit count = %d, want 3 (list + 2 endpoints, then cached)", hits)
	}

	// Expiring the cache forces a refetch.
	a.mu.Lock()
	a.modelsCachedAt = a.modelsCachedAt.Add(-2 * modelsCacheTTL)
	a.mu.Unlock()
	rr3 := httptest.NewRecorder()
	a.models(rr3, req)
	if rr3.Code != http.StatusOK {
		t.Fatalf("refetch status = %d, want %d", rr3.Code, http.StatusOK)
	}
	if hits != 6 {
		t.Errorf("upstream hit count = %d, want 6 after cache expiry", hits)
	}
}

func TestModelsEndpointUpstreamFailure(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.imagesURL = fake.URL
	a.openRouterKey = "test-key"

	req := httptest.NewRequest("GET", "/api/v1/models", nil)
	rr := httptest.NewRecorder()
	a.models(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusBadGateway, rr.Body.String())
	}
	var resp ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !strings.Contains(resp.Error, "status 429") {
		t.Errorf("error = %q, want a clear upstream status message", resp.Error)
	}
}

func TestModelsEndpointRequiresKey(t *testing.T) {
	a := newAPI(false, docsFS)

	req := httptest.NewRequest("GET", "/api/v1/models", nil)
	rr := httptest.NewRecorder()
	a.models(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}
	var resp ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !strings.Contains(resp.Error, "API key not configured") {
		t.Errorf("error = %q, want a clear not-configured message", resp.Error)
	}
}

func TestSetModel(t *testing.T) {
	listBody := `{"data":[
		{"id":"google/gemini-3.1-flash-lite-image","name":"Nano Banana 2 Lite",
		 "architecture":{"input_modalities":["image","text"],"output_modalities":["image","text"]},
		 "endpoints":""},
		{"id":"recraft/recraft-v4.1-vector","name":"Recraft V4.1 Vector",
		 "architecture":{"input_modalities":["image"],"output_modalities":["image"]},
		 "endpoints":""}
	]}`
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, listBody)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.imagesURL = fake.URL
	a.openRouterKey = "test-key"
	if a.model != defaultModel {
		t.Fatalf("initial model = %q, want %q", a.model, defaultModel)
	}

	// Switching to a known model succeeds and takes effect immediately.
	req := httptest.NewRequest("PUT", "/api/v1/model",
		strings.NewReader(`{"model":"recraft/recraft-v4.1-vector"}`))
	rr := httptest.NewRecorder()
	a.setModel(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("setModel status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if a.model != "recraft/recraft-v4.1-vector" {
		t.Errorf("model = %q after switch, want recraft/recraft-v4.1-vector", a.model)
	}
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["model"] != "recraft/recraft-v4.1-vector" {
		t.Errorf("response model = %q, want the switched model", resp["model"])
	}

	// An unknown model is rejected and the current model is unchanged.
	req2 := httptest.NewRequest("PUT", "/api/v1/model", strings.NewReader(`{"model":"no/such-model"}`))
	rr2 := httptest.NewRecorder()
	a.setModel(rr2, req2)
	if rr2.Code != http.StatusNotFound {
		t.Fatalf("unknown model status = %d, want %d", rr2.Code, http.StatusNotFound)
	}
	if a.model != "recraft/recraft-v4.1-vector" {
		t.Errorf("model changed after a rejected switch: %q", a.model)
	}

	// Empty and malformed payloads are rejected.
	for _, body := range []string{`{}`, `{"model":""}`, `oops`} {
		rr3 := httptest.NewRecorder()
		a.setModel(rr3, httptest.NewRequest("PUT", "/api/v1/model", strings.NewReader(body)))
		if rr3.Code != http.StatusBadRequest {
			t.Errorf("payload %q status = %d, want %d", body, rr3.Code, http.StatusBadRequest)
		}
	}
}

func TestSetModelRequiresKey(t *testing.T) {
	a := newAPI(false, docsFS)

	req := httptest.NewRequest("PUT", "/api/v1/model",
		strings.NewReader(`{"model":"google/gemini-3.1-flash-lite-image"}`))
	rr := httptest.NewRecorder()
	a.setModel(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}
}

func TestModelsEndpointEndpointsFailure(t *testing.T) {
	listBody := `{"data":[
		{"id":"recraft/recraft-v4.1-vector","name":"Recraft V4.1 Vector",
		 "architecture":{"input_modalities":["image"],"output_modalities":["image"]},
		 "endpoints":"/api/v1/images/models/recraft/recraft-v4.1-vector/endpoints"}
	]}`
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/endpoints") {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":"boom"}`)
			return
		}
		fmt.Fprint(w, listBody)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.imagesURL = fake.URL
	a.openRouterKey = "test-key"

	req := httptest.NewRequest("GET", "/api/v1/models", nil)
	rr := httptest.NewRecorder()
	a.models(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var resp ModelsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("got %d models, want 1 (endpoints failure must not drop the model)", len(resp.Models))
	}
	if resp.Models[0].ID != "recraft/recraft-v4.1-vector" {
		t.Errorf("model = %q, want the recraft model", resp.Models[0].ID)
	}
	if resp.Models[0].InputImageCost != 0 || resp.Models[0].OutputImageCost != 0 {
		t.Errorf("model costs = %+v, want zero when pricing is unavailable", resp.Models[0])
	}
}

func TestBestImagePrice(t *testing.T) {
	pricing := []openRouterImageEndpoint{
		{ProviderName: "A", Pricing: []openRouterImagePrice{
			{Billable: "output_image", Unit: "token", CostUSD: 0.00003},
			{Billable: "output_image", Unit: "image", CostUSD: 0.3},
			{Billable: "web_search", Unit: "request", CostUSD: 0.014},
		}},
		{ProviderName: "B", Pricing: []openRouterImagePrice{
			{Billable: "output_image", Unit: "image", CostUSD: 0.08},
		}},
	}

	exact, perToken := bestImagePrice(pricing, "output_image")
	if exact != 0.08 {
		t.Errorf("bestImagePrice exact = %v, want 0.08 (cheapest per-image)", exact)
	}
	if perToken != 0.00003 {
		t.Errorf("bestImagePrice perToken = %v, want 0.00003", perToken)
	}

	// A billable line with no matching entry yields zeros.
	if exact, perToken := bestImagePrice(pricing, "input_image"); exact != 0 || perToken != 0 {
		t.Errorf("bestImagePrice(input_image) = %v/%v, want 0/0", exact, perToken)
	}
	if exact, perToken := bestImagePrice(nil, "output_image"); exact != 0 || perToken != 0 {
		t.Errorf("bestImagePrice(none) = %v/%v, want 0/0", exact, perToken)
	}
}

func TestPerImageCost(t *testing.T) {
	if cost, known := perImageCost(0.08, 0.00003, imageOutputTokenEstimate); cost != 0.08 || !known {
		t.Errorf("perImageCost(exact) = %v/%v, want 0.08/true", cost, known)
	}
	if cost, known := perImageCost(0, 0.00003, imageOutputTokenEstimate); cost != 0.00003*imageOutputTokenEstimate || known {
		t.Errorf("perImageCost(token) = %v/%v, want estimate/false", cost, known)
	}
	if cost, known := perImageCost(0, 0, imageOutputTokenEstimate); cost != 0 || known {
		t.Errorf("perImageCost(none) = %v/%v, want 0/false", cost, known)
	}
}

// pngOf encodes a w×h black-and-white PNG; ink(x, y) selects the black pixels.
func pngOf(w, h int, ink func(x, y int) bool) []byte {
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if ink(x, y) {
				img.SetGray(x, y, color.Gray{Y: 0})
			} else {
				img.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// pngOfColour encodes a w×h RGBA PNG; colour(x, y) selects each pixel.
func pngOfColour(w, h int, colour func(x, y int) color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, colour(x, y))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestColourLayersSplitByColour(t *testing.T) {
	src := pngOfColour(8, 8, func(x, y int) color.RGBA {
		if x >= 1 && x <= 3 && y >= 1 && y <= 3 {
			return color.RGBA{255, 0, 0, 255}
		}
		if x >= 5 && x <= 7 && y >= 5 && y <= 7 {
			return color.RGBA{0, 0, 255, 255}
		}
		return color.RGBA{255, 255, 255, 255}
	})
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		t.Fatalf("failed to decode test image: %v", err)
	}

	layers := colourLayers(img)
	if len(layers) != 2 {
		t.Fatalf("got %d colour layers, want 2", len(layers))
	}
	fills := map[string]bool{}
	for _, l := range layers {
		fills[fmt.Sprintf("%02x%02x%02x", l.r, l.g, l.b)] = true
		if l.count != 9 {
			t.Errorf("layer #%02x%02x%02x has %d pixels, want 9", l.r, l.g, l.b, l.count)
		}
	}
	if !fills["ff0000"] || !fills["0000ff"] {
		t.Errorf("layer fills = %v, want ff0000 and 0000ff", fills)
	}
}

func TestColourLayersAllWhiteNoLayers(t *testing.T) {
	src := pngOfColour(4, 4, func(x, y int) color.RGBA { return color.RGBA{255, 255, 255, 255} })
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		t.Fatalf("failed to decode test image: %v", err)
	}
	if layers := colourLayers(img); len(layers) != 0 {
		t.Errorf("all-white image produced %d layers, want 0", len(layers))
	}
}

func TestNearWhiteKeepsBrightColours(t *testing.T) {
	if !isNearWhite(255, 255, 255) {
		t.Errorf("white not treated as near-white background")
	}
	if !isNearWhite(248, 248, 248) {
		t.Errorf("light grey not treated as near-white background")
	}
	if isNearWhite(255, 255, 0) {
		t.Errorf("yellow incorrectly treated as near-white background")
	}
	if isNearWhite(135, 206, 250) {
		t.Errorf("light blue incorrectly treated as near-white background")
	}
}

func TestVectoriseBitmapColourLayers(t *testing.T) {
	src := pngOfColour(8, 8, func(x, y int) color.RGBA {
		if x >= 1 && x <= 3 && y >= 1 && y <= 3 {
			return color.RGBA{0, 0, 0, 255}
		}
		if x >= 5 && x <= 7 && y >= 5 && y <= 7 {
			return color.RGBA{255, 255, 0, 255}
		}
		return color.RGBA{255, 255, 255, 255}
	})

	svg, err := vectoriseBitmap(src, "")
	if err != nil {
		t.Fatalf("vectoriseBitmap returned error: %v", err)
	}
	body := string(svg)
	if !strings.Contains(body, `fill="#000000"`) {
		t.Errorf("SVG missing the black layer fill: %s", body)
	}
	if !strings.Contains(body, `fill="#ffff00"`) {
		t.Errorf("SVG missing the yellow layer fill: %s", body)
	}
	if strings.Count(body, "<g fill=") != 2 {
		t.Errorf("expected 2 colour groups, got %d", strings.Count(body, "<g fill="))
	}
	if !bytes.Contains(svg, []byte(`fill-rule="evenodd"`)) {
		t.Errorf("SVG is missing the even-odd fill rule")
	}
}

func TestVectoriseBitmapProducesSVG(t *testing.T) {
	src := pngOf(4, 4, func(x, y int) bool { return x >= 1 && x <= 2 && y >= 1 && y <= 2 })

	svg, err := vectoriseBitmap(src, "")
	if err != nil {
		t.Fatalf("vectoriseBitmap returned error: %v", err)
	}
	if !bytes.HasPrefix(svg, []byte(`<?xml`)) || !bytes.Contains(svg, []byte("<svg")) {
		t.Errorf("output is not an SVG document: %s", svg)
	}
	if !bytes.Contains(svg, []byte(`width="4"`)) || !bytes.Contains(svg, []byte(`height="4"`)) {
		t.Errorf("SVG is missing the image dimensions: %s", svg)
	}
	if !bytes.Contains(svg, []byte(`<path d="M`)) {
		t.Errorf("SVG has no traced path: %s", svg)
	}
	if !bytes.Contains(svg, []byte(`fill-rule="evenodd"`)) {
		t.Errorf("SVG is missing the even-odd fill rule: %s", svg)
	}
}

func TestVectoriseBitmapPassesThroughSVG(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><path d="M0 0"/></svg>`)
	got, err := vectoriseBitmap(svg, "")
	if err != nil {
		t.Fatalf("vectoriseBitmap returned error: %v", err)
	}
	if !bytes.Equal(got, svg) {
		t.Errorf("SVG input was not passed through unchanged")
	}
}

func TestVectoriseBitmapRejectsUndecodableInput(t *testing.T) {
	if _, err := vectoriseBitmap([]byte("this is not an image"), ""); err == nil {
		t.Errorf("expected an error for undecodable input, got none")
	}
}

func TestVectoriseBitmapUsesPaletteColours(t *testing.T) {
	// Synthetic 8x8 PNG: two solid colour blocks whose nearest palette matches
	// are Crayola red (#ED0A3F) and Crayola blue (#0066CC), plus a white
	// background that should remain untraced.
	w, h := 8, 8
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case x < 4 && y < 4:
				img.SetNRGBA(x, y, color.NRGBA{R: 0xEE, G: 0x10, B: 0x40, A: 0xFF})
			case x >= 4 && y >= 4:
				img.SetNRGBA(x, y, color.NRGBA{R: 0x00, G: 0x66, B: 0xCC, A: 0xFF})
			default:
				img.SetNRGBA(x, y, color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("failed to encode test PNG: %v", err)
	}

	got, err := vectoriseBitmap(buf.Bytes(), "crayola-12")
	if err != nil {
		t.Fatalf("vectoriseBitmap with palette returned error: %v", err)
	}
	svg := string(got)
	if !strings.Contains(svg, "#ed0a3f") {
		t.Errorf("palette SVG is missing Crayola red (#ed0a3f): %s", svg)
	}
	if !strings.Contains(svg, "#0066cc") {
		t.Errorf("palette SVG is missing Crayola blue (#0066cc): %s", svg)
	}
}

func TestFindPaletteRejectsUnknownID(t *testing.T) {
	if _, ok := findPalette("does-not-exist"); ok {
		t.Errorf("findPalette should report ok=false for an unknown ID")
	}
	if _, ok := findPalette("crayola-12"); !ok {
		t.Errorf("findPalette failed to return the well-known crayola-12 entry")
	}
}

func TestTraceAllLoopsSingleSquare(t *testing.T) {
	w, h := 3, 3
	mask := make([]bool, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mask[y*w+x] = x == 1 && y == 1
		}
	}

	loops := traceAllLoops(mask, w, h)
	if len(loops) != 1 {
		t.Fatalf("got %d loops, want 1", len(loops))
	}
	loop := loops[0]
	if len(loop) != 4 {
		t.Fatalf("loop has %d points, want 4 (the unit square's boundary)", len(loop))
	}
	for _, p := range loop {
		if p.x < 1 || p.x > 2 || p.y < 1 || p.y > 2 {
			t.Errorf("loop point %v is outside the expected unit square", p)
		}
	}
}

func TestTraceAllLoopsSquareWithHole(t *testing.T) {
	w, h := 5, 5
	mask := make([]bool, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mask[y*w+x] = x >= 1 && x <= 3 && y >= 1 && y <= 3 && !(x == 2 && y == 2)
		}
	}

	loops := traceAllLoops(mask, w, h)
	if len(loops) != 2 {
		t.Fatalf("got %d loops, want 2 (outer boundary + hole)", len(loops))
	}
}

func TestTraceAllLoopsDisconnectedComponents(t *testing.T) {
	w, h := 6, 6
	mask := make([]bool, w*h)
	// Two separate 1x1 squares with a white gap between them.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mask[y*w+x] = (x == 1 && y == 1) || (x == 4 && y == 4)
		}
	}

	loops := traceAllLoops(mask, w, h)
	if len(loops) != 2 {
		t.Fatalf("got %d loops, want 2 (one per component)", len(loops))
	}
}

func TestSimplifyCollinearRemovesIntermediatePoints(t *testing.T) {
	poly := []ipoint{{0, 0}, {1, 0}, {2, 0}, {2, 1}, {2, 2}, {1, 2}, {0, 2}, {0, 1}}
	simple, idx := simplifyCollinear(poly)
	if len(simple) != 4 {
		t.Fatalf("simplified polygon has %d vertices, want 4", len(simple))
	}
	if len(idx) != len(simple) {
		t.Errorf("index mapping has %d entries for %d vertices", len(idx), len(simple))
	}
}

func TestDetectCornersSquare(t *testing.T) {
	square := []ipoint{
		{1, 1}, {2, 1}, {3, 1}, {4, 1}, {5, 1},
		{5, 2}, {5, 3}, {5, 4}, {5, 5},
		{4, 5}, {3, 5}, {2, 5}, {1, 5},
		{1, 4}, {1, 3}, {1, 2},
	}
	simple, _ := simplifyCollinear(square)
	if len(simple) != 4 {
		t.Fatalf("simplified square has %d vertices, want 4", len(simple))
	}
	corners := detectCorners(simple)
	if len(corners) != 4 {
		t.Errorf("square has %d corners, want 4", len(corners))
	}
}

func TestDetectCornersDiagonalHasNoCorners(t *testing.T) {
	// A diagonal staircase approximates a straight line and must not be
	// chopped into corners (the run is a single smooth curve instead).
	diag := []ipoint{{0, 0}, {1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}}
	if corners := detectCorners(diag); len(corners) != 0 {
		t.Errorf("diagonal has %d corners, want 0", len(corners))
	}
}

func TestProcessImageVectorise(t *testing.T) {
	src := pngOf(4, 4, func(x, y int) bool { return x >= 1 && x <= 2 && y >= 1 && y <= 2 })
	processedPNG := base64.StdEncoding.EncodeToString(src)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"data:image/png;base64,%s"}}]}`, processedPNG)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.openRouterURL = fake.URL
	a.outputDir = t.TempDir()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("image", "photo.png")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	fw.Write(miniPNG)
	mw.WriteField("vectorise", "true")
	mw.Close()

	req := httptest.NewRequest("POST", "/api/v1/process", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	a.processImage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	var resp ProcessImageResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if !resp.Vectorised {
		t.Errorf("vectorised = false, want true")
	}
	if !strings.HasSuffix(resp.Filename, ".svg") {
		t.Errorf("filename = %q, want a .svg name", resp.Filename)
	}
	stored, err := os.ReadFile(filepath.Join(a.outputDir, resp.Filename))
	if err != nil {
		t.Fatalf("failed to read stored SVG: %v", err)
	}
	if !bytes.Contains(stored, []byte("<svg")) {
		t.Errorf("stored file is not an SVG document")
	}
}

func TestStoreResultKeepsMatchingExtension(t *testing.T) {
	a := newAPI(false, docsFS)
	a.outputDir = t.TempDir()

	name, err := a.storeResult(miniPNG, "result.png")
	if err != nil {
		t.Fatalf("storeResult returned error: %v", err)
	}
	if name != "result.png" {
		t.Errorf("stored name = %q, want result.png (unchanged)", name)
	}
	entries, _ := os.ReadDir(a.outputDir)
	if len(entries) != 1 {
		t.Errorf("output dir has %d files, want 1", len(entries))
	}
}

func TestStoreResultCorrectsExtensionToContent(t *testing.T) {
	a := newAPI(false, docsFS)
	a.outputDir = t.TempDir()

	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"><path d="M0 0"/></svg>`)

	// A previous bitmap version under the same base name is replaced by the
	// SVG version, keeping the base name but changing the extension.
	if err := os.WriteFile(filepath.Join(a.outputDir, "result.png"), miniPNG, 0o644); err != nil {
		t.Fatalf("failed to write previous version: %v", err)
	}
	name, err := a.storeResult(svg, "result.png")
	if err != nil {
		t.Fatalf("storeResult returned error: %v", err)
	}
	if name != "result.svg" {
		t.Errorf("stored name = %q, want result.svg (same base, new extension)", name)
	}
	if _, err := os.Stat(filepath.Join(a.outputDir, "result.png")); !os.IsNotExist(err) {
		t.Errorf("stale result.png was not removed")
	}
	stored, err := os.ReadFile(filepath.Join(a.outputDir, "result.svg"))
	if err != nil {
		t.Fatalf("failed to read stored SVG: %v", err)
	}
	if !bytes.Equal(stored, svg) {
		t.Errorf("stored bytes do not match the SVG content")
	}

	// And back again: an SVG output name with bitmap content is stored under
	// the bitmap extension, removing the stale SVG version.
	name, err = a.storeResult(miniPNG, "result.svg")
	if err != nil {
		t.Fatalf("storeResult returned error: %v", err)
	}
	if name != "result.png" {
		t.Errorf("stored name = %q, want result.png (same base, new extension)", name)
	}
	if _, err := os.Stat(filepath.Join(a.outputDir, "result.svg")); !os.IsNotExist(err) {
		t.Errorf("stale result.svg was not removed")
	}
	if _, err := os.Stat(filepath.Join(a.outputDir, "result.png")); err != nil {
		t.Errorf("bitmap version missing: %v", err)
	}
	entries, _ := os.ReadDir(a.outputDir)
	if len(entries) != 1 {
		t.Errorf("output dir has %d files, want 1 (old version replaced)", len(entries))
	}
}

func TestProcessImageVectoriseReconcilesExtension(t *testing.T) {
	src := pngOf(4, 4, func(x, y int) bool { return x >= 1 && x <= 2 && y >= 1 && y <= 2 })
	processedPNG := base64.StdEncoding.EncodeToString(src)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"data:image/png;base64,%s"}}]}`, processedPNG)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.openRouterURL = fake.URL
	a.outputDir = t.TempDir()

	// A previous bitmap version stored under the name the UI would resend.
	oldName := "my-image.jpg"
	if err := os.WriteFile(filepath.Join(a.outputDir, oldName), miniPNG, 0o644); err != nil {
		t.Fatalf("failed to write previous version: %v", err)
	}

	payload := fmt.Sprintf(`{"image":"data:image/png;base64,%s","output":%q,"vectorise":true}`,
		base64.StdEncoding.EncodeToString(miniPNG), oldName)
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
	if resp.Filename != "my-image.svg" {
		t.Errorf("filename = %q, want my-image.svg (same base, new extension)", resp.Filename)
	}
	if !resp.Vectorised {
		t.Errorf("vectorised = false, want true")
	}
	if _, err := os.Stat(filepath.Join(a.outputDir, oldName)); !os.IsNotExist(err) {
		t.Errorf("stale %s was not removed", oldName)
	}
	stored, err := os.ReadFile(filepath.Join(a.outputDir, "my-image.svg"))
	if err != nil {
		t.Fatalf("failed to read stored SVG: %v", err)
	}
	if !bytes.Contains(stored, []byte("<svg")) {
		t.Errorf("stored file is not an SVG document")
	}
}

func TestProcessImageVectoriseJSONFlag(t *testing.T) {
	src := pngOf(4, 4, func(x, y int) bool { return x >= 1 && x <= 2 && y >= 1 && y <= 2 })
	processedPNG := base64.StdEncoding.EncodeToString(src)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"data:image/png;base64,%s"}}]}`, processedPNG)
	}))
	defer fake.Close()

	a := newAPI(false, docsFS)
	a.openRouterKey = "test-key"
	a.openRouterURL = fake.URL
	a.outputDir = t.TempDir()

	payload := fmt.Sprintf(`{"image":"data:image/png;base64,%s","vectorise":true}`,
		base64.StdEncoding.EncodeToString(miniPNG))
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
	if !resp.Vectorised {
		t.Errorf("vectorised = false, want true")
	}
	if !strings.HasSuffix(resp.Filename, ".svg") {
		t.Errorf("filename = %q, want a .svg name", resp.Filename)
	}
}
