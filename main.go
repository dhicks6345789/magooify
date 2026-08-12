package main

import (
	"bytes"
	"embed"
	"encoding/binary"
	"encoding/xml"
	"flag"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/color"
	"image/png"
	"io/fs"
	"log"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
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
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
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

// svgNode is a generic SVG element tree used to rebuild the logo without
// Inkscape-only namespaces, generated ids and redundant style defaults.
type svgNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr `xml:",any,attr"`
	Children []svgNode  `xml:",any"`
}

// cleanLogoSVG strips Inkscape-specific parts from the SVG logo file at path,
// leaving a plain, standard SVG that renders in all browsers. The file is
// rewritten in place.
func cleanLogoSVG(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var root svgNode
	if err := xml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	cleanSVGNode(&root)

	out, err := xml.MarshalIndent(&root, "", "  ")
	if err != nil {
		return err
	}
	out = append([]byte(xml.Header), out...)
	out = append(out, '\n')

	return os.WriteFile(path, out, 0o644)
}

// cleanSVGNode recursively removes Inkscape/Sodipodi namespaced attributes and
// elements, Inkscape-generated ids, and collapses verbose style attributes
// down to plain fill/stroke attributes.
func cleanSVGNode(n *svgNode) {
	// Drop the resolved default namespace: the root keeps its plain xmlns
	// attribute, so re-marshaling emits unprefixed elements without adding
	// redundant xmlns declarations to every node.
	n.XMLName.Space = ""

	var attrs []xml.Attr
	for _, a := range n.Attrs {
		switch {
		case a.Name.Space == "inkscape" || a.Name.Space == "sodipodi":
			continue
		case a.Name.Space == "" && a.Name.Local == "id":
			continue
		case a.Name.Space == "" && a.Name.Local == "style":
			for _, prop := range splitStyleProps(a.Value) {
				if (prop.name == "fill" || prop.name == "stroke") && prop.value != "" && prop.value != "none" {
					attrs = append(attrs, xml.Attr{Name: xml.Name{Local: prop.name}, Value: prop.value})
				}
			}
			continue
		}
		attrs = append(attrs, a)
	}
	n.Attrs = attrs

	var children []svgNode
	for i := range n.Children {
		c := &n.Children[i]
		if c.XMLName.Space == "inkscape" || c.XMLName.Space == "sodipodi" ||
			c.XMLName.Local == "metadata" ||
			c.XMLName.Local == "namedview" ||
			(c.XMLName.Local == "defs" && len(c.Children) == 0) {
			continue
		}
		cleanSVGNode(c)
		children = append(children, *c)
	}
	n.Children = children
}

// styleProp is a single property from a CSS style attribute.
type styleProp struct {
	name  string
	value string
}

// splitStyleProps parses a style attribute such as
// "fill:#ffff00;fill-opacity:1;stroke:none" into its name/value pairs.
func splitStyleProps(style string) []styleProp {
	var props []styleProp
	for _, part := range strings.Split(style, ";") {
		name, value, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		props = append(props, styleProp{name: strings.TrimSpace(name), value: strings.TrimSpace(value)})
	}
	return props
}

// faviconCanvasW and faviconCanvasH are the dimensions of the logo's SVG
// viewBox; favicon renders fit the logo inside a square while keeping the
// original 5:6 aspect ratio, matching the browser's default SVG scaling.
const (
	faviconCanvasW = 250.0
	faviconCanvasH = 300.0
)

// faviconShape is one filled path of the logo, flattened into closed polygons
// ready for point-in-polygon rasterisation.
type faviconShape struct {
	color    color.NRGBA
	polygons [][]fpoint
}

