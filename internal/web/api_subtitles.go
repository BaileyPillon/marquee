package web

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/subtitles"
)

// registerSubtitles adds the subtitle routes. Without the service (a test
// server that does not need it) it adds nothing, and those paths fall through
// to the JSON 404.
//
// The settings go through the generic /settings/subtitles routes (owner only;
// the API key and the password are sealed and shown masked).
func (s *Server) registerSubtitles(api *http.ServeMux) {
	if s.Subtitles == nil {
		return
	}
	see := guard{scope: accounts.ScopeSee}
	manage := guard{roles: []accounts.Role{accounts.RoleOwner, accounts.RoleAdmin}, scope: accounts.ScopeAdmin}
	owner := guard{roles: []accounts.Role{accounts.RoleOwner}, scope: accounts.ScopeSettings}

	api.HandleFunc("GET /api/v1/subtitles/status", s.guarded(manage, s.handleSubtitleStatus))
	api.HandleFunc("POST /api/v1/subtitles/test", s.guarded(owner, s.handleSubtitleTest))
	api.HandleFunc("DELETE /api/v1/subtitles/{id}", s.guarded(manage, s.handleSubtitleDelete))
	api.HandleFunc("GET /api/v1/titles/{id}/subtitles", s.guarded(see, s.handleTitleSubtitles))
	api.HandleFunc("GET /api/v1/library/files/{id}/subtitles", s.guarded(see, s.handleFileSubtitles))
	api.HandleFunc("POST /api/v1/library/files/{id}/subtitles/search", s.guarded(manage, s.handleFileSubtitleSearch))
	api.HandleFunc("POST /api/v1/library/files/{id}/subtitles/download", s.guarded(manage, s.handleFileSubtitleDownload))
}

// subtitlesError maps what the subtitles module returns to an API error. The
// codes are stable; the UI keys off them. A provider that refuses the key is not
// a 401 or 403 (the UI would take either for "signed out").
func (s *Server) subtitlesError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		inv  *subtitles.InvalidError
		q    *subtitles.QuotaError
		rl   *subtitles.RateLimitError
		auth *subtitles.AuthError
		un   *subtitles.UnavailableError
	)
	switch {
	case errors.As(err, &inv):
		writeError(w, http.StatusBadRequest, "invalid_request", inv.Message)
	case errors.Is(err, subtitles.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such file or subtitle.")
	case errors.Is(err, subtitles.ErrNotVideo):
		writeError(w, http.StatusBadRequest, "not_video", "Only a movie or an episode in the library has subtitles.")
	case errors.Is(err, subtitles.ErrFileMissing):
		writeError(w, http.StatusConflict, "file_missing", "The video file is not on disk, so there is nothing to put a subtitle beside.")
	case errors.Is(err, subtitles.ErrNoProvider):
		writeError(w, http.StatusConflict, "no_provider", "No subtitle source is turned on. Turn one on in Settings, then Subtitles.")
	case errors.Is(err, subtitles.ErrForeignFile):
		writeError(w, http.StatusConflict, "file_exists", "A subtitle file with that name is already there and Marquee did not write it, so it was left alone.")
	case errors.Is(err, subtitles.ErrNotSubtitle):
		writeError(w, http.StatusBadGateway, "bad_subtitle", "The subtitle source sent something that is not a subtitle file.")
	case errors.As(err, &q):
		writeError(w, http.StatusTooManyRequests, "quota", q.Error()+".")
	case errors.As(err, &rl):
		if rl.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(rl.RetryAfter.Round(time.Second).Seconds())))
		}
		writeError(w, http.StatusTooManyRequests, "rate_limited", "The subtitle source asked Marquee to slow down. Try again in a minute.")
	case errors.As(err, &auth):
		writeError(w, http.StatusBadGateway, "provider_auth", auth.Message)
	case errors.As(err, &un):
		writeError(w, http.StatusBadGateway, "provider_failed", un.Message)
	case r.Context().Err() != nil:
		// The client went away; there is nobody to answer.
	default:
		s.internalError(w, r, err)
	}
}

// GET /api/v1/subtitles/status
// -> {"providers":[{"name","enabled","ok","quotaRemaining"?,"error"?,"blockedUntil"?}], "missingCount", "languages"}
func (s *Server) handleSubtitleStatus(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	st, err := s.Subtitles.Status(r.Context())
	if err != nil {
		s.subtitlesError(w, r, err)
		return
	}
	if st.Providers == nil {
		st.Providers = []subtitles.ProviderStatus{}
	}
	if st.Languages == nil {
		st.Languages = []string{}
	}
	writeJSON(w, http.StatusOK, st)
}

