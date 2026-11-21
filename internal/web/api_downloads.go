package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/downloads"
)

// registerDownloads adds the download and download-client routes. Without the
// downloads service (a test server that does not need it) it adds nothing, and
// those paths fall through to the JSON 404.
func (s *Server) registerDownloads(api *http.ServeMux) {
	if s.Downloads == nil {
		return
	}
	// Downloads: owner and admins, and API keys with the "downloads" tick.
	control := guard{roles: []accounts.Role{accounts.RoleOwner, accounts.RoleAdmin}, scope: accounts.ScopeDownloads}
	// Clients hold the passwords of other services: owner only.
	owner := guard{roles: []accounts.Role{accounts.RoleOwner}, scope: accounts.ScopeSettings}

	api.HandleFunc("GET /api/v1/downloads", s.guarded(control, s.handleDownloadList))
	api.HandleFunc("GET /api/v1/downloads/summary", s.guarded(control, s.handleDownloadSummary))
	api.HandleFunc("POST /api/v1/downloads/{id}/pause", s.guarded(control, s.handleDownloadPause))
	api.HandleFunc("POST /api/v1/downloads/{id}/resume", s.guarded(control, s.handleDownloadResume))
	api.HandleFunc("DELETE /api/v1/downloads/{id}", s.guarded(control, s.handleDownloadRemove))

	api.HandleFunc("GET /api/v1/clients", s.guarded(owner, s.handleClientList))
	api.HandleFunc("POST /api/v1/clients", s.guarded(owner, s.handleClientAdd))
	api.HandleFunc("PUT /api/v1/clients/{id}", s.guarded(owner, s.handleClientUpdate))
	api.HandleFunc("DELETE /api/v1/clients/{id}", s.guarded(owner, s.handleClientDelete))
	api.HandleFunc("POST /api/v1/clients/{id}/test", s.guarded(owner, s.handleClientTest))
}

// downloadsError maps what the downloads module returns to an API error. The
// codes are stable; the UI keys off them.
func (s *Server) downloadsError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		ie *downloads.InvalidError
		de *downloads.Error
	)
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, "invalid_request", ie.Message)
	case errors.Is(err, downloads.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such download.")
	case errors.Is(err, downloads.ErrClientNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such download client.")
	case errors.As(err, &de):
		status := http.StatusBadGateway
		switch de.Code {
		case downloads.ErrInvalidState.Code, downloads.ErrClientInUse.Code, downloads.ErrNameTaken.Code:
			status = http.StatusConflict
		case downloads.ErrNoClient.Code:
			status = http.StatusConflict
		}
		writeError(w, status, de.Code, de.Message)
	case r.Context().Err() != nil:
		// The client went away; there is nobody to answer.
	default:
		s.internalError(w, r, err)
	}
}

// ---- downloads -------------------------------------------------------------

const (
	defaultDownloadLimit = downloads.DefaultLimit
	maxDownloadLimit     = downloads.MaxLimit
)

// GET /api/v1/downloads?state=active|history|all&limit=&offset=
func (s *Server) handleDownloadList(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	q := r.URL.Query()
	f := downloads.ListFilter{State: q.Get("state")}
	switch f.State {
	case "", downloads.FilterActive, downloads.FilterHistory, downloads.FilterAll:
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "state must be active, history or all.")
		return
	}
	limit, ok := queryInt(w, q.Get("limit"), "limit", defaultDownloadLimit, 1, maxDownloadLimit)
	if !ok {
		return
	}
	offset, ok := queryInt(w, q.Get("offset"), "offset", 0, 0, 1<<40)
	if !ok {
		return
	}
	f.Limit, f.Offset = int(limit), int(offset)

	list, total, err := s.Downloads.List(r.Context(), f)
	if err != nil {
		s.downloadsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"downloads": list, "total": total, "limit": f.Limit, "offset": f.Offset,
	})
}

// GET /api/v1/downloads/summary
func (s *Server) handleDownloadSummary(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	sum, err := s.Downloads.Summary(r.Context())
	if err != nil {
		s.downloadsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// POST /api/v1/downloads/{id}/pause
func (s *Server) handleDownloadPause(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	s.downloadAction(w, r, s.Downloads.Pause)
}

// POST /api/v1/downloads/{id}/resume
func (s *Server) handleDownloadResume(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	s.downloadAction(w, r, s.Downloads.Resume)
}

func (s *Server) downloadAction(w http.ResponseWriter, r *http.Request, act func(context.Context, int64) (downloads.View, error)) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such download.")
		return
	}
	v, err := act(r.Context(), id)
	if err != nil {
		s.downloadsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"download": v})
}

// DELETE /api/v1/downloads/{id}?deleteData=true|false. The download leaves its
// client and stays in the history as "removed".
func (s *Server) handleDownloadRemove(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such download.")
		return
	}
	deleteData := false
	if v := r.URL.Query().Get("deleteData"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "deleteData must be true or false.")
			return
		}
		deleteData = b
	}
	if err := s.Downloads.Remove(r.Context(), id, deleteData); err != nil {
		s.downloadsError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- clients ---------------------------------------------------------------

// GET /api/v1/clients: secrets come back masked.
func (s *Server) handleClientList(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	list, err := s.Downloads.Clients(r.Context())
	if err != nil {
		s.downloadsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": list})
}

// POST /api/v1/clients
func (s *Server) handleClientAdd(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	var in downloads.ClientInput
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := s.Downloads.AddClient(r.Context(), in)
	if err != nil {
		s.downloadsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"client": c})
}

// PUT /api/v1/clients/{id}: fields left out keep their value; a password or key
// left out (or still masked) keeps the stored secret.
func (s *Server) handleClientUpdate(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such download client.")
		return
	}
	var in downloads.ClientInput
	if !decodeJSON(w, r, &in) {
		return
	}
	c, err := s.Downloads.UpdateClient(r.Context(), id, in)
	if err != nil {
		s.downloadsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": c})
}

// DELETE /api/v1/clients/{id}
func (s *Server) handleClientDelete(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such download client.")
		return
	}
	if err := s.Downloads.DeleteClient(r.Context(), id); err != nil {
		s.downloadsError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/clients/{id}/test: always 200; ok says whether the client answered.
func (s *Server) handleClientTest(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such download client.")
		return
	}
	res, err := s.Downloads.TestClient(r.Context(), id)
	if err != nil {
		s.downloadsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