// generateFavicons renders the UI logo into a set of favicon files for a
// range of platforms and writes them into dir (the embedded UI directory):
// favicon.ico (16/32/48), favicon-{16,32,48}x{n}.png, apple-touch-icon.png,
// favicon.svg and site.webmanifest.
func generateFavicons(dir string) error {
	logoPath := filepath.Join(dir, "logo.svg")
	data, err := os.ReadFile(logoPath)
	if err != nil {
		return err
	}

	shapes, err := parseFaviconShapes(data)
	if err != nil {
		return err
	}

	writeFile := func(name string, content []byte) error {
		return os.WriteFile(filepath.Join(dir, name), content, 0o644)
	}

	var icoPNGs [][]byte
	var icoSizes []int
	for _, size := range []int{16, 32, 48, 180} {
		pngBytes, err := renderFaviconPNG(shapes, size)
		if err != nil {
			return err
		}
		if size == 180 {
			if err := writeFile("apple-touch-icon.png", pngBytes); err != nil {
				return err
			}
			continue
		}
		if err := writeFile(fmt.Sprintf("favicon-%dx%d.png", size, size), pngBytes); err != nil {
			return err
		}
		icoPNGs = append(icoPNGs, pngBytes)
		icoSizes = append(icoSizes, size)
	}

	ico, err := encodeICO(icoPNGs, icoSizes)
	if err != nil {
		return err
	}
	if err := writeFile("favicon.ico", ico); err != nil {
		return err
	}

	// The SVG favicon is simply the cleaned logo itself.
	if err := writeFile("favicon.svg", data); err != nil {
		return err
	}

	return writeFile("site.webmanifest", buildFaviconManifest())
}

// parseFaviconShapes parses the logo SVG into flattened filled shapes. Only
// the plain SVG features the logo uses are supported: translate transforms,
// and paths with fill (attribute or style) built from M/L/H/V/C/Z commands.
func parseFaviconShapes(data []byte) ([]faviconShape, error) {
	var root svgNode
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, err
	}

	var shapes []faviconShape
	if err := collectFaviconShapes(&root, 0, 0, &shapes); err != nil {
		return nil, err
	}
	return shapes, nil
}

// collectFaviconShapes walks the SVG tree accumulating translate transforms
// and turning each filled <path> into a faviconShape.
func collectFaviconShapes(n *svgNode, tx, ty float64, shapes *[]faviconShape) error {
	tx, ty = faviconTransform(n, tx, ty)

	if n.XMLName.Local == "path" {
		fill, ok := nodeFill(n)
		if ok {
			polys, err := parseFaviconPathData(nodeAttr(n, "d"), tx, ty)
			if err != nil {
				return fmt.Errorf("path %q: %w", nodeAttr(n, "id"), err)
			}
			if len(polys) > 0 {
				*shapes = append(*shapes, faviconShape{color: fill, polygons: polys})
			}
		}
	}

	for i := range n.Children {
		if err := collectFaviconShapes(&n.Children[i], tx, ty, shapes); err != nil {
			return err
		}
	}
	return nil
}

// faviconTransform adds any translate() on the node to the running transform.
// Other transforms are ignored; the logo does not use them.
func faviconTransform(n *svgNode, tx, ty float64) (float64, float64) {
	t := nodeAttr(n, "transform")
	if !strings.HasPrefix(t, "translate(") {
		return tx, ty
	}
	nums := svgNumRe.FindAllString(t, -1)
	if len(nums) < 2 {
		return tx, ty
	}
	dx, err1 := strconv.ParseFloat(nums[0], 64)
	dy, err2 := strconv.ParseFloat(nums[1], 64)
	if err1 != nil || err2 != nil {
		return tx, ty
	}
	return tx + dx, ty + dy
}

