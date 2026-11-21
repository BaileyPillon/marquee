package web

import (
	"errors"
	"net/http"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/alerts"
)

// registerAlerts adds the alert channel routes. They are the owner's: a channel
// holds a webhook URL, a token or a mail password. Without the service (a test
// server that does not need it) it adds nothing, and the paths fall through to
// the JSON 404.
func (s *Server) registerAlerts(api *http.ServeMux) {
	if s.Alerts == nil {
		return
	}
	owner := guard{roles: []accounts.Role{accounts.RoleOwner}, scope: accounts.ScopeSettings}
	api.HandleFunc("GET /api/v1/alerts", s.guarded(owner, s.handleAlertList))
	api.HandleFunc("POST /api/v1/alerts", s.guarded(owner, s.handleAlertCreate))
	api.HandleFunc("PUT /api/v1/alerts/{id}", s.guarded(owner, s.handleAlertUpdate))
	api.HandleFunc("DELETE /api/v1/alerts/{id}", s.guarded(owner, s.handleAlertDelete))
	api.HandleFunc("POST /api/v1/alerts/{id}/test", s.guarded(owner, s.handleAlertTest))
}

// alertsError maps what the alerts module returns to an API error. The codes are
// stable; the UI keys off them.
func (s *Server) alertsError(w http.ResponseWriter, r *http.Request, err error) {
	var ie *alerts.InvalidError
	switch {
	case errors.As(err, &ie):
		writeError(w, http.StatusBadRequest, ie.Code, ie.Message)
	case errors.Is(err, alerts.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such alert channel.")
	case errors.Is(err, alerts.ErrNameTaken):
		writeError(w, http.StatusConflict, "name_taken", "Another channel already has that name.")
	case r.Context().Err() != nil:
		// The client went away; there is nobody to answer.
	default:
		s.internalError(w, r, err)
	}
}

// alertBody is a channel as the owner sends it. A field left out keeps its value
// on a change; a secret that is still masked keeps the stored one.
type alertBody struct {
	Name    *string        `json:"name"`
	Kind    *string        `json:"kind"`
	Config  map[string]any `json:"config"`
	Events  *[]string      `json:"events"`
	Enabled *bool          `json:"enabled"`
}

func (b alertBody) input() alerts.Input {
	return alerts.Input{Name: b.Name, Kind: b.Kind, Config: b.Config, Events: b.Events, Enabled: b.Enabled}
}

// GET /api/v1/alerts -> {alerts: [...]}, secrets masked.
func (s *Server) handleAlertList(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	list, err := s.Alerts.List(r.Context())
	if err != nil {
		s.alertsError(w, r, err)
		return
	}
	if list == nil {
		list = []alerts.Channel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"alerts": list, "kinds": alerts.Kinds, "events": alerts.Events})
}

// POST /api/v1/alerts {name, kind, config, events?, enabled?} -> 201 {alert}
func (s *Server) handleAlertCreate(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	var in alertBody
	if !decodeJSON(w, r, &in) {
		return
	}
	ch, err := s.Alerts.Create(r.Context(), in.input())
	if err != nil {
		s.alertsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"alert": ch})
}

// PUT /api/v1/alerts/{id} -> {alert}
func (s *Server) handleAlertUpdate(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such alert channel.")
		return
	}
	var in alertBody
	if !decodeJSON(w, r, &in) {
		return
	}
	ch, err := s.Alerts.Update(r.Context(), id, in.input())
	if err != nil {
		s.alertsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"alert": ch})
}

// DELETE /api/v1/alerts/{id}
func (s *Server) handleAlertDelete(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such alert channel.")
		return
	}
	if err := s.Alerts.Delete(r.Context(), id); err != nil {
		s.alertsError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/alerts/{id}/test sends a test message. It always answers 200:
// {"ok": true, "error": null}, or {"ok": false, "error": "plain words"}.
func (s *Server) handleAlertTest(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such alert channel.")
		return
	}
	err := s.Alerts.Test(r.Context(), id)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "error": nil})
	case errors.Is(err, alerts.ErrNotFound):
		s.alertsError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
	}
}
