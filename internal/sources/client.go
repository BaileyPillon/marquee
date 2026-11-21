package sources

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/outbound"
)

// fetch calls a source with the given query parameters (the key is added
// here) and returns the body of a good answer. Every failure is an *Error in
// plain words that contains neither the key nor the query string.
func (s *Service) fetch(ctx context.Context, r record, key string, params url.Values) ([]byte, error) {
	u, err := url.Parse(r.URL)
	if err != nil {
		return nil, failure(CodeNotAFeed, "The address of this source is not valid.")
	}
	if key != "" {
		params.Set("apikey", key)
	}
	u.RawQuery = params.Encode()
	resp, err := s.hc.Get(ctx, u.String(), outbound.WithTimeout(SearchTimeout))
	if err != nil {
		return nil, s.transportError(ctx, err, key)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure(CodeUnreachable, "The answer from the source was cut short.")
	}
	switch code := resp.StatusCode; {
	case code == http.StatusOK:
		return body, nil
	case (code == http.StatusForbidden || code == http.StatusServiceUnavailable) && botCheck(body):
		return nil, failure(CodeNotAFeed, "The site answered with a bot check (such as Cloudflare). Add it through Prowlarr or Jackett instead.")
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		if e := parseError(body); e != nil && e.Code == CodeKeyRefused {
			return nil, e
		}
		return nil, failure(CodeKeyRefused, "The source refused the API key.")
	case code == http.StatusNotFound:
		return nil, failure(CodeNotAFeed, "Nothing is at that address (404). Check it is the feed address, not the site's home page.")
	case code == http.StatusTooManyRequests:
		return nil, failure(CodeSlowDown, "The source asked Marquee to slow down.")
	case code >= 500:
		return nil, failure(CodeSourceError, fmt.Sprintf("The source had a problem (HTTP %d).", code))
	}
	// Some feeds answer an error as HTTP 400 with the usual <error> document.
	if e := parseError(body); e != nil {
		return nil, e
	}
	return nil, failure(CodeSourceError, fmt.Sprintf("The source answered with an unexpected status (HTTP %d).", resp.StatusCode))
}

func botCheck(body []byte) bool {
	n := min(len(body), 4096)
	l := strings.ToLower(string(body[:n]))
	return strings.Contains(l, "cloudflare") || strings.Contains(l, "just a moment")
}

// transportError describes a failure before any answer. The outbound client
// has already stripped the query from the error; the key is also blanked out
// in case a proxy or resolver echoed it.
func (s *Service) transportError(ctx context.Context, err error, key string) error {
	switch {
	case ctx.Err() != nil && errors.Is(err, ctx.Err()) && !errors.Is(err, context.DeadlineExceeded):
		return ctx.Err() // the caller gave up; not the source's fault
	case errors.Is(err, context.DeadlineExceeded):
		return failure(CodeTimeout, "The source took too long to answer.")
	}
	return failure(CodeUnreachable, "Could not reach the source: "+redact(err.Error(), key))
}

// ensureCaps returns the source's capabilities, asking for them when they were
// never fetched or are a week old. A failed refresh falls back to what is
// cached; a source with nothing cached and no answer gives the error.
func (s *Service) ensureCaps(ctx context.Context, r record, key string) (*Caps, error) {
	if r.Caps != nil && s.now().Sub(r.Caps.FetchedAt) < capsMaxAge {
		return r.Caps, nil
	}
	c, err := s.fetchCaps(ctx, r, key)
	if err != nil {
		if r.Caps != nil && ctx.Err() == nil {
			return r.Caps, nil
		}
		return nil, err
	}
	if err := s.store.SaveCaps(ctx, r.ID, c); err != nil {
		s.log.Warn("could not save a source's capabilities", "source", r.ID, "err", err)
	}
	return c, nil
}