// nodeAttr returns the value of a plain (unprefixed) attribute of n.
func nodeAttr(n *svgNode, name string) string {
	for _, a := range n.Attrs {
		if a.Name.Space == "" && a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// nodeFill returns the fill colour of n, taken from a fill attribute or a
// style attribute, preferring the explicit attribute.
func nodeFill(n *svgNode) (color.NRGBA, bool) {
	fill := nodeAttr(n, "fill")
	if fill == "" {
		for _, p := range splitStyleProps(nodeAttr(n, "style")) {
			if p.name == "fill" {
				fill = p.value
			}
		}
	}
	return parseFaviconColour(fill)
}

// parseFaviconColour parses hex (#rgb/#rrggbb) and a few common named
// colours, reporting ok=false for none, url() fills and unknowns.
func parseFaviconColour(s string) (color.NRGBA, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "none" || strings.HasPrefix(s, "url(") {
		return color.NRGBA{}, false
	}
	if strings.HasPrefix(s, "#") {
		hex := s[1:]
		parse := func(a, b byte) (uint8, bool) {
			v, err := strconv.ParseUint(string([]byte{a, b}), 16, 8)
			return uint8(v), err == nil
		}
		var r, g, b uint8
		var ok bool
		switch len(hex) {
		case 3:
			r, ok = parse(hex[0], hex[0])
			g, ok = parse(hex[1], hex[1])
			b, ok = parse(hex[2], hex[2])
		case 6:
			r, ok = parse(hex[0], hex[1])
			g, ok = parse(hex[2], hex[3])
			b, ok = parse(hex[4], hex[5])
		}
		if ok {
			return color.NRGBA{r, g, b, 0xff}, true
		}
		return color.NRGBA{}, false
	}
	switch strings.ToLower(s) {
	case "red":
		return color.NRGBA{0xff, 0, 0, 0xff}, true
	case "yellow":
		return color.NRGBA{0xff, 0xff, 0, 0xff}, true
	case "black":
		return color.NRGBA{0, 0, 0, 0xff}, true
	case "white":
		return color.NRGBA{0xff, 0xff, 0xff, 0xff}, true
	}
	return color.NRGBA{}, false
}

// svgNumRe matches a floating-point number in SVG path/transform data.
var svgNumRe = regexp.MustCompile(`-?(?:\d+\.?\d*|\.\d+)(?:[eE][-+]?\d+)?`)

// svgToken is one token of SVG path data: a command letter or a number.
type svgToken struct {
	isCmd bool
	cmd   byte
	val   float64
}

// svgPathTokens splits SVG path data into commands and numbers.
func svgPathTokens(d string) ([]svgToken, error) {
	var toks []svgToken
	i := 0
	for i < len(d) {
		switch c := d[i]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			i++
		case strings.ContainsRune("MmZzLlHhVvCcSsQqTtAa", rune(c)):
			toks = append(toks, svgToken{isCmd: true, cmd: c})
			i++
		case c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9'):
			m := svgNumRe.FindString(d[i:])
			if m == "" {
				return nil, fmt.Errorf("unexpected character %q in path data", c)
			}
			v, err := strconv.ParseFloat(m, 64)
			if err != nil {
				return nil, err
			}
			toks = append(toks, svgToken{val: v})
			i += len(m)
		default:
			return nil, fmt.Errorf("unexpected character %q in path data", c)
		}
	}
	return toks, nil
}

// parseFaviconPathData converts SVG path data into closed polygons, applying
// the group translate (tx,ty). Only the commands the logo uses are supported.
func parseFaviconPathData(d string, tx, ty float64) ([][]fpoint, error) {
	toks, err := svgPathTokens(d)
	if err != nil {
		return nil, err
	}

	var polys [][]fpoint
	var cur []fpoint
	var x, y, sx, sy float64
	var cmd byte
	i := 0

	next := func() (float64, error) {
		if i >= len(toks) || toks[i].isCmd {
			return 0, fmt.Errorf("expected a number in path data")
		}
		v := toks[i].val
		i++
		return v, nil
	}

	// point reads one coordinate pair relative to the current point (for
	// relative commands), advances the untransformed cursor and returns the
	// transformed point.
	point := func(relative bool) (fpoint, error) {
		a, err := next()
		if err != nil {
			return fpoint{}, err
		}
		b, err := next()
		if err != nil {
			return fpoint{}, err
		}
		if relative {
			a, b = x+a, y+b
		}
		x, y = a, b
		return fpoint{a + tx, b + ty}, nil
	}

	closePath := func() {
		if len(cur) > 0 {
			polys = append(polys, cur)
			cur = nil
		}
	}

	for i < len(toks) {
		if toks[i].isCmd {
			cmd = toks[i].cmd
			i++
			continue
		}
		rel := cmd >= 'a' && cmd <= 'z'
		switch cmd {
		case 'M', 'm':
			closePath()
			p, err := point(rel)
			if err != nil {
				return nil, err
			}
			sx, sy = x, y
			cur = append(cur, p)
			if rel {
				cmd = 'l'
			} else {
				cmd = 'L'
			}
		case 'L', 'l':
			p, err := point(rel)
			if err != nil {
				return nil, err
			}
			cur = append(cur, p)
		case 'H', 'h':
			a, err := next()
			if err != nil {
				return nil, err
			}
			if rel {
				a = x + a
			}
			x = a
			cur = append(cur, fpoint{a + tx, y + ty})
		case 'V', 'v':
			b, err := next()
			if err != nil {
				return nil, err
			}
			if rel {
				b = y + b
			}
			y = b
			cur = append(cur, fpoint{x + tx, b + ty})
		case 'C', 'c':
			for {
				baseX, baseY := x, y
				dx1, err := next()
				if err != nil {
					return nil, err
				}
				dy1, err := next()
				if err != nil {
					return nil, err
				}
				dx2, err := next()
				if err != nil {
					return nil, err
				}
				dy2, err := next()
				if err != nil {
					return nil, err
				}
				dx, err := next()
				if err != nil {
					return nil, err
				}
				dy, err := next()
				if err != nil {
					return nil, err
				}
				var c1, c2, p3 fpoint
				if rel {
					c1 = fpoint{baseX + dx1 + tx, baseY + dy1 + ty}
					c2 = fpoint{baseX + dx2 + tx, baseY + dy2 + ty}
					p3 = fpoint{baseX + dx + tx, baseY + dy + ty}
					x, y = baseX+dx, baseY+dy
				} else {
					c1 = fpoint{dx1 + tx, dy1 + ty}
					c2 = fpoint{dx2 + tx, dy2 + ty}
					p3 = fpoint{dx + tx, dy + ty}
					x, y = dx, dy
				}
				flattenFaviconCubic(fpoint{baseX + tx, baseY + ty}, c1, c2, p3, &cur)
				if i >= len(toks) || toks[i].isCmd {
					break
				}
			}
		case 'Z', 'z':
			if len(cur) > 0 {
				if start := cur[0]; cur[len(cur)-1] != start {
					cur = append(cur, start)
				}
			}
			closePath()
			x, y = sx, sy
		default:
			return nil, fmt.Errorf("unsupported path command %q", cmd)
		}
	}
	closePath()
	return polys, nil
}

// flattenFaviconCubic appends the end point of a cubic Bezier, subdividing
// until it is flat enough to be a straight line.
func flattenFaviconCubic(p0, p1, p2, p3 fpoint, out *[]fpoint) {
	flattenFaviconCubicRec(p0, p1, p2, p3, out, 0)
}

func flattenFaviconCubicRec(p0, p1, p2, p3 fpoint, out *[]fpoint, depth int) {
	if depth > 12 || math.Abs(p1.x-p3.x)+math.Abs(p1.y-p3.y)+math.Abs(p2.x-p3.x)+math.Abs(p2.y-p3.y) < 0.5 {
		*out = append(*out, p3)
		return
	}
	mid := func(a, b fpoint) fpoint {
		return fpoint{(a.x + b.x) / 2, (a.y + b.y) / 2}
	}
	p01, p12, p23 := mid(p0, p1), mid(p1, p2), mid(p2, p3)
	p012, p123 := mid(p01, p12), mid(p12, p23)
	pm := mid(p012, p123)
	flattenFaviconCubicRec(p0, p01, p012, pm, out, depth+1)
	flattenFaviconCubicRec(pm, p123, p23, p3, out, depth+1)
}

// renderFaviconPNG rasterises the logo shapes into a size-by-size PNG.
func renderFaviconPNG(shapes []faviconShape, size int) ([]byte, error) {
	img := rasteriseFavicon(shapes, size)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// rasteriseFavicon draws the logo centred into a transparent square, keeping
// its aspect ratio. Shapes are scanline-filled in document order (so the
// black outline wins where it overlaps the fills) into a 2x-bigger canvas,
// then box-downsampled for anti-aliased edges.
func rasteriseFavicon(shapes []faviconShape, size int) *image.NRGBA {
	const ss = 2
	big := size * ss

	img := image.NewNRGBA(image.Rect(0, 0, big, big))
	scale := math.Min(float64(big)/faviconCanvasW, float64(big)/faviconCanvasH)
	ox := (float64(big) - faviconCanvasW*scale) / 2
	oy := (float64(big) - faviconCanvasH*scale) / 2

	for i := range shapes {
		scanFillFavicon(img, &shapes[i], scale, ox, oy)
	}

	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	block := ss * ss
	var acc [3]int
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			acc = [3]int{}
			covered := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					c := img.NRGBAAt(x*ss+sx, y*ss+sy)
					if c.A != 0 {
						covered++
					}
					acc[0] += int(c.R)
					acc[1] += int(c.G)
					acc[2] += int(c.B)
				}
			}
			if covered > 0 {
				out.SetNRGBA(x, y, color.NRGBA{
					R: uint8((acc[0] + block/2) / block),
					G: uint8((acc[1] + block/2) / block),
					B: uint8((acc[2] + block/2) / block),
					A: uint8((covered*255 + block/2) / block),
				})
			}
		}
	}
	return out
}

