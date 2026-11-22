package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// Artwork proxy. Posters, backdrops and covers are fetched from three
// third-party hosts (and nowhere else), cached on disk, and served from our own
// origin, so a browser never talks to them and the page's Content-Security-Policy
// can say img-src 'self'.
const (
	// DefaultArtworkMaxBytes caps the on-disk cache.
	DefaultArtworkMaxBytes = 500 << 20
	maxImageBytes          = 20 << 20
	negativeTTL            = time.Hour
	negativeMax            = 4096
	artworkTimeout         = 30 * time.Second
)

// Default image hosts.
const (
	DefaultTMDBImageURL   = "https://image.tmdb.org/t/p"
	DefaultTVmazeImageURL = "https://static.tvmaze.com"
	DefaultCAAURL         = "https://coverartarchive.org"
)

// ErrBadArtwork is returned for a ref the allowlist does not permit.
var ErrBadArtwork = &Error{Code: "invalid_artwork", Message: "That is not an artwork address Marquee serves."}

// errNoImage is returned for an unknown provider or a missing image.
func errNoImage() error { return &Error{Code: ErrNotFound.Code, Message: "No such image."} }

// artRule says which paths one provider may fetch.
type artRule struct {
	base  *url.URL
	allow func(rest string) bool
	// redirects are the only hosts a redirect from this provider may lead to (see
	// outbound.WithRedirectHosts).
	redirects []string
}

// CAARedirectHosts are where the Cover Art Archive sends a cover on to: its own
// host, and the Internet Archive (archive.org and the servers below it, such as
// ia800000.us.archive.org), which stores the images. Nothing else is followed.
var CAARedirectHosts = []string{"coverartarchive.org", "archive.org", "*.archive.org"}

var (
	tmdbSize    = regexp.MustCompile(`^(?:[wh][0-9]{2,4}|original)$`)
	tmdbFile    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}\.(?:jpg|jpeg|png|webp)$`)
	pathSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,120}$`)
	imageExt    = regexp.MustCompile(`\.(?:jpg|jpeg|png|webp|gif)$`)
	caaPath     = regexp.MustCompile(`^(?:release-group|release)/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/front(?:-(?:250|500|1200))?$`)
)

func allowTMDB(rest string) bool {
	parts := strings.Split(rest, "/")
	return len(parts) == 2 && tmdbSize.MatchString(parts[0]) && tmdbFile.MatchString(parts[1])
}

func allowTVmaze(rest string) bool {
	parts := strings.Split(rest, "/")
	if len(parts) < 3 || parts[0] != "uploads" || parts[1] != "images" {
		return false
	}
	for _, p := range parts {
		if !pathSegment.MatchString(p) || strings.Contains(p, "..") {
			return false
		}
	}
	return imageExt.MatchString(parts[len(parts)-1])
}

func allowCAA(rest string) bool { return caaPath.MatchString(rest) }

// ArtworkFile is an open cached image.
type ArtworkFile struct {
	File        *os.File
	ContentType string
	ModTime     time.Time
	Size        int64
	Tag         string // a strong ETag value, quotes included
}

// Close closes the file.
func (f *ArtworkFile) Close() error { return f.File.Close() }

// Artwork is the artwork proxy's engine: allowlist, fetch, disk cache.
type Artwork struct {
	hc       *outbound.Client
	dir      string
	maxBytes int64
	rules    map[string]artRule
	status   *Status
	log      *slog.Logger
	now      func() time.Time

	mu      sync.Mutex
	size    int64
	sized   bool
	flights map[string]*flight
	missing map[string]time.Time
}

type flight struct {
	done chan struct{}
	err  error
}

func newArtwork(hc *outbound.Client, status *Status, log *slog.Logger, now func() time.Time,
	dir string, maxBytes int64, bases map[string]string) (*Artwork, error) {

	if dir == "" {
		return nil, errors.New("metadata: the artwork cache needs a directory")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultArtworkMaxBytes
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("metadata: artwork cache: %w", err)
	}
	def := map[string]string{
		ProviderTMDB:   DefaultTMDBImageURL,
		ProviderTVmaze: DefaultTVmazeImageURL,
		"caa":          DefaultCAAURL,
	}
	for k, v := range bases {
		def[k] = v
	}
	a := &Artwork{
		hc: hc, dir: dir, maxBytes: maxBytes, status: status, log: log, now: now,
		rules:   map[string]artRule{},
		flights: map[string]*flight{}, missing: map[string]time.Time{},
	}
	for name, allow := range map[string]func(string) bool{
		ProviderTMDB: allowTMDB, ProviderTVmaze: allowTVmaze, "caa": allowCAA,
	} {
		u, err := baseURL(def[name])
		if err != nil {
			return nil, err
		}
		hc.SetHostLimit(u.Host, 30, 30) // image hosts are CDNs; a page shows many posters at once
		// An image may only be sent on to the provider's own host; the Cover Art
		// Archive also hands covers over to the Internet Archive.
		redirects := []string{u.Hostname()}
		if name == "caa" {
			for _, h := range CAARedirectHosts {
				if !slices.Contains(redirects, h) {
					redirects = append(redirects, h)
				}
			}
		}
		a.rules[name] = artRule{base: u, allow: allow, redirects: redirects}
	}
	return a, nil
}

