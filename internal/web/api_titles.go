package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// registerTitles adds the catalog, search, metadata-status and artwork routes.
// Without the services (a test server that does not need them) it adds
// nothing, and those paths fall through to the JSON 404.
func (s *Server) registerTitles(api *http.ServeMux) {
	if s.Titles == nil || s.Metadata == nil {
		return
	}
	see := guard{scope: accounts.ScopeSee}
	manage := guard{roles: []accounts.Role{accounts.RoleOwner, accounts.RoleAdmin}, scope: accounts.ScopeAdmin}
	remove := guard{roles: []accounts.Role{accounts.RoleOwner}, scope: accounts.ScopeDelete}

	api.HandleFunc("GET /api/v1/search", s.guarded(see, s.handleSearch))
	api.HandleFunc("GET /api/v1/titles", s.guarded(see, s.handleTitleList))
	api.HandleFunc("POST /api/v1/titles", s.guarded(manage, s.handleTitleAdd))
	api.HandleFunc("GET /api/v1/titles/{id}", s.guarded(see, s.handleTitleGet))
	api.HandleFunc("PATCH /api/v1/titles/{id}", s.guarded(manage, s.handleTitlePatch))
	api.HandleFunc("DELETE /api/v1/titles/{id}", s.guarded(remove, s.handleTitleDelete))
	api.HandleFunc("POST /api/v1/titles/{id}/refresh", s.guarded(manage, s.handleTitleRefresh))
	api.HandleFunc("GET /api/v1/metadata/status", s.guarded(see, s.handleMetadataStatus))
	api.HandleFunc("GET /api/v1/artwork/{provider}/{rest...}", s.guarded(see, s.handleArtwork))
}

// ---- errors ----------------------------------------------------------------

// titlesError maps what the catalog and metadata modules return to an API
// error. The codes are stable; the UI keys off them.
func (s *Server) titlesError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		me *metadata.Error
		ie *titles.InvalidError
	)
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, "invalid_request", ie.Message)
	case errors.Is(err, titles.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such title.")
	case errors.As(err, &me):
		status := http.StatusBadGateway
		switch me.Code {
		case metadata.ErrTMDBKeyMissing.Code, metadata.ErrTMDBKeyInvalid.Code:
			// The owner has something to set up. Not a 401 or 403: the UI
			// would take either for "signed out".
			status = http.StatusConflict
		case metadata.ErrNotFound.Code:
			status = http.StatusNotFound
		case metadata.ErrBadArtwork.Code:
			status = http.StatusBadRequest
		}
		writeError(w, status, me.Code, me.Message)
	case r.Context().Err() != nil:
		// The client went away; there is nobody to answer.
	default:
		s.internalError(w, r, err)
	}
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// ---- search ----------------------------------------------------------------

const (
	maxQueryRunes = 200
	searchTimeout = 25 * time.Second
)

// handleSearch looks candidates up at the providers without saving anything.
// The answer is grouped by kind, and each group is either {"results": [...]} or
// {"error": "<code>", "message": "..."}, so one provider being down (or TMDB
// having no key) does not hide the others.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "all"
	}
	var kinds []string
	switch kind {
	case "all":
		kinds = []string{metadata.KindMovie, metadata.KindShow, metadata.KindArtist}
	case metadata.KindMovie, metadata.KindShow, metadata.KindArtist:
		kinds = []string{kind}
	default:
		writeError(w, http.StatusBadRequest, "invalid_kind", "kind must be movie, show, artist or all.")
		return
	}
	if query == "" {
		writeError(w, http.StatusBadRequest, "invalid_query", "Type something to search for.")
		return
	}
	if utf8.RuneCountInString(query) > maxQueryRunes {
		writeError(w, http.StatusBadRequest, "invalid_query", "That search is too long.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), searchTimeout)
	defer cancel()
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = make(map[string]any, len(kinds))
	)
	for _, k := range kinds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.Titles.Lookup(ctx, k, query)
			group := map[string]any{}
			switch {
			case err == nil:
				if res == nil {
					res = []titles.LookupResult{}
				}
				group["results"] = res
			default:
				group["error"], group["message"] = s.searchFailure(r, err)
			}
			mu.Lock()
			out[k] = group
			mu.Unlock()
		}()
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}

// searchFailure turns a failed lookup into the code and message of its group.
func (s *Server) searchFailure(r *http.Request, err error) (code, message string) {
	var ie *titles.InvalidError
	switch {
	case errors.As(err, &ie):
		return "invalid_request", ie.Message
	case metadata.CodeOf(err) != "":
		return metadata.CodeOf(err), metadata.MessageOf(err)
	case errors.Is(err, context.DeadlineExceeded):
		return metadata.ErrUpstream.Code, "The provider took too long to answer."
	}
	s.log.Error("search failed", "id", RequestID(r.Context()), "err", err)
	return "internal", "Something went wrong."
}