// faviconEdge is one polygon edge for scanline filling, kept in its original
// direction so the nonzero winding rule can be applied across subpaths.
type faviconEdge struct {
	x1, y1 float64
	x2, y2 float64
}

// scanFillFavicon maps a shape's polygons into image coordinates (the logo is
// fitted inside a square, keeping its aspect ratio) and fills them with the
// nonzero winding rule, matching the SVG default fill-rule that the logo was
// designed with: subpaths with the same orientation union, opposite ones carve
// holes.
func scanFillFavicon(img *image.NRGBA, s *faviconShape, scale, ox, oy float64) {
	var edges []faviconEdge
	minY, maxY := math.Inf(1), math.Inf(-1)
	for _, poly := range s.polygons {
		n := len(poly)
		if n == 0 {
			continue
		}
		for i := 0; i < n; i++ {
			p1, p2 := poly[i], poly[(i+1)%n]
			if p1.y == p2.y {
				continue
			}
			e := faviconEdge{
				x1: p1.x*scale + ox,
				y1: p1.y*scale + oy,
				x2: p2.x*scale + ox,
				y2: p2.y*scale + oy,
			}
			if e.y1 < minY {
				minY = e.y1
			}
			if e.y1 > maxY {
				maxY = e.y1
			}
			if e.y2 < minY {
				minY = e.y2
			}
			if e.y2 > maxY {
				maxY = e.y2
			}
			edges = append(edges, e)
		}
	}
	if len(edges) == 0 {
		return
	}

	rowMin := int(math.Ceil(minY)) - 1
	if rowMin < img.Bounds().Min.Y {
		rowMin = img.Bounds().Min.Y
	}
	rowMax := int(math.Floor(maxY))
	if rowMax > img.Bounds().Max.Y-1 {
		rowMax = img.Bounds().Max.Y - 1
	}

	type crossing struct {
		x   float64
		dir int
	}
	for row := rowMin; row <= rowMax; row++ {
		y := float64(row) + 0.5
		var cs []crossing
		for _, e := range edges {
			if (e.y1 <= y && e.y2 > y) || (e.y2 <= y && e.y1 > y) {
				x := e.x1 + (y-e.y1)*(e.x2-e.x1)/(e.y2-e.y1)
				dir := 1
				if e.y1 > e.y2 {
					dir = -1
				}
				cs = append(cs, crossing{x, dir})
			}
		}
		sort.Slice(cs, func(i, j int) bool { return cs[i].x < cs[j].x })
		winding := 0
		for k := 0; k < len(cs); k++ {
			winding += cs[k].dir
			if winding != 0 && k+1 < len(cs) {
				x0, x1 := cs[k].x, cs[k+1].x
				xStart := int(math.Ceil(x0))
				if xStart < img.Bounds().Min.X {
					xStart = img.Bounds().Min.X
				}
				xEnd := int(math.Floor(x1))
				if xEnd > img.Bounds().Max.X-1 {
					xEnd = img.Bounds().Max.X - 1
				}
				for x := xStart; x <= xEnd; x++ {
					img.SetNRGBA(x, row, s.color)
				}
			}
		}
	}
}

