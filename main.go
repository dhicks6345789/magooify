package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"log"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

//go:embed all:ui
var uiFS embed.FS

//go:embed all:docs
var docsFS embed.FS

//go:embed PROMPT.md
var promptMD string

// registerMimeTypes pins deterministic Content-Type headers for embedded
// assets so the UI and docs work even on hosts without a system MIME database.
func registerMimeTypes() {
	mime.AddExtensionType(".html", "text/html; charset=utf-8")
	mime.AddExtensionType(".htm", "text/html; charset=utf-8")
	mime.AddExtensionType(".css", "text/css; charset=utf-8")
	mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8")
	mime.AddExtensionType(".json", "application/json")
	mime.AddExtensionType(".map", "application/json")
	mime.AddExtensionType(".svg", "image/svg+xml")
	mime.AddExtensionType(".png", "image/png")
	mime.AddExtensionType(".jpg", "image/jpeg")
	mime.AddExtensionType(".jpeg", "image/jpeg")
	mime.AddExtensionType(".gif", "image/gif")
	mime.AddExtensionType(".webp", "image/webp")
	mime.AddExtensionType(".ico", "image/x-icon")
	mime.AddExtensionType(".woff", "font/woff")
	mime.AddExtensionType(".woff2", "font/woff2")
	mime.AddExtensionType(".ttf", "font/ttf")
	mime.AddExtensionType(".eot", "application/vnd.ms-fontobject")
	mime.AddExtensionType(".txt", "text/plain; charset=utf-8")
	mime.AddExtensionType(".xml", "text/xml; charset=utf-8")
	mime.AddExtensionType(".wasm", "application/wasm")
	mime.AddExtensionType(".pdf", "application/pdf")
}

func main() {
	registerMimeTypes()

	defaultPort := getEnv("PORT", "8080")
	defaultMode := getEnv("APP_MODE", "desktop")
	defaultHost := getEnv("HOST", "")

	port := flag.String("port", defaultPort, "Port to listen on")
	mode := flag.String("mode", defaultMode, "Operation mode: 'desktop' or 'server'")
	host := flag.String("host", defaultHost, "Host IP to bind to (defaults to 127.0.0.1 for desktop, 0.0.0.0 for server)")
	basePath := flag.String("base-path", getEnv("BASE_PATH", ""), "Comma-separated URL prefixes to serve under when mounted behind a reverse proxy at sub-paths (e.g. /magooify); the app is always also served from the site root")
	noBrowser := flag.Bool("no-browser", false, "Disable automatic browser launch in desktop mode")
	genDocs := flag.String("gen-docs", "", "Write the Swagger UI documentation page to the given path and exit")
	openRouterKey := flag.String("openrouter-key", getEnv("OPENROUTER_API_KEY", ""), "OpenRouter API key used to process captured images")
	managementKey := flag.String("openrouter-management-key", getEnv("OPENROUTER_MANAGEMENT_KEY", ""), "OpenRouter management key used to query account credits; cannot process images")
	outputDir := flag.String("output-dir", getEnv("OUTPUT_DIR", defaultOutputDir), "Directory where processed images and their descriptions are stored")
	model := flag.String("model", getEnv("OPENROUTER_MODEL", defaultModel), "OpenRouter model used to process images")
	promptFile := flag.String("prompt-file", getEnv("PROMPT_FILE", ""), "Path to a file whose text is sent to the model with each image, instead of the embedded PROMPT.md")
	flag.Parse()

	// Offline documentation generation (used by the build script to produce docs/api.html).
	if *genDocs != "" {
		if err := os.WriteFile(*genDocs, swaggerUIHTML(), 0o644); err != nil {
			log.Fatalf("Failed to write docs to %s: %v", *genDocs, err)
		}
		log.Printf("Wrote API documentation to %s", *genDocs)
		return
	}

	isServerMode := strings.ToLower(*mode) == "server"

	bindHost := *host
	if bindHost == "" {
		if isServerMode {
			bindHost = "0.0.0.0"
		} else {
			bindHost = "127.0.0.1"
		}
	}

	addr := net.JoinHostPort(bindHost, *port)
	basePaths := parseBasePaths(*basePath)

	a := newAPI(isServerMode, docsFS)
	a.openRouterKey = *openRouterKey
	a.managementKey = *managementKey
	a.outputDir = *outputDir
	a.model = *model
	a.promptFile = *promptFile

	handler := buildHandler(a, basePaths)

	user := a.getUser(&http.Request{})

	log.Printf("==================================================")
	log.Printf("Magooify Starting")
	log.Printf("Mode      : %s", strings.ToUpper(user.Mode))
	log.Printf("User      : %s (%s)", user.Username, user.AuthType)
	log.Printf("Listening : http://%s", addr)
	if a.openRouterKey != "" {
		log.Printf("OpenRouter: configured (model %s)", a.model)
	} else {
		log.Printf("OpenRouter: NOT configured (use -openrouter-key)")
	}
	if a.managementKey != "" {
		log.Printf("Credits   : management key configured (use -openrouter-management-key to change)")
	} else {
		log.Printf("Credits   : NOT configured (use -openrouter-management-key)")
	}
	log.Printf("Output dir: %s", a.outputDir)
	if a.promptFile != "" {
		log.Printf("Prompt file: %s", a.promptFile)
	} else {
		log.Printf("Prompt file: embedded PROMPT.md")
	}
	for _, bp := range basePaths {
		log.Printf("Base path : %s", bp)
		log.Printf("UI        : http://%s%s/", addr, bp)
		log.Printf("OpenAPI   : http://%s%s/docs/api", addr, bp)
	}
	if len(basePaths) == 0 {
		log.Printf("UI        : http://%s/", addr)
		log.Printf("OpenAPI   : http://%s/docs/api", addr)
	}
	log.Printf("==================================================")

	// Auto-launch web browser in desktop mode
	if !isServerMode && !*noBrowser {
		targetURL := fmt.Sprintf("http://127.0.0.1:%s/", *port)
		go func() {
			log.Printf("Launching browser targeting %s...", targetURL)
			if err := openBrowser(targetURL); err != nil {
				log.Printf("Note: Could not open web browser automatically: %v", err)
			}
		}()
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", addr, err)
	}

	if err := http.Serve(listener, handler); err != nil {
		log.Fatalf("Server stopped with error: %v", err)
	}
}

