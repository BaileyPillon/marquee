package downloads

import (
	"context"
	"crypto/sha1" //nolint:gosec // BitTorrent info-hashes are SHA-1 by definition
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// ---- bencode -----------------------------------------------------------------
//
// Just enough of it to read a .torrent file: integers, byte strings, lists and
// dictionaries. Strings come back as Go strings holding the raw bytes.

const (
	maxBencodeDepth = 32
	// maxTorrentBytes caps a .torrent download. Real ones are well under 1 MB;
	// a huge pack with tens of thousands of files can reach a few MB.
	maxTorrentBytes = 10 << 20
	maxRedirects    = 5
)

var errBencode = errors.New("not a valid bencoded file")

type bencoder struct {
	data []byte
	pos  int
	// The raw bytes of the top-level "info" value, which is what the info-hash
	// is computed over.
	infoStart, infoEnd int
}

// decodeBencode decodes one value from data (anything after it is ignored) and
// reports the span of the top-level dictionary's "info" value, if there is one.
func decodeBencode(data []byte) (v any, infoStart, infoEnd int, err error) {
	d := &bencoder{data: data, infoStart: -1, infoEnd: -1}
	v, err = d.value(0)
	if err != nil {
		return nil, -1, -1, err
	}
	return v, d.infoStart, d.infoEnd, nil
}

func (d *bencoder) value(depth int) (any, error) {
	if depth > maxBencodeDepth {
		return nil, fmt.Errorf("%w: nested too deeply", errBencode)
	}
	if d.pos >= len(d.data) {
		return nil, fmt.Errorf("%w: ends too early", errBencode)
	}
	switch c := d.data[d.pos]; {
	case c == 'i':
		return d.integer()
	case c == 'l':
		return d.list(depth)
	case c == 'd':
		return d.dict(depth)
	case c >= '0' && c <= '9':
		return d.str()
	default:
		return nil, fmt.Errorf("%w: unexpected byte %q at %d", errBencode, c, d.pos)
	}
}

func (d *bencoder) integer() (any, error) {
	d.pos++ // 'i'
	end := d.pos
	for end < len(d.data) && d.data[end] != 'e' {
		end++
	}
	if end >= len(d.data) {
		return nil, fmt.Errorf("%w: integer never ends", errBencode)
	}
	s := string(d.data[d.pos:end])
	digits := strings.TrimPrefix(s, "-")
	switch {
	case digits == "" || strings.Trim(digits, "0123456789") != "":
		return nil, fmt.Errorf("%w: bad integer", errBencode)
	case len(digits) > 1 && digits[0] == '0', s == "-0":
		return nil, fmt.Errorf("%w: bad integer", errBencode)
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: integer too large", errBencode)
	}
	d.pos = end + 1
	return n, nil
}

func (d *bencoder) str() (any, error) {
	colon := d.pos
	for colon < len(d.data) && d.data[colon] != ':' {
		colon++
	}
	if colon >= len(d.data) {
		return nil, fmt.Errorf("%w: string without a length", errBencode)
	}
	lenText := string(d.data[d.pos:colon])
	if lenText == "" || strings.Trim(lenText, "0123456789") != "" || (len(lenText) > 1 && lenText[0] == '0') {
		return nil, fmt.Errorf("%w: bad string length", errBencode)
	}
	n, err := strconv.Atoi(lenText)
	if err != nil || n < 0 || n > len(d.data)-colon-1 {
		return nil, fmt.Errorf("%w: string runs past the end", errBencode)
	}
	start := colon + 1
	d.pos = start + n
	return string(d.data[start:d.pos]), nil
}

func (d *bencoder) list(depth int) (any, error) {
	d.pos++ // 'l'
	out := []any{}
	for {
		if d.pos >= len(d.data) {
			return nil, fmt.Errorf("%w: list never ends", errBencode)
		}
		if d.data[d.pos] == 'e' {
			d.pos++
			return out, nil
		}
		v, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}

func (d *bencoder) dict(depth int) (any, error) {
	d.pos++ // 'd'
	out := map[string]any{}
	for {
		if d.pos >= len(d.data) {
			return nil, fmt.Errorf("%w: dictionary never ends", errBencode)
		}
		if d.data[d.pos] == 'e' {
			d.pos++
			return out, nil
		}
		if c := d.data[d.pos]; c < '0' || c > '9' {
			return nil, fmt.Errorf("%w: dictionary key is not a string", errBencode)
		}
		k, err := d.str()
		if err != nil {
			return nil, err
		}
		key := k.(string)
		start := d.pos
		v, err := d.value(depth + 1)
		if err != nil {
			return nil, err
		}
		if depth == 0 && key == "info" {
			d.infoStart, d.infoEnd = start, d.pos
		}
		out[key] = v
	}
}

// ---- torrent files -----------------------------------------------------------

// TorrentMeta is what Marquee needs from a .torrent file.
type TorrentMeta struct {
	InfoHash string // lower-case hex, the id the client uses
	Name     string
	Size     int64
}

// ParseTorrent reads a .torrent file and computes its info-hash: SHA-1 of the
// raw bencoded "info" dictionary, as lower-case hex. (A version-2-only torrent
// has no SHA-1 id; clients identify it by the first 20 bytes of the SHA-256.)
func ParseTorrent(data []byte) (TorrentMeta, error) {
	if len(data) == 0 || data[0] != 'd' {
		return TorrentMeta{}, errors.New("that is not a torrent file")
	}
	v, start, end, err := decodeBencode(data)
	if err != nil {
		return TorrentMeta{}, fmt.Errorf("that is not a torrent file: %w", err)
	}
	top, _ := v.(map[string]any)
	info, _ := top["info"].(map[string]any)
	if info == nil || start < 0 || end <= start {
		return TorrentMeta{}, errors.New("that torrent file has no info section")
	}
	raw := data[start:end]

	meta := TorrentMeta{}
	if _, hasPieces := info["pieces"]; !hasPieces && info["meta version"] == int64(2) {
		sum := sha256.Sum256(raw)
		meta.InfoHash = hex.EncodeToString(sum[:20])
	} else {
		sum := sha1.Sum(raw) //nolint:gosec // see import
		meta.InfoHash = hex.EncodeToString(sum[:])
	}
	meta.Name, _ = info["name"].(string)
	if n, ok := info["length"].(int64); ok {
		meta.Size = n
	} else if files, ok := info["files"].([]any); ok {
		for _, f := range files {
			if fm, ok := f.(map[string]any); ok {
				if n, ok := fm["length"].(int64); ok {
					meta.Size += n
				}
			}
		}
	}
	return meta, nil
}

// ---- magnet links ------------------------------------------------------------

var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// MagnetInfoHash returns the lower-case hex info-hash of a magnet link. The
// hash may be 40 hex characters or 32 base32 characters (xt=urn:btih:), or a
// version-2 multihash (xt=urn:btmh:1220...), which clients shorten to 40.
func MagnetInfoHash(magnet string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(magnet))
	if err != nil || !strings.EqualFold(u.Scheme, "magnet") {
		return "", errors.New("that is not a magnet link")
	}
	var v2 string
	for name, values := range u.Query() {
		if !strings.EqualFold(name, "xt") {
			continue
		}
		for _, xt := range values {
			lower := strings.ToLower(xt)
			switch {
			case strings.HasPrefix(lower, "urn:btih:"):
				if h, ok := decodeBTIH(xt[len("urn:btih:"):]); ok {
					return h, nil
				}
			case strings.HasPrefix(lower, "urn:btmh:1220") && len(xt) == len("urn:btmh:")+68:
				if h := strings.ToLower(xt[len("urn:btmh:1220"):]); isHex(h) {
					v2 = h[:40]
				}
			}
		}
	}
	if v2 != "" {
		return v2, nil
	}
	return "", errors.New("that magnet link has no usable info-hash")
}

func decodeBTIH(s string) (string, bool) {
	switch len(s) {
	case 40:
		if isHex(s) {
			return strings.ToLower(s), true
		}
	case 32:
		raw, err := base32NoPad.DecodeString(strings.ToUpper(s))
		if err == nil && len(raw) == 20 {
			return hex.EncodeToString(raw), true
		}
	}
	return "", false
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// ---- fetching ----------------------------------------------------------------

// fetched is what an indexer's download link turned out to be: a .torrent file,
// or (some indexers redirect to one) a magnet link.
type fetched struct {
	File   []byte
	Meta   TorrentMeta
	Magnet string
}

// withoutURL drops the address from a network error. outbound already removes
// the query string, but private trackers also put a passkey in the path, so an
// error about an indexer link must name no part of it.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// fetchTorrent downloads a .torrent file through outbound. Indexer links can
// need the indexer's key and a client may not be able to reach the indexer at
// all, so Marquee fetches the file itself and hands the client the bytes.
//
// Redirects are followed by hand, up to maxRedirects, so that a redirect to
// magnet: (which no HTTP client can follow) is seen and returned as a magnet.
// The link carries the indexer's key: it is never logged, and errors say only
// what went wrong.
func fetchTorrent(ctx context.Context, hc *outbound.Client, link string) (fetched, error) {
	cur := link
	for hop := 0; hop <= maxRedirects; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cur, nil)
		if err != nil {
			return fetched{}, errors.New("the download link is not a web address")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fetched{}, errors.New("the download link is not an http or https address")
		}
		resp, err := hc.Do(req, outbound.WithNoRedirect())
		if err != nil {
			return fetched{}, fmt.Errorf("could not fetch the torrent file: %w", withoutURL(err))
		}
		status, loc := resp.StatusCode, resp.Header.Get("Location")

		if status >= 300 && status < 400 {
			_ = resp.Body.Close()
			if loc == "" {
				return fetched{}, fmt.Errorf("the source answered with HTTP %d and nowhere to go", status)
			}
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(loc)), "magnet:") {
				return fetched{Magnet: strings.TrimSpace(loc)}, nil
			}
			next, err := req.URL.Parse(loc)
			if err != nil {
				return fetched{}, errors.New("the source redirected to an address Marquee cannot read")
			}
			cur = next.String()
			continue
		}
		if status != http.StatusOK {
			_ = resp.Body.Close()
			return fetched{}, fmt.Errorf("the source answered with HTTP %d", status)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxTorrentBytes+1))
		_ = resp.Body.Close()
		if err != nil {
			return fetched{}, fmt.Errorf("could not read the torrent file: %w", withoutURL(err))
		}
		if len(body) > maxTorrentBytes {
			return fetched{}, errors.New("the torrent file is far too big to be real")
		}
		meta, err := ParseTorrent(body)
		if err != nil {
			// Usually a sign-in page where the file should be.
			return fetched{}, fmt.Errorf("the source did not return a torrent file: %w", err)
		}
		return fetched{File: body, Meta: meta}, nil
	}
	return fetched{}, errors.New("the source redirected too many times")
}