// encodeICO wraps PNG images into an ICO container (PNG compression inside
// ICO is supported by Windows Vista and later and every modern browser).
func encodeICO(pngs [][]byte, sizes []int) ([]byte, error) {
	if len(pngs) != len(sizes) {
		return nil, fmt.Errorf("icon count %d does not match sizes %d", len(pngs), len(sizes))
	}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, uint16(0)); err != nil { // reserved
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint16(1)); err != nil { // type: icon
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint16(len(pngs))); err != nil { // count
		return nil, err
	}

	offset := 6 + 16*len(pngs)
	for i, size := range sizes {
		var entry bytes.Buffer
		if size >= 256 {
			entry.WriteByte(0)
		} else {
			entry.WriteByte(byte(size))
		}
		if size >= 256 {
			entry.WriteByte(0)
		} else {
			entry.WriteByte(byte(size))
		}
		entry.WriteByte(0) // palette entries
		entry.WriteByte(0) // reserved
		if err := binary.Write(&entry, binary.LittleEndian, uint16(1)); err != nil { // planes
			return nil, err
		}
		if err := binary.Write(&entry, binary.LittleEndian, uint16(32)); err != nil { // bit count
			return nil, err
		}
		if err := binary.Write(&entry, binary.LittleEndian, uint32(len(pngs[i]))); err != nil { // bytes
			return nil, err
		}
		if err := binary.Write(&entry, binary.LittleEndian, uint32(offset)); err != nil { // offset
			return nil, err
		}
		offset += len(pngs[i])
		buf.Write(entry.Bytes())
	}
	for _, p := range pngs {
		buf.Write(p)
	}
	return buf.Bytes(), nil
}