// ---- titles ----------------------------------------------------------------

// providerID reads an id that may arrive as a JSON string or number: TMDB and
// TVmaze ids are numbers, a MusicBrainz id is a UUID.
type providerID string

func (p *providerID) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*p = providerID(s)
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var n json.Number
	if err := dec.Decode(&n); err != nil {
		return errors.New("providerId must be a string or a number")
	}
	*p = providerID(n.String())
	return nil
}

func (s *Server) handleTitleAdd(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	var in struct {
		Kind       string     `json:"kind"`
		Provider   string     `json:"provider"`
		ProviderID providerID `json:"providerId"`
		Monitored  *bool      `json:"monitored"`
		RootFolder string     `json:"rootFolder"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	monitored := true
	if in.Monitored != nil {
		monitored = *in.Monitored
	}
	opts := titles.AddOptions{Monitored: monitored, RootFolder: in.RootFolder}
	if in.Kind == titles.KindArtist {
		s.handleArtistAdd(w, r, in.Provider, string(in.ProviderID), opts)
		return
	}
	d, created, err := s.Titles.Add(r.Context(), in.Kind, in.Provider, string(in.ProviderID), opts)
	if err != nil {
		s.titlesError(w, r, err)
		return
	}
	status := http.StatusOK // already there: same answer, nothing created
	if created {
		status = http.StatusCreated
	}
	s.writeDetail(w, r, status, d)
}

// handleArtistAdd stores the artist at once and answers 202 {"title", "hydrating": true};
// the albums and tracks load in the background, and a "titles" event
// {"id", "event": "hydrated"} goes out on the event stream when they are in.
// An artist that is already complete answers 200 with "hydrating": false.
func (s *Server) handleArtistAdd(w http.ResponseWriter, r *http.Request, provider, providerID string, opts titles.AddOptions) {
	d, created, hydrating, err := s.Titles.AddAsync(r.Context(), titles.KindArtist, provider, providerID, opts)
	if err != nil {
		s.titlesError(w, r, err)
		return
	}
	view, err := s.detailView(r.Context(), d)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	status := http.StatusOK
	switch {
	case hydrating:
		status = http.StatusAccepted
	case created:
		status = http.StatusCreated // the provider had no quick answer: it was added whole
	}
	writeJSON(w, status, artistAdded{Title: view, Hydrating: hydrating})
}

// writeDetail answers with a title and what is on disk for it.
func (s *Server) writeDetail(w http.ResponseWriter, r *http.Request, status int, d *titles.Detail) {
	view, err := s.detailView(r.Context(), d)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, status, view)
}

func (s *Server) handleTitleList(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	q := r.URL.Query()
	f := titles.ListFilter{Kind: q.Get("kind"), Q: q.Get("q"), Sort: q.Get("sort")}
	for name, dst := range map[string]*int{"limit": &f.Limit, "offset": &f.Offset} {
		v := q.Get(name)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", name+" must be a number.")
			return
		}
		*dst = n
	}
	if v := q.Get("monitored"); v != "" {
		m, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "monitored must be true or false.")
			return
		}
		f.Monitored = &m
	}
	res, err := s.Titles.List(r.Context(), f)
	if err != nil {
		s.titlesError(w, r, err)
		return
	}
	view, err := s.listView(r.Context(), res)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleTitleGet(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such title.")
		return
	}
	d, err := s.Titles.Get(r.Context(), id)
	if err != nil {
		s.titlesError(w, r, err)
		return
	}
	s.writeDetail(w, r, http.StatusOK, d)
}

func (s *Server) handleTitlePatch(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such title.")
		return
	}
	var in struct {
		Monitored *bool `json:"monitored"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Monitored == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "monitored is required.")
		return
	}
	if err := s.Titles.SetMonitored(r.Context(), id, *in.Monitored); err != nil {
		s.titlesError(w, r, err)
		return
	}
	d, err := s.Titles.Get(r.Context(), id)
	if err != nil {
		s.titlesError(w, r, err)
		return
	}
	s.writeDetail(w, r, http.StatusOK, d)
}

// handleTitleDelete removes the catalog rows of a title. It never deletes files.
func (s *Server) handleTitleDelete(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such title.")
		return
	}
	if err := s.Titles.Delete(r.Context(), id); err != nil {
		s.titlesError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTitleRefresh(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such title.")
		return
	}
	d, err := s.Titles.Refresh(r.Context(), id)
	if err != nil {
		s.titlesError(w, r, err)
		return
	}
	s.writeDetail(w, r, http.StatusOK, d)
}

// ---- metadata status -------------------------------------------------------

func (s *Server) handleMetadataStatus(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	writeJSON(w, http.StatusOK, s.Metadata.Status(r.Context()))
}