// parseBasePaths splits a comma-separated list of base paths (from the
// --base-path flag or BASE_PATH env var) and normalizes each entry.
func parseBasePaths(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if bp := normalizeBasePath(part); bp != "" {
			out = append(out, bp)
		}
	}
	return out
}

// basePathHandler wraps the application handler so it can also be served from
// one or more reverse-proxy sub-paths (e.g. /magooify) without the proxy
// needing to strip the prefix. Requests arriving with one of those path
// prefixes have the prefix stripped before they reach the application routes.
// A request whose path is exactly a prefix (no trailing slash) is redirected
// to the trailing-slash form so the page's relative links resolve against the
// sub-path rather than the site root. Requests without a known prefix (the
// site root, or a proxy that strips the prefix itself) are passed through
// unchanged, so the app is always also served from its default location.
func basePathHandler(basePaths []string, next http.Handler) http.Handler {
	paths := make([]string, 0, len(basePaths))
	for _, bp := range basePaths {
		if bp = normalizeBasePath(bp); bp != "" {
			paths = append(paths, bp)
		}
	}
	if len(paths) == 0 {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path

		for _, basePath := range paths {
			var stripped string

			switch {
			case p == basePath:
				// Redirect to the trailing-slash form so the browser resolves
				// relative URLs (vendor/, style.css, app.js, api/...) against
				// the base path instead of the site root.
				location := basePath + "/"
				if r.URL.RawQuery != "" {
					location += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, location, http.StatusMovedPermanently)
				return
			case p == basePath+"/":
				stripped = "/"
			case strings.HasPrefix(p, basePath+"/"):
				stripped = strings.TrimPrefix(p, basePath)
			default:
				continue
			}

			r2 := r.Clone(r.Context())
			r2.URL = new(url.URL)
			*r2.URL = *r.URL
			r2.URL.Path = stripped
			r2.URL.RawPath = ""
			next.ServeHTTP(w, r2)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// buildHandler wires up all HTTP routes for the application. The app is always
// served from the site root; the optional basePaths let it additionally be
// served from reverse-proxy sub-paths such as /magooify when the proxy passes
// the full path through.
func buildHandler(a *api, basePaths []string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health", a.health)
	mux.HandleFunc("/api/v1/user", a.user)
	mux.HandleFunc("/api/v1/info", a.info)
	mux.HandleFunc("/api/v1/prompt", a.prompt)
	mux.HandleFunc("GET /api/v1/items", a.listItems)
	mux.HandleFunc("POST /api/v1/items", a.createItem)
	mux.HandleFunc("POST /api/v1/process", a.processImage)
	mux.HandleFunc("GET /api/v1/credits", a.credits)
	mux.HandleFunc("GET /api/v1/models", a.models)
	mux.HandleFunc("PUT /api/v1/model", a.setModel)
	mux.HandleFunc("GET /api/v1/images", a.listImages)
	mux.HandleFunc("GET /api/v1/images/{filename}", a.getImage)

	mux.HandleFunc("/docs/api", a.serveDocs)
	mux.HandleFunc("/docs/", a.serveDocs)

	subFS, err := fs.Sub(uiFS, "ui")
	if err != nil {
		log.Fatalf("Failed to initialize embedded UI filesystem: %v", err)
	}
	fileServer := http.FileServer(http.FS(subFS))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") || strings.HasPrefix(r.URL.Path, "/docs") {
			http.NotFound(w, r)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" && path != "index.html" {
			f, err := subFS.Open(path)
			if err == nil {
				f.Close()
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// A stripping proxy forwards a bare prefix such as "/magooify" as an
		// empty path. Redirect to the trailing-slash form so the browser loads
		// the index document from a URL ending in "/". Only this case is
		// redirected: ordinary resource requests like /magooify/app.js are
		// served as-is, never bounced to a trailing slash.
		if r.URL.Path == "" {
			if loc, ok := indexRedirectLocation(r); ok {
				http.Redirect(w, r, loc, http.StatusMovedPermanently)
				return
			}
		}

		// SPA fallback to index.html. The page uses plain relative links, so
		// resources resolve against the document URL automatically, whether the
		// app is served from the site root or a reverse-proxy sub-path.
		serveIndex(w, r, subFS)
	})

	return basePathHandler(basePaths, mux)
}

// indexRedirectLocation returns the Location header for a 301 that adds a
// trailing slash when a stripping proxy (Traefik/Pangolin) forwards a bare
// prefix such as "/magooify" as an empty path. ok is false when the request
// does not need a redirect. The prefix the proxy reports via
// X-Forwarded-Prefix is re-attached so the redirect points at the external
// URL the browser sees; otherwise it falls back to the site root.
func indexRedirectLocation(r *http.Request) (string, bool) {
	if r.URL.Path != "" {
		return "", false
	}

	prefix := safeForwardedPrefix(r.Header.Get("X-Forwarded-Prefix"))
	if prefix == "" {
		return "/", true
	}
	return prefix + "/", true
}

// safeForwardedPrefix validates a proxy-supplied X-Forwarded-Prefix header
// before it is trusted for building a redirect URL. The header is attacker
// controllable on misconfigured proxy chains, so only a safe path-absolute
// value such as "/magooify" is accepted; anything that could turn the redirect
// into an open redirect (protocol-relative, dot segments, query/fragment
// characters) is rejected.
func safeForwardedPrefix(val string) string {
	val = strings.TrimSpace(val)
	if val == "" || !strings.HasPrefix(val, "/") || strings.HasPrefix(val, "//") {
		return ""
	}
	if strings.ContainsAny(val, "\\?#\r\n") || strings.Contains(val, "..") {
		return ""
	}
	return normalizeBasePath(val)
}

// normalizeBasePath cleans a reported reverse-proxy prefix so it can be used
// in a redirect URL. An empty value (or "/") means the site root.
func normalizeBasePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimSuffix(p, "/")
}

// serveIndex writes the embedded index.html. The page uses only plain
// relative links, so no <base> tag is needed: resources resolve against the
// document URL, which keeps them working under any reverse-proxy sub-path as
// long as the request URL carries a trailing slash.
func serveIndex(w http.ResponseWriter, r *http.Request, subFS fs.FS) {
	data, err := fs.ReadFile(subFS, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Never cache the page so a stale copy can't linger after redeploys.
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func getEnv(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

// openBrowser launches the system's default web browser at the given URL.
func openBrowser(url string) error {
	// Give the server a moment to start listening.
	time.Sleep(100 * time.Millisecond)

	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	return cmd.Start()
}

// ---------------------------------------------------------------------------
// Bitmap vectorisation
//
// The functions below trace the dark (ink) pixels of a processed bitmap into
// closed vector paths and render them as an SVG document, all using only the
// Go standard library. The pipeline is: decode the bitmap, threshold it into a
// binary mask, extract the connected components, follow each component's
// boundary (black kept on the left of the walk) to get closed polygons, detect
// the genuine corners of each polygon, fit smooth cubic bezier segments
// between corners, and finally write the SVG path data.
// ---------------------------------------------------------------------------

// vectoriseThreshold is the luminance below which a pixel is treated as ink
// and traced into a vector path. 0.5 suits the clean black-on-white output
// produced by the processing prompt.
const vectoriseThreshold = 0.5

// vectoriseMaxFitError is the largest distance (in pixels) a fitted bezier
// curve may stray from the traced boundary before the fit is abandoned in
// favour of a plain polygon segment.
const vectoriseMaxFitError = 1.5

// fpoint is a floating-point grid point used while fitting curves.
type fpoint struct{ x, y float64 }

// vectoriseBitmap converts a processed bitmap into an SVG document tracing the
// ink pixels into smooth vector paths. Bitmap formats supported by the
// standard library (PNG, JPEG, GIF) are accepted; an image that is already
// vector (an SVG document) is passed through unchanged. The returned document
// has a transparent background with the traced ink filled black, sized to the
// source image.
func vectoriseBitmap(imgBytes []byte) ([]byte, error) {
	trimmed := bytes.TrimLeft(imgBytes, " \t\r\n")
	if bytes.HasPrefix(trimmed, []byte("<svg")) ||
		(bytes.HasPrefix(trimmed, []byte("<?xml")) && bytes.Contains(trimmed, []byte("<svg"))) {
		return imgBytes, nil
	}

	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("unsupported image format (%v); PNG and JPEG output can be vectorised", err)
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	mask := make([]bool, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			mask[y*w+x] = pixelIsInk(img, b.Min.X+x, b.Min.Y+y)
		}
	}

	return loopsToSVG(traceAllLoops(mask, w, h), w, h), nil
}

// pixelIsInk reports whether the pixel at (x, y) counts as ink: dark enough
// (below vectoriseThreshold) and sufficiently opaque. Transparent pixels are
// treated as background so alpha-matted scans don't produce stray outlines.
func pixelIsInk(img image.Image, x, y int) bool {
	r, g, bl, a := img.At(x, y).RGBA()
	if a < 0x8000 {
		return false
	}
	lum := 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(bl)
	return lum/65535.0 < vectoriseThreshold
}

// ipoint is an integer grid point on the boundary grid. Boundary paths move
// along the edges between pixels, so their points live in [0,W]x[0,H] while
// pixel centres live in [0,W)x[0,H).
type ipoint struct{ x, y int }

// Direction indexing for the boundary walk. The order E,N,W,S is also the
// left-turn order: turning left from E faces N, from N faces W, and so on.
const (
	dirE = iota
	dirN
	dirW
	dirS
)

var dirStep = [4]ipoint{{1, 0}, {0, -1}, {-1, 0}, {0, 1}}

// edgeKey identifies one directed boundary edge: an edge leaving point from in
// the given direction, with the ink always on the left of the walk.
type edgeKey struct {
	from ipoint
	dir  int
}

// traceAllLoops extracts the closed boundary polygons of every ink component
// in the mask. Components are found with a 4-connected flood fill; each one is
// traced separately, producing one loop for its outer boundary and one for
// each of its holes. Holes are later punched out of the fill using the
// even-odd rule, so their winding direction does not matter.
func traceAllLoops(mask []bool, w, h int) [][]ipoint {
	visited := make([]bool, w*h)
	var loops [][]ipoint
	neighbours := [4]ipoint{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !mask[y*w+x] || visited[y*w+x] {
				continue
			}
			// Flood fill the 4-connected component of ink pixels.
			var comp []ipoint
			queue := []ipoint{{x, y}}
			visited[y*w+x] = true
			for len(queue) > 0 {
				p := queue[0]
				queue = queue[1:]
				comp = append(comp, p)
				for _, nb := range neighbours {
					nx, ny := p.x+nb.x, p.y+nb.y
					if nx < 0 || ny < 0 || nx >= w || ny >= h {
						continue
					}
					if mask[ny*w+nx] && !visited[ny*w+nx] {
						visited[ny*w+nx] = true
						queue = append(queue, ipoint{nx, ny})
					}
				}
			}
			loops = append(loops, traceComponentLoops(mask, w, h, comp)...)
		}
	}
	return loops
}

// traceComponentLoops builds the directed boundary edges of a single ink
// component and chains them into closed loops. Each black pixel contributes an
// edge on any side whose neighbouring cell is white (or outside the image),
// oriented so the ink stays on the left of the walk. The walk picks, at every
// grid point, the leftmost unused continuation edge, which keeps it hugging
// the component's boundary and returns it to its start.
func traceComponentLoops(mask []bool, w, h int, comp []ipoint) [][]ipoint {
	isWhite := func(x, y int) bool {
		if x < 0 || y < 0 || x >= w || y >= h {
			return true
		}
		return !mask[y*w+x]
	}

	edges := map[ipoint][]int{}
	for _, p := range comp {
		if isWhite(p.x, p.y-1) {
			edges[ipoint{p.x + 1, p.y}] = append(edges[ipoint{p.x + 1, p.y}], dirW)
		}
		if isWhite(p.x, p.y+1) {
			edges[ipoint{p.x, p.y + 1}] = append(edges[ipoint{p.x, p.y + 1}], dirE)
		}
		if isWhite(p.x-1, p.y) {
			edges[p] = append(edges[p], dirS)
		}
		if isWhite(p.x+1, p.y) {
			edges[ipoint{p.x + 1, p.y + 1}] = append(edges[ipoint{p.x + 1, p.y + 1}], dirN)
		}
	}

	used := map[edgeKey]bool{}
	var loops [][]ipoint
	for from, dirs := range edges {
		for _, d := range dirs {
			if used[edgeKey{from, d}] {
				continue
			}
			loop := []ipoint{}
			cur, dir := from, d
			for {
				loop = append(loop, cur)
				used[edgeKey{cur, dir}] = true
				nxt := ipoint{cur.x + dirStep[dir].x, cur.y + dirStep[dir].y}
				if nxt == from {
					break
				}
				nextDir := -1
				for k := 0; k < 4; k++ {
					cand := (dir + k) % 4
					if !used[edgeKey{nxt, cand}] && containsInt(edges[nxt], cand) {
						nextDir = cand
						break
					}
				}
				if nextDir == -1 {
					break
				}
				cur, dir = nxt, nextDir
			}
			if len(loop) >= 4 {
				loops = append(loops, loop)
			}
		}
	}
	return loops
}

// containsInt reports whether list contains want.
func containsInt(list []int, want int) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// simplifyCollinear removes vertices whose two adjacent edges are collinear
// (straight through the vertex), returning the simplified polygon and the
// index of each kept vertex in the original polygon.
func simplifyCollinear(poly []ipoint) ([]ipoint, []int) {
	n := len(poly)
	if n <= 3 {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		return append([]ipoint(nil), poly...), idx
	}

	var kept []ipoint
	var idx []int
	for i := 0; i < n; i++ {
		prev := poly[(i-1+n)%n]
		cur := poly[i]
		next := poly[(i+1)%n]
		if (cur.x-prev.x)*(next.y-cur.y) == (cur.y-prev.y)*(next.x-cur.x) {
			continue
		}
		kept = append(kept, cur)
		idx = append(idx, i)
	}
	if len(kept) < 3 {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		return append([]ipoint(nil), poly...), idx
	}
	return kept, idx
}

// turnSign returns +1 for a turn to one side, -1 for a turn to the other, and
// 0 for a straight-through vertex, based on the cross product of the two edge
// vectors at cur. Only the relative sign matters, not which side is "left".
func turnSign(prev, cur, next ipoint) int {
	cross := (cur.x-prev.x)*(next.y-cur.y) - (cur.y-prev.y)*(next.x-cur.x)
	if cross > 0 {
		return 1
	}
	if cross < 0 {
		return -1
	}
	return 0
}

// detectCorners returns the set of corner vertices of a collinear-simplified
// closed polygon. After simplification every vertex is a genuine turn; the
// goal is to tell real geometric corners apart from the alternating left/right
// turns of a staircase (which approximates a smooth diagonal or arc). A vertex
// is a corner when it belongs to a run of two or more consecutive same-side
// turns, or when it is an isolated turn flanked on both sides by such runs.
// Purely alternating staircase runs contain no corners, so the arc they
// approximate is smoothed as one curve.
func detectCorners(poly []ipoint) map[int]bool {
	n := len(poly)
	corners := map[int]bool{}
	if n < 4 {
		return corners
	}

	turn := make([]int, n)
	for i := 0; i < n; i++ {
		turn[i] = turnSign(poly[(i-1+n)%n], poly[i], poly[(i+1)%n])
	}

	// runLen[i] is the length of the maximal cyclic run of same-sign turns
	// containing vertex i. The run is capped at the polygon size so a polygon
	// whose turns all share one sign (every vertex a corner, as on a convex
	// ring) gets a run length of n rather than wrapping onto itself.
	runLen := make([]int, n)
	for i := 0; i < n; i++ {
		if turn[i] == 0 {
			continue
		}
		fwd := 1
		for j := (i + 1) % n; turn[j] == turn[i] && fwd < n; j = (j + 1) % n {
			fwd++
		}
		bwd := 0
		for j := (i - 1 + n) % n; turn[j] == turn[i] && fwd+bwd < n; j = (j - 1 + n) % n {
			bwd++
		}
		runLen[i] = fwd + bwd
	}

	for i := 0; i < n; i++ {
		if turn[i] == 0 {
			continue
		}
		if runLen[i] >= 2 {
			corners[i] = true
			continue
		}
		if runLen[(i-1+n)%n] >= 2 && runLen[(i+1)%n] >= 2 {
			corners[i] = true
		}
	}
	return corners
}

// fitCubicBezier fits a cubic bezier through the sample points with the first
// and last sample fixed as the curve endpoints, using least squares with
// chord-length parameterisation. The two control points and ok (true when at
// least three samples exist and the fit stays within maxErr of every sample)
// are returned.
func fitCubicBezier(samples []ipoint, maxErr float64) (fpoint, fpoint, bool) {
	n := len(samples)
	if n < 3 {
		return fpoint{}, fpoint{}, false
	}
	p0 := fpoint{float64(samples[0].x), float64(samples[0].y)}
	p3 := fpoint{float64(samples[n-1].x), float64(samples[n-1].y)}

	chord := make([]float64, n)
	total := 0.0
	for i := 1; i < n; i++ {
		chord[i] = math.Hypot(float64(samples[i].x-samples[i-1].x), float64(samples[i].y-samples[i-1].y))
		total += chord[i]
	}
	if total == 0 {
		return fpoint{}, fpoint{}, false
	}

	var a11, a12, a22, bx1, bx2, by1, by2 float64
	ts := make([]float64, n)
	t := 0.0
	for i := 0; i < n; i++ {
		if i > 0 {
			t += chord[i] / total
		}
		ts[i] = t
		a1 := 3 * (1 - t) * (1 - t) * t
		a2 := 3 * (1 - t) * t * t
		ex := float64(samples[i].x) - (1-t)*(1-t)*(1-t)*p0.x - t*t*t*p3.x
		ey := float64(samples[i].y) - (1-t)*(1-t)*(1-t)*p0.y - t*t*t*p3.y
		a11 += a1 * a1
		a12 += a1 * a2
		a22 += a2 * a2
		bx1 += a1 * ex
		bx2 += a2 * ex
		by1 += a1 * ey
		by2 += a2 * ey
	}

	det := a11*a22 - a12*a12
	if math.Abs(det) < 1e-12 {
		return fpoint{}, fpoint{}, false
	}
	c1 := fpoint{(bx1*a22 - bx2*a12) / det, (by1*a22 - by2*a12) / det}
	c2 := fpoint{(a11*bx2 - a12*bx1) / det, (a11*by2 - a12*by1) / det}

	for i := 0; i < n; i++ {
		u := ts[i]
		w0 := (1 - u) * (1 - u) * (1 - u)
		w1 := 3 * (1 - u) * (1 - u) * u
		w2 := 3 * (1 - u) * u * u
		w3 := u * u * u
		bx := w0*p0.x + w1*c1.x + w2*c2.x + w3*p3.x
		by := w0*p0.y + w1*c1.y + w2*c2.y + w3*p3.y
		if math.Hypot(bx-float64(samples[i].x), by-float64(samples[i].y)) > maxErr {
			return fpoint{}, fpoint{}, false
		}
	}
	return c1, c2, true
}

// denseRange returns the closed sequence of polygon points from begin to end
// (inclusive), following the polygon's winding order and wrapping around the
// end.
func denseRange(poly []ipoint, begin, end int) []ipoint {
	var out []ipoint
	i := begin
	for {
		out = append(out, poly[i])
		if i == end {
			return out
		}
		i = (i + 1) % len(poly)
	}
}

// loopPathData builds the SVG path data for a single traced loop: one M move
// followed by cubic bezier (C) segments between the loop's corners and plain
// line (L) segments where a fit would overshoot or the segment is straight.
func loopPathData(loop []ipoint) string {
	n := len(loop)
	if n < 3 {
		return ""
	}

	simple, origIdx := simplifyCollinear(loop)
	corners := detectCorners(simple)

	var cornerIdx []int
	for i := range simple {
		if corners[i] {
			cornerIdx = append(cornerIdx, origIdx[i])
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "M %d %d", loop[0].x, loop[0].y)

	if len(cornerIdx) == 0 {
		// No corners: the whole loop is one smooth curve (or a degenerate
		// polyline). Emit the traced boundary as-is rather than risk an
		// over-eager single-curve fit.
		for i := 1; i < n; i++ {
			fmt.Fprintf(&b, " L %d %d", loop[i].x, loop[i].y)
		}
		b.WriteString(" Z")
		return b.String()
	}

	for k := range cornerIdx {
		begin := cornerIdx[k]
		end := cornerIdx[(k+1)%len(cornerIdx)]
		samples := denseRange(loop, begin, end)
		if c1, c2, ok := fitCubicBezier(samples, vectoriseMaxFitError); ok {
			fmt.Fprintf(&b, " C %d %d %d %d %d %d",
				roundCoord(c1.x), roundCoord(c1.y), roundCoord(c2.x), roundCoord(c2.y),
				loop[end].x, loop[end].y)
			continue
		}
		if len(samples) == 2 {
			fmt.Fprintf(&b, " L %d %d", loop[end].x, loop[end].y)
			continue
		}
		for i := 1; i < len(samples); i++ {
			fmt.Fprintf(&b, " L %d %d", samples[i].x, samples[i].y)
		}
	}
	b.WriteString(" Z")
	return b.String()
}

// roundCoord rounds a fitted coordinate to the nearest integer for compact,
// deterministic SVG output.
func roundCoord(v float64) int {
	return int(math.Round(v))
}

// loopsToSVG assembles the traced loops into an SVG document. All loops are
// combined into one path filled with the even-odd rule, so holes and nested
// shapes are rendered correctly and the document stays small.
func loopsToSVG(loops [][]ipoint, w, h int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`+"\n", w, h, w, h)
	if len(loops) > 0 {
		b.WriteString("  <path d=\"")
		for _, loop := range loops {
			b.WriteString(loopPathData(loop))
		}
		b.WriteString("\" fill=\"#000000\" fill-rule=\"evenodd\"/>\n")
	}
	b.WriteString("</svg>\n")
	return []byte(b.String())
}