// buildFaviconManifest returns the web app manifest referencing the generated
// icons. start_url is relative so the app still works under a proxy sub-path.
func buildFaviconManifest() []byte {
	return []byte(`{
  "name": "Magooify",
  "short_name": "Magooify",
  "icons": [
    { "src": "favicon-16x16.png", "sizes": "16x16", "type": "image/png" },
    { "src": "favicon-32x32.png", "sizes": "32x32", "type": "image/png" },
    { "src": "favicon-48x48.png", "sizes": "48x48", "type": "image/png" },
    { "src": "favicon.svg", "sizes": "any", "type": "image/svg+xml" }
  ],
  "theme_color": "#6366f1",
  "background_color": "#ffffff",
  "display": "standalone",
  "start_url": "./"
}`)
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
	cleanLogo := flag.String("clean-logo", "", "Remove Inkscape-specific parts from the given SVG logo file in place and exit")
	genFavicons := flag.String("gen-favicons", "", "Render the UI logo into favicon files (ICO, PNG, SVG, web manifest) in the given UI directory and exit")
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

	// Offline logo cleanup (used by the build script to strip Inkscape-specific
	// parts from the UI logo, leaving a plain SVG that renders in all browsers).
	if *cleanLogo != "" {
		if err := cleanLogoSVG(*cleanLogo); err != nil {
			log.Fatalf("Failed to clean logo: %v", err)
		}
		log.Printf("Cleaned logo: %s", *cleanLogo)
		return
	}

	// Offline favicon generation (used by the build script to render the UI
	// logo into the favicon files embedded in the executable).
	if *genFavicons != "" {
		if err := generateFavicons(*genFavicons); err != nil {
			log.Fatalf("Failed to generate favicons: %v", err)
		}
		log.Printf("Generated favicons in %s", *genFavicons)
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

// vectoriseMaxColourLayers is the largest number of distinct colour layers the
// trace produces. The most populous colours in the bitmap are kept as their own
// layers and any remaining colours are merged into the nearest kept layer, so
// anti-aliasing and tiny colour variations don't explode the layer count.
const vectoriseMaxColourLayers = 16

// vectoriseMaxFitError is the largest distance (in pixels) a fitted bezier
// curve may stray from the traced boundary before the fit is abandoned in
// favour of a plain polygon segment.
const vectoriseMaxFitError = 1.5

// fpoint is a floating-point grid point used while fitting curves.
type fpoint struct{ x, y float64 }

// isSVGDocument reports whether the bytes look like an SVG document rather
// than a raster image. Both a bare <svg> root and an XML prolog followed by an
// <svg> root are recognised.
func isSVGDocument(img []byte) bool {
	trimmed := bytes.TrimLeft(img, " \t\r\n")
	return bytes.HasPrefix(trimmed, []byte("<svg")) ||
		(bytes.HasPrefix(trimmed, []byte("<?xml")) && bytes.Contains(trimmed, []byte("<svg")))
}

// vectoriseBitmap converts a processed bitmap into an SVG document, tracing the
// artwork into one vector layer per colour so the vector result matches the
// bitmap version. Bitmap formats supported by the standard library (PNG, JPEG,
// GIF) are accepted; an image that is already vector (an SVG document) is
// passed through unchanged. The returned document has a transparent background
// with the colour layers on top, sized to the source image.
func vectoriseBitmap(imgBytes []byte) ([]byte, error) {
	if isSVGDocument(imgBytes) {
		return imgBytes, nil
	}

	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("unsupported image format (%v); PNG and JPEG output can be vectorised", err)
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	return colourLoopsToSVG(colourLayers(img), w, h), nil
}

// vectorPixel is a single pixel's colour, used while building colour layers.
type vectorPixel struct {
	r, g, b, a uint8
}

// colourLayer is one traced colour: the fill colour and the binary mask of
// pixels belonging to it. count is the number of pixels in the mask.
type colourLayer struct {
	r, g, b uint8
	mask    []bool
	count   int
}

// colourBucket accumulates the pixels of one quantised colour so the average
// fill colour and total population of the layer can be computed.
type colourBucket struct {
	count            int
	sumR, sumG, sumB uint64
}

// quantColourKey folds a pixel's colour into a coarse 12-bit bucket key (4 bits
// per channel). Neighbouring colours share a bucket, so photos and soft edges
// produce a manageable number of candidate layers instead of one per colour.
func quantColourKey(p vectorPixel) int {
	return int(p.r>>4)<<8 | int(p.g>>4)<<4 | int(p.b>>4)
}

// colourLayers splits the opaque, non-background pixels of an image into
// per-colour masks. Transparent pixels and the near-white background are left
// untraced. The colours are quantised and the most populous up to
// vectoriseMaxColourLayers kept; every other pixel is merged into the nearest
// kept colour by RGB distance so anti-aliasing doesn't add stray layers. Each
// layer's fill is the average of the pixels that populate it.
func colourLayers(img image.Image) []colourLayer {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	pixels := make([]vectorPixel, w*h)
	buckets := map[int]*colourBucket{}
	var keys []int
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			p := vectorPixel{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), uint8(a >> 8)}
			pixels[y*w+x] = p
			if isBackgroundColour(p) {
				continue
			}
			k := quantColourKey(p)
			if buckets[k] == nil {
				buckets[k] = &colourBucket{}
				keys = append(keys, k)
			}
			bk := buckets[k]
			bk.count++
			bk.sumR += uint64(p.r)
			bk.sumG += uint64(p.g)
			bk.sumB += uint64(p.b)
		}
	}

	sort.Slice(keys, func(i, j int) bool {
		if buckets[keys[i]].count != buckets[keys[j]].count {
			return buckets[keys[i]].count > buckets[keys[j]].count
		}
		return keys[i] < keys[j]
	})
	if len(keys) > vectoriseMaxColourLayers {
		keys = keys[:vectoriseMaxColourLayers]
	}

	layers := make([]colourLayer, 0, len(keys))
	layerFor := make(map[int]int, len(keys))
	for i, k := range keys {
		bk := buckets[k]
		layers = append(layers, colourLayer{
			r:    uint8((bk.sumR + uint64(bk.count)/2) / uint64(bk.count)),
			g:    uint8((bk.sumG + uint64(bk.count)/2) / uint64(bk.count)),
			b:    uint8((bk.sumB + uint64(bk.count)/2) / uint64(bk.count)),
			mask: make([]bool, w*h),
		})
		layerFor[k] = i
	}

	for idx, p := range pixels {
		if isBackgroundColour(p) {
			continue
		}
		li, ok := layerFor[quantColourKey(p)]
		if !ok {
			li = nearestLayer(p, layers)
		}
		layers[li].mask[idx] = true
		layers[li].count++
	}

	return layers
}

