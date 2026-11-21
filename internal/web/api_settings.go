package web

import (
	"errors"
	"net/http"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// maxSettingsBody caps one settings section.
const maxSettingsBody = 64 << 10

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	sec, err := s.Settings.GetMasked(r.Context(), r.PathValue("section"))
	if err != nil {
		s.settingsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sec)
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	var in map[string]any
	if !decodeJSONLimit(w, r, &in, maxSettingsBody) {
		return
	}
	sec, err := s.Settings.Put(r.Context(), r.PathValue("section"), in)
	if err != nil {
		s.settingsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sec)
}

func (s *Server) settingsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, settings.ErrBadSection):
		writeError(w, http.StatusNotFound, "not_found", "No such settings section.")
	case errors.Is(err, settings.ErrNotObject):
		writeError(w, http.StatusBadRequest, "invalid_json", "A settings section must be a JSON object.")
	case errors.Is(err, settings.ErrBadSecret):
		writeError(w, http.StatusBadRequest, "invalid_secret", "Keys, tokens and passwords must be text.")
	default:
		s.internalError(w, r, err)
	}
}
