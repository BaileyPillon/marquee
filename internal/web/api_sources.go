package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/decide"
	"github.com/BaileyPillon/marquee/internal/search"
	"github.com/BaileyPillon/marquee/internal/sources"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// registerSources adds the routes for sources, quality profiles, the blocklist
// and release search. Each group is added only when its service is there, so a
// test server without them answers with the ordinary JSON 404.
func (s *Server) registerSources(api *http.ServeMux) {
	owner := guard{roles: []accounts.Role{accounts.RoleOwner}, scope: accounts.ScopeSettings}
	manage := guard{roles: []accounts.Role{accounts.RoleOwner, accounts.RoleAdmin}, scope: accounts.ScopeAdmin}
	download := guard{roles: []accounts.Role{accounts.RoleOwner, accounts.RoleAdmin}, scope: accounts.ScopeDownloads}

	if s.Sources != nil {
		api.HandleFunc("GET /api/v1/sources", s.guarded(owner, s.handleSourceList))
		api.HandleFunc("POST /api/v1/sources", s.guarded(owner, s.handleSourceCreate))
		api.HandleFunc("POST /api/v1/sources/import", s.guarded(owner, s.handleSourceImport))
		api.HandleFunc("PUT /api/v1/sources/{id}", s.guarded(owner, s.handleSourceUpdate))
		api.HandleFunc("DELETE /api/v1/sources/{id}", s.guarded(owner, s.handleSourceDelete))
		api.HandleFunc("POST /api/v1/sources/{id}/test", s.guarded(owner, s.handleSourceTest))
	}
	if s.Decide != nil {
		api.HandleFunc("GET /api/v1/profiles", s.guarded(owner, s.handleProfileList))
		api.HandleFunc("PUT /api/v1/profiles/{id}", s.guarded(owner, s.handleProfileUpdate))
		api.HandleFunc("GET /api/v1/blocklist", s.guarded(owner, s.handleBlocklist))
		api.HandleFunc("DELETE /api/v1/blocklist/{id}", s.guarded(owner, s.handleBlocklistDelete))
	}
	if s.Search != nil {
		api.HandleFunc("GET /api/v1/titles/{id}/releases", s.guarded(manage, s.handleTitleReleases))
		api.HandleFunc("POST /api/v1/titles/{id}/grab", s.guarded(download, s.handleTitleGrab))
	}
}

// ---- errors ------------------------------------------------------------------

// sourcesError maps what the sources and decide modules return to an API error.
func (s *Server) sourcesError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		ie  *sources.InvalidError
		de  *decide.InvalidError
		se  *sources.Error
		sie *search.InvalidError
	)
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, "invalid_request", ie.Message)
	case errors.As(err, &de):
		writeError(w, http.StatusBadRequest, "invalid_request", de.Message)
	case errors.As(err, &sie):
		writeError(w, http.StatusBadRequest, "invalid_request", sie.Message)
	case errors.Is(err, sources.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such source.")
	case errors.Is(err, sources.ErrDuplicate):
		writeError(w, http.StatusConflict, "duplicate", "You already added a source with that address.")
	case errors.Is(err, decide.ErrDuplicateName):
		writeError(w, http.StatusConflict, "duplicate", "Another profile for that kind already has that name.")
	case errors.Is(err, decide.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such profile or blocklist entry.")
	case errors.As(err, &se):
		status := http.StatusBadGateway
		if se.Code == sources.CodeTimeout {
			status = http.StatusGatewayTimeout
		}
		writeError(w, status, se.Code, se.Message)
	case r.Context().Err() != nil:
		// The client went away; there is nobody to answer.
	default:
		s.internalError(w, r, err)
	}
}