// POST /api/v1/subtitles/test {"provider","apiKey"?,"username"?,"password"?}
// Tries a provider with what was typed into the form (a field left empty or
// still masked uses the saved one) -> {"ok": true}.
func (s *Server) handleSubtitleTest(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	var in struct {
		Provider string `json:"provider"`
		APIKey   string `json:"apiKey"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	err := s.Subtitles.TestProvider(r.Context(), in.Provider, subtitles.OpenSubtitlesConfig{
		APIKey: in.APIKey, Username: in.Username, Password: in.Password,
	})
	if err != nil {
		s.subtitlesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DELETE /api/v1/subtitles/{id} removes a subtitle file Marquee wrote, and only
// such a file.
func (s *Server) handleSubtitleDelete(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such subtitle.")
		return
	}
	if err := s.Subtitles.Remove(r.Context(), id); err != nil {
		s.subtitlesError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/library/files/{id}/subtitles
// -> {"fileId","languages":[wanted],"subtitles":[{"id"?,"language","hi","forced"?,"provider"?,"score"?,"name","source","addedAt"?}],
//
//	"missing":[...],"canSearch"}
//
// Every signed-in person may read it; it names files, never folders.
func (s *Server) handleFileSubtitles(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such file.")
		return
	}
	res, err := s.Subtitles.ForFile(r.Context(), id)
	if err != nil {
		s.subtitlesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /api/v1/titles/{id}/subtitles
// -> {"files":[{"fileId","name","episodeIds":[...],"languages","subtitles":[...],"missing","canSearch"}]}
// The subtitles of every video file of a movie or show, for its page. Every
// signed-in person may read it; it names files, never folders.
func (s *Server) handleTitleSubtitles(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such title.")
		return
	}
	files, err := s.Subtitles.ForTitle(r.Context(), id)
	if err != nil {
		s.subtitlesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

type subtitleResultJSON struct {
	Provider   string   `json:"provider"`
	ProviderID string   `json:"providerId"`
	Language   string   `json:"language"`
	Release    string   `json:"release"`
	Releases   []string `json:"releases"`
	HI         bool     `json:"hi"`
	Downloads  int      `json:"downloads"`
	HashMatch  bool     `json:"hashMatch"`
	Score      int      `json:"score"`
	// Accepted: the score clears the owner's minimum, so an automatic search
	// would take it.
	Accepted bool     `json:"accepted"`
	Reasons  []string `json:"reasons"`
}

// POST /api/v1/library/files/{id}/subtitles/search {"language"}
// -> {"language","minScore","results":[...best first, with scores],"providers":[{"name","ok","error"?,"skipped"?}]}
// It asks every provider that is turned on, now. Nothing is downloaded.
func (s *Server) handleFileSubtitleSearch(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such file.")
		return
	}
	var in struct {
		Language string `json:"language"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	res, err := s.Subtitles.Search(r.Context(), id, in.Language)
	if err != nil {
		s.subtitlesError(w, r, err)
		return
	}
	out := make([]subtitleResultJSON, len(res.Results))
	for i, c := range res.Results {
		x := subtitleResultJSON{
			Provider: c.Provider, ProviderID: c.ProviderID, Language: c.Language, Releases: c.Releases, HI: c.HI,
			Downloads: c.Downloads, HashMatch: c.HashMatch, Score: c.Score, Accepted: c.Score >= res.MinScore && c.Score > 0, Reasons: c.Reasons,
		}
		if len(c.Releases) > 0 {
			x.Release = c.Releases[0]
		}
		if x.Releases == nil {
			x.Releases = []string{}
		}
		if x.Reasons == nil {
			x.Reasons = []string{}
		}
		out[i] = x
	}
	if res.Sources == nil {
		res.Sources = []subtitles.ProviderOutcome{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"language": res.Language, "minScore": res.MinScore, "results": out, "providers": res.Sources,
	})
}

// POST /api/v1/library/files/{id}/subtitles/download {"provider","providerId","language"}
// -> 201 {"subtitle":{"id","fileId","language","hi","provider","providerId","score","name","createdAt"}}
// The subtitle is saved next to the video. A subtitle file with that name that
// Marquee did not write is never replaced (409 file_exists).
func (s *Server) handleFileSubtitleDownload(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such file.")
		return
	}
	var in struct {
		Provider   string `json:"provider"`
		ProviderID string `json:"providerId"`
		Language   string `json:"language"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	sub, err := s.Subtitles.Download(r.Context(), id, strings.TrimSpace(in.Provider), strings.TrimSpace(in.ProviderID), in.Language)
	if err != nil {
		s.subtitlesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"subtitle": map[string]any{
		"id": sub.ID, "fileId": sub.FileID, "language": sub.Language, "hi": sub.HI, "provider": sub.Provider,
		"providerId": sub.ProviderID, "score": sub.Score, "name": filepath.Base(sub.Path), "createdAt": sub.CreatedAt,
	}})
}