func (s *Service) fetchCaps(ctx context.Context, r record, key string) (*Caps, error) {
	body, err := s.fetch(ctx, r, key, url.Values{"t": {"caps"}})
	if err != nil {
		return nil, err
	}
	root, rerr := rootName(body)
	if rerr != nil || root != "caps" {
		if root == "error" {
			if e := parseError(body); e != nil {
				return nil, e
			}
		}
		return nil, failure(CodeNotAFeed, "The address answered, but not with a Torznab or Newznab feed.")
	}
	c, err := ParseCaps(bytes.NewReader(body), s.now())
	if err != nil {
		return nil, failure(CodeNotAFeed, "The capabilities answer from the source could not be read.")
	}
	return c, nil
}

// searchRecord runs one query against one source.
func (s *Service) searchRecord(ctx context.Context, r record, q Query) ([]Item, error) {
	cats, ok := categoriesFor(r.Categories, q.Media)
	if !ok {
		return nil, errSkipped
	}
	key, err := s.open(r.KeySealed)
	if err != nil {
		return nil, failure(CodeKeyRefused, "The saved API key cannot be read (was the master key file replaced?). Enter it again.")
	}
	caps, err := s.ensureCaps(ctx, r, key)
	if err != nil {
		return nil, err
	}
	items, err := s.searchOnce(ctx, r, key, caps, q, cats)
	if err != nil {
		return nil, err
	}
	// An id search that found nothing is retried as a text search: plenty of
	// sources list a title without its ids.
	if len(items) == 0 && !q.NoIDs && strings.TrimSpace(q.Text) != "" {
		if _, _, used := BuildSearch(caps, q, cats); used {
			q.NoIDs = true
			return s.searchOnce(ctx, r, key, caps, q, cats)
		}
	}
	return items, nil
}

func (s *Service) searchOnce(ctx context.Context, r record, key string, caps *Caps, q Query, cats []int) ([]Item, error) {
	_, params, _ := BuildSearch(caps, q, cats)
	body, err := s.fetch(ctx, r, key, params)
	if err != nil {
		return nil, err
	}
	items, err := parseFeedBytes(body, r.Kind, r.ID)
	if err != nil {
		return nil, err
	}
	return items, nil
}

// errSkipped marks a source that was not asked because it does not carry that
// kind of media.
var errSkipped = errors.New("sources: source does not carry this kind of media")

// Outcome is what one source did with one search.
type Outcome struct {
	Indexer Indexer
	Items   []Item
	Err     error // plain words; nil on success
	Skipped bool  // not asked: it does not carry this kind of media
}

// SearchAll asks every enabled source (at most SearchConcurrency at a time,
// each given SearchTimeout) and returns one Outcome per source, best priority
// first. A source that fails does not stop the others; its error is recorded
// on it so the owner sees it in the list.
func (s *Service) SearchAll(ctx context.Context, q Query) ([]Outcome, error) {
	recs, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	var todo []record
	for _, r := range recs {
		if r.Enabled {
			todo = append(todo, r)
		}
	}
	return s.fanOut(ctx, todo, q), nil
}

func (s *Service) fanOut(ctx context.Context, recs []record, q Query) []Outcome {
	out := make([]Outcome, len(recs))
	sem := make(chan struct{}, SearchConcurrency)
	var wg sync.WaitGroup
	for i, r := range recs {
		out[i].Indexer = s.public(r)
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				out[i].Err = ctx.Err()
				return
			}
			cctx, cancel := context.WithTimeout(ctx, SearchTimeout)
			defer cancel()
			items, err := s.searchRecord(cctx, r, q)
			switch {
			case errors.Is(err, errSkipped):
				out[i].Skipped = true
			case err != nil:
				if errors.Is(err, context.DeadlineExceeded) {
					err = failure(CodeTimeout, "The source took too long to answer.")
				}
				out[i].Err = err
				if ctx.Err() == nil {
					s.noteError(r.ID, err)
				}
			default:
				out[i].Items = items
				if ctx.Err() == nil {
					s.noteOK(r.ID)
				}
			}
		}()
	}
	wg.Wait()
	sort.SliceStable(out, func(a, b int) bool { return out[a].Indexer.Priority < out[b].Indexer.Priority })
	return out
}

