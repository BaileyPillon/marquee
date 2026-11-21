package web

import (
	"errors"
	"net/http"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/mediaservers"
)

// registerMediaServers adds the media-server routes. They hold the tokens of
// other services, so they are for the owner only. Without the service they add
// nothing and fall through to the JSON 404.
func (s *Server) registerMediaServers(api *http.ServeMux) {
	if s.MediaServers == nil {
		return
	}
	owner := guard{roles: []accounts.Role{accounts.RoleOwner}, scope: accounts.ScopeSettings}
	api.HandleFunc("GET /api/v1/mediaservers", s.guarded(owner, s.handleMediaServerList))
	api.HandleFunc("POST /api/v1/mediaservers", s.guarded(owner, s.handleMediaServerAdd))
	api.HandleFunc("PUT /api/v1/mediaservers/{id}", s.guarded(owner, s.handleMediaServerUpdate))
	api.HandleFunc("DELETE /api/v1/mediaservers/{id}", s.guarded(owner, s.handleMediaServerDelete))
	api.HandleFunc("POST /api/v1/mediaservers/{id}/test", s.guarded(owner, s.handleMediaServerTest))
}

// mediaServersError maps what the module returns to an API error. The codes are
// stable; the UI keys off them.
func (s *Server) mediaServersError(w http.ResponseWriter, r *http.Request, err error) {
	var ie *mediaservers.InvalidError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, "invalid_request", ie.Message)
	case errors.Is(err, mediaservers.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such media server.")
	case errors.Is(err, mediaservers.ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Another media server already has that name.")
	case r.Context().Err() != nil:
		// The client went away; there is nobody to answer.
	default:
		s.internalError(w, r, err)
	}
}

// GET /api/v1/mediaservers: tokens come back masked.
func (s *Server) handleMediaServerList(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	list, err := s.MediaServers.List(r.Context())
	if err != nil {
		s.mediaServersError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": list})
}

// POST /api/v1/mediaservers
func (s *Server) handleMediaServerAdd(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	var in mediaservers.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	v, err := s.MediaServers.Add(r.Context(), in)
	if err != nil {
		s.mediaServersError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"server": v})
}

// PUT /api/v1/mediaservers/{id}: fields left out keep their value; a token left
// out (or still masked) keeps the stored one.
func (s *Server) handleMediaServerUpdate(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such media server.")
		return
	}
	var in mediaservers.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	v, err := s.MediaServers.Update(r.Context(), id, in)
	if err != nil {
		s.mediaServersError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": v})
}

// DELETE /api/v1/mediaservers/{id}
func (s *Server) handleMediaServerDelete(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such media server.")
		return
	}
	if err := s.MediaServers.Delete(r.Context(), id); err != nil {
		s.mediaServersError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/mediaservers/{id}/test: always 200; ok says whether the server
// answered. {ok, version, error, libraries: [{id, name, paths}]}
func (s *Server) handleMediaServerTest(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such media server.")
		return
	}
	res, err := s.MediaServers.Test(r.Context(), id)
	if err != nil {
		s.mediaServersError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