// searchError maps what the search module returns.
func (s *Server) searchError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, titles.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such title.")
	case errors.Is(err, search.ErrNoGrabber):
		writeError(w, http.StatusConflict, "no_grabber", "No download client is set up yet. Add one in Settings, then try again.")
	case errors.Is(err, search.ErrNoSources):
		writeError(w, http.StatusConflict, "no_sources", "No source is set up yet. Add one in Settings, then try again.")
	case errors.Is(err, search.ErrNoCopy):
		writeError(w, http.StatusConflict, "no_copy", "This title is not set up in that quality yet. Add or ask for that copy first.")
	case errors.Is(err, search.ErrReleaseGone):
		writeError(w, http.StatusNotFound, "release_gone", "That release is no longer in the list. Search again.")
	case errors.Is(err, decide.ErrNotFound):
		writeError(w, http.StatusConflict, "no_profile", "There is no quality profile for that kind of title.")
	default:
		s.sourcesError(w, r, err)
	}
}

// ---- sources -----------------------------------------------------------------

type sourceBody struct {
	Name       *string `json:"name"`
	Kind       *string `json:"kind"`
	URL        *string `json:"url"`
	APIKey     *string `json:"apiKey"`
	Categories *[]int  `json:"categories"`
	Enabled    *bool   `json:"enabled"`
	Priority   *int    `json:"priority"`
}

func strValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Server) handleSourceList(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	list, err := s.Sources.List(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": list})
}

func (s *Server) handleSourceCreate(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	var in sourceBody
	if !decodeJSON(w, r, &in) {
		return
	}
	var cats []int
	if in.Categories != nil {
		cats = *in.Categories
	}
	prio := 0
	if in.Priority != nil {
		prio = *in.Priority
	}
	ix, err := s.Sources.Create(r.Context(), sources.Input{
		Name: strValue(in.Name), Kind: strValue(in.Kind), URL: strValue(in.URL), APIKey: strValue(in.APIKey),
		Categories: cats, Enabled: in.Enabled, Priority: prio,
	})
	if err != nil {
		s.sourcesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"source": ix})
}

func (s *Server) handleSourceUpdate(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such source.")
		return
	}
	var in sourceBody
	if !decodeJSON(w, r, &in) {
		return
	}
	ix, err := s.Sources.Update(r.Context(), id, sources.Patch{
		Name: in.Name, Kind: in.Kind, URL: in.URL, APIKey: in.APIKey,
		Categories: in.Categories, Enabled: in.Enabled, Priority: in.Priority,
	})
	if err != nil {
		s.sourcesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": ix})
}

func (s *Server) handleSourceDelete(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such source.")
		return
	}
	if err := s.Sources.Delete(r.Context(), id); err != nil {
		s.sourcesError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/sources/{id}/test: a source that does not work is still a 200;
// the body says what is wrong.
func (s *Server) handleSourceTest(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such source.")
		return
	}
	res, err := s.Sources.Test(r.Context(), id)
	if err != nil {
		s.sourcesError(w, r, err)
		return
	}
	ix, err := s.Sources.Get(r.Context(), id)
	if err != nil {
		s.sourcesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": res.OK, "code": res.Code, "message": res.Message, "ms": res.Ms, "caps": res.Caps, "source": ix,
	})
}