// noteOK and noteError keep the owner's list of sources honest. They use their
// own short context so a cancelled search still records what it learned.
func (s *Service) noteOK(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.MarkOK(ctx, id, s.now()); err != nil {
		s.log.Warn("could not record a source's answer", "source", id, "err", err)
	}
}

func (s *Service) noteError(id int64, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.MarkError(ctx, id, s.now(), err.Error()); err != nil {
		s.log.Warn("could not record a source's failure", "source", id, "err", err)
	}
	s.log.Warn("source failed", "source", id, "err", err)
}

// Search asks one source.
func (s *Service) Search(ctx context.Context, id int64, q Query) ([]Item, error) {
	r, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, SearchTimeout)
	defer cancel()
	items, err := s.searchRecord(cctx, r, q)
	switch {
	case errors.Is(err, errSkipped):
		return nil, nil
	case err != nil:
		s.noteError(id, err)
		return nil, err
	}
	s.noteOK(id)
	return items, nil
}

// ---- Test --------------------------------------------------------------------

// TestResult is the answer to "does this source work?".
type TestResult struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Caps    *Caps  `json:"caps,omitempty"`
	Ms      int64  `json:"ms"`
}

// Test fetches the source's capabilities afresh, then asks for one item to prove
// the key works. The answer is recorded on the source.
func (s *Service) Test(ctx context.Context, id int64) (TestResult, error) {
	r, err := s.store.Get(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	start := time.Now()
	res := TestResult{}
	fail := func(err error) (TestResult, error) {
		var se *Error
		if errors.As(err, &se) {
			res.Code, res.Message = se.Code, se.Message
		} else if ctx.Err() != nil {
			return TestResult{}, ctx.Err()
		} else {
			res.Code, res.Message = CodeSourceError, "The test failed."
		}
		res.Ms = time.Since(start).Milliseconds()
		s.noteError(id, errors.New(res.Message))
		return res, nil
	}

	key, err := s.open(r.KeySealed)
	if err != nil {
		return fail(failure(CodeKeyRefused, "The saved API key cannot be read (was the master key file replaced?). Enter it again."))
	}
	cctx, cancel := context.WithTimeout(ctx, SearchTimeout)
	defer cancel()
	caps, err := s.fetchCaps(cctx, r, key)
	if err != nil {
		return fail(err)
	}
	if err := s.store.SaveCaps(ctx, id, caps); err != nil {
		return TestResult{}, err
	}
	// The capabilities answer is often public; a search proves the key.
	probe := Query{Limit: 1}
	_, params, _ := BuildSearch(caps, probe, r.Categories)
	body, err := s.fetch(cctx, r, key, params)
	if err == nil {
		_, err = parseFeedBytes(body, r.Kind, r.ID)
	}
	if err != nil {
		return fail(err)
	}
	s.noteOK(id)
	res.OK, res.Caps = true, caps
	res.Message = capsSummary(caps)
	res.Ms = time.Since(start).Milliseconds()
	return res, nil
}

// capsSummary says in plain words what a source can do.
func capsSummary(c *Caps) string {
	var kinds []string
	if c.ModeAvailable(ModeMovie) {
		kinds = append(kinds, "movies")
	}
	if c.ModeAvailable(ModeTV) {
		kinds = append(kinds, "TV")
	}
	if c.ModeAvailable(ModeMusic) {
		kinds = append(kinds, "music")
	}
	msg := "Works."
	if len(kinds) > 0 {
		msg += " Searches for " + strings.Join(kinds, ", ") + "."
	} else {
		msg += " Text search only."
	}
	var ids []string
	for _, p := range []struct{ param, label string }{
		{"imdbid", "IMDb"}, {"tmdbid", "TMDB"}, {"tvmazeid", "TVmaze"}, {"tvdbid", "TheTVDB"},
	} {
		for _, m := range []string{ModeMovie, ModeTV} {
			if c.Supports(m, p.param) {
				ids = append(ids, p.label)
				break
			}
		}
	}
	if len(ids) > 0 {
		msg += " Can search by " + strings.Join(ids, ", ") + " id."
	}
	return msg
}
