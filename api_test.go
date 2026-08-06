package main

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
		{"/", http.StatusOK, "text/html", "Go Self-Contained App"},
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
		if tc.wantHTML && !strings.Contains(rr.Body.String(), "Go Self-Contained App") {
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
		{"/magooify/", http.StatusOK, "text/html", "Go Self-Contained App"},
		{"/magooify/style.css", http.StatusOK, "text/css", ".brand-icon"},
		{"/magooify/vendor/bootstrap/bootstrap.min.css", http.StatusOK, "text/css", "bootstrap"},
		{"/magooify/app.js", http.StatusOK, "text/javascript", "DOMContentLoaded"},
		{"/magooify/api/v1/user", http.StatusOK, "application/json", `"auth_type"`},
		{"/magooify/api/v1/health", http.StatusOK, "application/json", `"ok"`},
		{"/magooify/docs/api", http.StatusOK, "text/html", "swagger-ui"},
		{"/magooify/docs/swagger.json", http.StatusOK, "application/json", `"swagger"`},
		{"/magooify/docs/swagger-ui/swagger-ui.css", http.StatusOK, "text/css", "swagger-ui"},
		// The site root keeps working alongside the base path.
		{"/", http.StatusOK, "text/html", "Go Self-Contained App"},
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
