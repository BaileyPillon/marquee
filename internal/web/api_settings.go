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
	section := r.PathValue("section")
	// Some sections are checked and tidied before they are stored (the weekly limits
	// may be written "unlimited"; the server's public address and trusted proxies
	// must be valid).
	if norm := normalizers[section]; norm != nil && in != nil {
		out, err := norm(in)
		var bad *sectionError
		if errors.As(err, &bad) {
			writeError(w, http.StatusBadRequest, bad.code, bad.message)
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		in = out
	}
	sec, err := s.Settings.Put(r.Context(), section, in)
	if err != nil {
		s.settingsError(w, r, err)
		return
	}
	if section == ServerSection {
		s.forgetRuntime() // proxies and the public address take effect at once
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