// POST /api/v1/sources/import {from, url, apiKey}
func (s *Server) handleSourceImport(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	var in struct {
		From   string `json:"from"`
		URL    string `json:"url"`
		APIKey string `json:"apiKey"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	res, err := s.Sources.Import(r.Context(), in.From, in.URL, in.APIKey)
	if err != nil {
		s.sourcesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"sources": res.Created, "skipped": res.Skipped})
}

// ---- profiles and blocklist ----------------------------------------------------

func (s *Server) handleProfileList(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	list, err := s.Decide.Profiles(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": list})
}

// PUT /api/v1/profiles/{id}: send the fields to change. A field left out keeps
// its value; a size sent as null is cleared.
func (s *Server) handleProfileUpdate(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such profile.")
		return
	}
	cur, err := s.Decide.ProfileByID(r.Context(), id)
	if err != nil {
		s.sourcesError(w, r, err)
		return
	}
	in := decide.InputOf(cur)
	if !decodeJSON(w, r, &in) {
		return
	}
	p, err := s.Decide.UpdateProfile(r.Context(), id, in)
	if err != nil {
		s.sourcesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": p})
}

func (s *Server) handleBlocklist(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	limit, offset := 100, 0
	for name, dst := range map[string]*int{"limit": &limit, "offset": &offset} {
		if v := r.URL.Query().Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "invalid_request", name+" must be a number.")
				return
			}
			*dst = n
		}
	}
	items, total, err := s.Decide.Blocklist(r.Context(), limit, offset)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleBlocklistDelete(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such blocklist entry.")
		return
	}
	if err := s.Decide.Unblock(r.Context(), id); err != nil {
		s.sourcesError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- releases and grabbing -----------------------------------------------------

type foundReleaseJSON struct {
	Indexer     releaseIndexerJSON `json:"indexer"`
	GUID        string             `json:"guid"`
	Title       string             `json:"title"`
	Size        int64              `json:"size"`
	Protocol    string             `json:"protocol"`
	Seeders     *int               `json:"seeders"`
	Age         *int               `json:"age"` // whole days
	PublishedAt *time.Time         `json:"publishedAt"`
	Quality     string             `json:"quality"`
	Score       int                `json:"score"`
	Accepted    bool               `json:"accepted"`
	Reasons     []string           `json:"reasons"`
	Rejection   string             `json:"rejection"`
}

type releaseIndexerJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type indexerFailureJSON struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

// GET /api/v1/titles/{id}/releases?quality=hd|uhd&season=&episode=
//
// It searches every enabled source now. The answer never carries a download
// link; POST .../grab picks one of the releases by its source and guid.
func (s *Server) handleTitleReleases(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such title.")
		return
	}
	q := r.URL.Query()
	quality := q.Get("quality")
	if quality == "" {
		quality = "hd"
	}
	var season, episode int
	for name, dst := range map[string]*int{"season": &season, "episode": &episode} {
		if v := q.Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "invalid_request", name+" must be a number.")
				return
			}
			*dst = n
		}
	}
	res, err := s.Search.Interactive(r.Context(), id, quality, season, episode)
	if err != nil {
		s.searchError(w, r, err)
		return
	}
	now := time.Now()
	out := make([]foundReleaseJSON, len(res.Results))
	for i, x := range res.Results {
		rj := foundReleaseJSON{
			Indexer: releaseIndexerJSON{ID: x.IndexerID, Name: x.IndexerName}, GUID: x.GUID, Title: x.Title, Size: x.Size,
			Protocol: x.Protocol, Quality: x.Quality, Score: x.Score, Accepted: x.Accepted,
			Reasons: x.Reasons, Rejection: x.Rejection,
		}
		if x.Seeders >= 0 {
			n := x.Seeders
			rj.Seeders = &n
		}
		if !x.PublishedAt.IsZero() {
			t := x.PublishedAt
			days := max(int(now.Sub(t).Hours()/24), 0)
			rj.PublishedAt, rj.Age = &t, &days
		}
		if rj.Reasons == nil {
			rj.Reasons = []string{}
		}
		out[i] = rj
	}
	failed := make([]indexerFailureJSON, len(res.IndexersFailed))
	for i, f := range res.IndexersFailed {
		failed[i] = indexerFailureJSON{ID: f.ID, Name: f.Name, Error: f.Error}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results": out, "searchedAt": res.SearchedAt, "indexersSearched": res.IndexersSearched, "indexersFailed": failed,
	})
}

// POST /api/v1/titles/{id}/grab {indexerId, guid, quality}
func (s *Server) handleTitleGrab(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such title.")
		return
	}
	var in struct {
		IndexerID int64  `json:"indexerId"`
		GUID      string `json:"guid"`
		Quality   string `json:"quality"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Quality == "" {
		in.Quality = "hd"
	}
	res, err := s.Search.GrabRelease(r.Context(), id, in.IndexerID, in.GUID, in.Quality)
	if err != nil {
		s.searchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"downloadId": res.DownloadID,
		"release": map[string]any{
			"title": res.Title, "quality": res.Quality, "score": res.Score, "reasons": res.Reasons,
		},
	})
}