// upstream returns the one URL a ref may be fetched from.
func (a *Artwork) upstreamURL(provider, rest string) (string, error) {
	rule, ok := a.rules[provider]
	if !ok {
		return "", errNoImage()
	}
	if rest == "" || len(rest) > 300 || !rule.allow(rest) {
		return "", ErrBadArtwork
	}
	target := rule.base.String() + "/" + rest
	u, err := url.Parse(target)
	// Belt and braces: whatever the patterns above let through, the request
	// goes to the allowlisted host, with no userinfo and no query.
	if err != nil || u.Scheme != rule.base.Scheme || u.Host != rule.base.Host ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(u.Path, "..") {
		return "", ErrBadArtwork
	}
	return target, nil
}

func cacheName(provider, rest string) string {
	sum := sha256.Sum256([]byte(provider + "/" + rest))
	return hex.EncodeToString(sum[:])
}

// Open returns the image for a ref ("tmdb/w500/abc.jpg" is provider "tmdb",
// rest "w500/abc.jpg"), downloading it first if it is not cached. The caller
// closes the file.
func (a *Artwork) Open(ctx context.Context, provider, rest string) (*ArtworkFile, error) {
	target, err := a.upstreamURL(provider, rest)
	if err != nil {
		return nil, err
	}
	name := cacheName(provider, rest)
	if f, err := a.openCached(name); err == nil {
		return f, nil
	}
	if a.knownMissing(name) {
		return nil, errNoImage()
	}

	redirects := a.rules[provider].redirects
	if err := a.singleFlight(ctx, name, func(ctx context.Context) error { return a.download(ctx, target, name, redirects) }); err != nil {
		return nil, err
	}
	f, err := a.openCached(name)
	if err != nil {
		return nil, upstream("artwork", errors.New("the image was not kept in the cache"))
	}
	return f, nil
}

// singleFlight runs fn once for concurrent callers of the same name. fn runs
// detached from the first caller's cancellation, so a browser that gives up
// does not fail everyone waiting on the same poster.
func (a *Artwork) singleFlight(ctx context.Context, name string, fn func(context.Context) error) error {
	a.mu.Lock()
	if fl, ok := a.flights[name]; ok {
		a.mu.Unlock()
		select {
		case <-fl.done:
			return fl.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	fl := &flight{done: make(chan struct{})}
	a.flights[name] = fl
	a.mu.Unlock()

	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), artworkTimeout)
	defer cancel()
	fl.err = fn(dctx)
	a.mu.Lock()
	delete(a.flights, name)
	a.mu.Unlock()
	close(fl.done)
	return fl.err
}

func (a *Artwork) openCached(name string) (*ArtworkFile, error) {
	f, err := os.Open(filepath.Join(a.dir, name))
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("not a file")
	}
	var head [512]byte
	n, _ := io.ReadFull(f, head[:])
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	ct := http.DetectContentType(head[:n])
	if !okImageType(ct) {
		f.Close()
		_ = os.Remove(filepath.Join(a.dir, name)) // not an image: never serve it
		return nil, errors.New("not an image")
	}
	return &ArtworkFile{File: f, ContentType: ct, ModTime: fi.ModTime(), Size: fi.Size(), Tag: `"` + name[:24] + `"`}, nil
}

func okImageType(ct string) bool {
	switch ct {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/avif":
		return true
	}
	return false
}

func (a *Artwork) knownMissing(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	until, ok := a.missing[name]
	if !ok {
		return false
	}
	if a.now().After(until) {
		delete(a.missing, name)
		return false
	}
	return true
}

func (a *Artwork) markMissing(name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.missing) >= negativeMax {
		a.missing = map[string]time.Time{}
	}
	a.missing[name] = a.now().Add(negativeTTL)
}

