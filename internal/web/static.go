package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
)

// staticHandler serves the embedded single-page UI.
//
//   - /assets/* is fingerprinted by the build, so it is cached for a year.
//   - index.html is never cached, so a new release shows up straight away.
//   - A path that is not a file falls back to index.html (client-side routes),
//     except under /assets/, where a missing file is a real 404.
//   - If "<file>.br" exists the browser gets it as-is when it accepts brotli;
//     otherwise it is decompressed on the fly. A file with no .br is sent raw.
type staticHandler struct {
	fsys  fs.FS
	etags sync.Map // variant key -> etag
}

func newStatic(fsys fs.FS) http.Handler { return &staticHandler{fsys: fsys} }

func (h *staticHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel, ok := cleanRel(r.URL.Path)
	if ok && (rel == "api" || strings.HasPrefix(rel, "api/")) {
		writeError(w, http.StatusNotFound, "not_found", "No such endpoint.")
		return
	}
	if ok && strings.HasSuffix(rel, ".br") {
		http.NotFound(w, r) // the precompressed twins are not addressable
		return
	}

	name, spa := "index.html", true
	if ok && rel != "" && (h.isFile(rel) || h.isFile(rel+".br")) {
		name, spa = rel, false
	} else if ok && (rel == "assets" || strings.HasPrefix(rel, "assets/")) {
		http.NotFound(w, r)
		return
	}
	h.serve(w, r, name, spa)
}

// cleanRel turns a URL path into a slash-separated name relative to the
// root ("" for the root itself).
func cleanRel(p string) (string, bool) {
	if strings.ContainsAny(p, "\\\x00") {
		return "", false
	}
	rel := strings.TrimPrefix(path.Clean("/"+p), "/")
	if rel != "" && !fs.ValidPath(rel) {
		return "", false
	}
	return rel, true
}

func (h *staticHandler) isFile(name string) bool {
	fi, err := fs.Stat(h.fsys, name)
	return err == nil && !fi.IsDir()
}

func (h *staticHandler) serve(w http.ResponseWriter, r *http.Request, name string, spa bool) {
	var rawSize, brSize int64 = -1, -1
	if fi, err := fs.Stat(h.fsys, name); err == nil && !fi.IsDir() {
		rawSize = fi.Size()
	}
	if fi, err := fs.Stat(h.fsys, name+".br"); err == nil && !fi.IsDir() {
		brSize = fi.Size()
	}
	if rawSize < 0 && brSize < 0 {
		http.Error(w, "The web interface is not built into this binary.", http.StatusNotFound)
		return
	}

	hd := w.Header()
	hd.Set("Vary", "Accept-Encoding")
	hd.Set("Content-Type", contentType(name))
	if !spa && strings.HasPrefix(name, "assets/") {
		hd.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		hd.Set("Cache-Control", "no-cache")
	}

	// Pick the representation.
	var (
		file     string // file to read
		encoding string // Content-Encoding to announce
		decode   bool   // read a .br file but send the decoded bytes
		size     int64  = -1
		variant  string
	)
	switch {
	case brSize >= 0 && acceptsBrotli(r.Header.Get("Accept-Encoding")):
		file, encoding, size, variant = name+".br", "br", brSize, "br"
	case rawSize >= 0:
		file, size, variant = name, rawSize, "raw"
	default:
		file, decode, variant = name+".br", true, "dec"
	}

	etag := h.etag(file, variant)
	if etag != "" {
		hd.Set("ETag", etag)
		if etagMatches(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if encoding != "" {
		hd.Set("Content-Encoding", encoding)
	}
	if size >= 0 {
		hd.Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if r.Method == http.MethodHead {
		return
	}

	f, err := h.fsys.Open(file)
	if err != nil {
		http.Error(w, "file unavailable", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	var src io.Reader = f
	if decode {
		src = brotli.NewReader(f)
	}
	_, _ = io.Copy(w, src) // a failure here is the client going away
}

// etag hashes the stored bytes once; the embedded files never change while
// the process runs. The decoded view of a .br file gets its own tag.
func (h *staticHandler) etag(file, variant string) string {
	key := variant + ":" + file
	if v, ok := h.etags.Load(key); ok {
		return v.(string)
	}
	f, err := h.fsys.Open(file)
	if err != nil {
		return ""
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return ""
	}
	tag := `"` + hex.EncodeToString(sum.Sum(nil))[:20]
	if variant == "dec" {
		tag += "-d"
	}
	tag += `"`
	h.etags.Store(key, tag)
	return tag
}

func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	for _, c := range strings.Split(header, ",") {
		c = strings.TrimSpace(c)
		if c == "*" || strings.TrimPrefix(c, "W/") == etag {
			return true
		}
	}
	return false
}

// acceptsBrotli reports whether an Accept-Encoding header allows br.
func acceptsBrotli(header string) bool {
	if header == "" {
		return false
	}
	var star, explicit bool
	var starOK, brOK bool
	for _, part := range strings.Split(header, ",") {
		token, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		token = strings.ToLower(strings.TrimSpace(token))
		ok := true
		for _, p := range strings.Split(params, ";") {
			k, v, found := strings.Cut(strings.TrimSpace(p), "=")
			if found && strings.EqualFold(strings.TrimSpace(k), "q") {
				if q, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && q <= 0 {
					ok = false
				}
			}
		}
		switch token {
		case "br":
			explicit, brOK = true, ok
		case "*":
			star, starOK = true, ok
		}
	}
	if explicit {
		return brOK
	}
	return star && starOK
}

// contentTypes is explicit because Go's mime package reads the Windows
// registry, which can return something odd for .js on a tidied-up machine.
var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
	".xml":         "application/xml",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".gif":         "image/gif",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".ico":         "image/x-icon",
	".woff2":       "font/woff2",
	".woff":        "font/woff",
	".ttf":         "font/ttf",
	".wasm":        "application/wasm",
}

func contentType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if t, ok := contentTypes[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}