// isBackgroundColour reports whether a pixel is part of the untraced
// background: transparent, or a near-white grey (the paper the artwork is
// printed on).
func isBackgroundColour(p vectorPixel) bool {
	if p.a < 0x80 {
		return true
	}
	return isNearWhite(p.r, p.g, p.b)
}

// isNearWhite reports whether the colour is close to white: bright and with
// little chroma. Bright colours such as yellow stay foreground.
func isNearWhite(r, g, b uint8) bool {
	mx := max(r, max(g, b))
	mn := min(r, min(g, b))
	return mx > 235 && int(mx)-int(mn) < 25
}

// nearestLayer returns the index of the colour layer whose fill colour is
// closest to the given pixel, used to absorb colours that didn't make the kept
// layer cut.
func nearestLayer(p vectorPixel, layers []colourLayer) int {
	best, bestDist := 0, int(^uint(0)>>1)
	for i, l := range layers {
		dr := int(p.r) - int(l.r)
		dg := int(p.g) - int(l.g)
		db := int(p.b) - int(l.b)
		if d := dr*dr + dg*dg + db*db; d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
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

// colourLoopsToSVG assembles the traced colour layers into an SVG document.
// Each layer becomes a group filled with its colour containing the traced
// loops of its mask, combined into one path per layer with the even-odd rule so
// holes and nested shapes render correctly.
func colourLoopsToSVG(layers []colourLayer, w, h int) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`+"\n", w, h, w, h)
	for _, layer := range layers {
		if layer.count == 0 {
			continue
		}
		fmt.Fprintf(&b, "  <g fill=\"#%02x%02x%02x\">\n", layer.r, layer.g, layer.b)
		if loops := traceAllLoops(layer.mask, w, h); len(loops) > 0 {
			b.WriteString("    <path d=\"")
			for _, loop := range loops {
				b.WriteString(loopPathData(loop))
			}
			b.WriteString("\" fill-rule=\"evenodd\"/>\n")
		}
		b.WriteString("  </g>\n")
	}
	b.WriteString("</svg>\n")
	return []byte(b.String())
}