// download fetches target into the cache, following redirects only to the hosts
// the provider's rule allows.
func (a *Artwork) download(ctx context.Context, target, name string, redirects []string) error {
	resp, err := a.hc.Get(ctx, target, outbound.WithRedirectHosts(redirects...))
	if err != nil {
		a.status.fail(ProviderArtwork, err)
		return upstream("artwork", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden:
		// Plenty of albums have no cover. Remember it for a while instead of
		// asking again on every page view.
		a.markMissing(name)
		a.status.ok(ProviderArtwork)
		return errNoImage()
	default:
		err := upstream("artwork", &statusError{Status: resp.StatusCode})
		a.status.fail(ProviderArtwork, err)
		return err
	}

	tmp, err := os.CreateTemp(a.dir, "tmp-*")
	if err != nil {
		return fmt.Errorf("metadata: artwork cache: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op after the rename

	var head [512]byte
	n, err := io.ReadFull(io.LimitReader(resp.Body, maxImageBytes+1), head[:])
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		tmp.Close()
		e := upstream("artwork", fmt.Errorf("reading the image: %w", err))
		a.status.fail(ProviderArtwork, e)
		return e
	}
	if !okImageType(http.DetectContentType(head[:n])) {
		tmp.Close()
		e := upstream("artwork", errors.New("the answer was not an image"))
		a.status.fail(ProviderArtwork, e)
		return e
	}
	written := int64(n)
	if _, err := tmp.Write(head[:n]); err != nil {
		tmp.Close()
		return err
	}
	if n == len(head) {
		rest, err := io.Copy(tmp, io.LimitReader(resp.Body, maxImageBytes+1-int64(n)))
		written += rest
		if err != nil {
			tmp.Close()
			e := upstream("artwork", fmt.Errorf("reading the image: %w", err))
			a.status.fail(ProviderArtwork, e)
			return e
		}
	}
	if written > maxImageBytes {
		tmp.Close()
		e := upstream("artwork", errors.New("the image is too large"))
		a.status.fail(ProviderArtwork, e)
		return e
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(a.dir, name)); err != nil {
		return fmt.Errorf("metadata: artwork cache: %w", err)
	}
	a.status.ok(ProviderArtwork)
	a.added(written)
	return nil
}

// added counts a new file and prunes if the cache is over its cap.
func (a *Artwork) added(n int64) {
	a.mu.Lock()
	if a.sized {
		a.size += n
	} else {
		a.ensureSizedLocked() // the scan already counts the file just written
	}
	over := a.size > a.maxBytes
	a.mu.Unlock()
	if over {
		a.prune()
	}
}

func (a *Artwork) ensureSizedLocked() {
	if a.sized {
		return
	}
	a.sized = true
	var total int64
	for _, e := range a.listLocked() {
		total += e.size
	}
	a.size = total
}

type cacheFile struct {
	name string
	size int64
	mod  time.Time
}

// listLocked lists finished files and removes temp files left by a crash.
func (a *Artwork) listLocked() []cacheFile {
	entries, err := os.ReadDir(a.dir)
	if err != nil {
		return nil
	}
	var out []cacheFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), "tmp-") {
			if fi, err := e.Info(); err == nil && a.now().Sub(fi.ModTime()) > time.Hour {
				_ = os.Remove(filepath.Join(a.dir, e.Name()))
			}
			continue
		}
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, cacheFile{name: e.Name(), size: fi.Size(), mod: fi.ModTime()})
	}
	return out
}

// prune deletes the oldest files until the cache is down to 90 percent of its
// cap, so it does not run again on the very next image.
func (a *Artwork) prune() {
	a.mu.Lock()
	defer a.mu.Unlock()
	files := a.listLocked()
	var total int64
	for _, f := range files {
		total += f.size
	}
	if total > a.maxBytes {
		sort.Slice(files, func(i, j int) bool {
			if !files[i].mod.Equal(files[j].mod) {
				return files[i].mod.Before(files[j].mod)
			}
			return files[i].name < files[j].name
		})
		target := a.maxBytes / 10 * 9
		for _, f := range files {
			if total <= target {
				break
			}
			if err := os.Remove(filepath.Join(a.dir, f.name)); err == nil {
				total -= f.size
			}
		}
	}
	a.size = total
	a.sized = true
}

// CacheSize returns how many bytes of images are cached.
func (a *Artwork) CacheSize() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ensureSizedLocked()
	return a.size
}
