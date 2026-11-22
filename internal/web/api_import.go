package web

import (
	"errors"
	"net/http"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/importer"
	"github.com/BaileyPillon/marquee/internal/mediaservers"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// registerImport adds the routes that come with importing: a title's history,
// "open on the media server", a manual import retry and the importer's recent
// work. Each one is left out when the service behind it is nil, so a test
// server that does not need it falls through to the JSON 404.
func (s *Server) registerImport(api *http.ServeMux) {
	see := guard{scope: accounts.ScopeSee}
	control := guard{roles: []accounts.Role{accounts.RoleOwner, accounts.RoleAdmin}, scope: accounts.ScopeDownloads}

	if s.History != nil {
		api.HandleFunc("GET /api/v1/titles/{id}/history", s.guarded(see, s.handleTitleHistory))
	}
	if s.MediaServers != nil && s.Titles != nil {
		api.HandleFunc("GET /api/v1/titles/{id}/open", s.guarded(see, s.handleTitleOpen))
	}
	if s.Importer != nil && s.Downloads != nil {
		api.HandleFunc("POST /api/v1/downloads/{id}/import", s.guarded(control, s.handleDownloadImport))
		api.HandleFunc("GET /api/v1/import/status", s.guarded(control, s.handleImportStatus))
	}
	// Rename and delete a file that is already in the library (api_library_files.go).
	s.registerLibraryFiles(api)
}

const (
	defaultHistoryLimit = 50
	maxHistoryLimit     = 500
)

// GET /api/v1/titles/{id}/history?limit= → {"items": [{at, event, detail, ...}]},
// newest first.
func (s *Server) handleTitleHistory(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such title.")
		return
	}
	limit, ok := queryInt(w, r.URL.Query().Get("limit"), "limit", defaultHistoryLimit, 1, maxHistoryLimit)
	if !ok {
		return
	}
	if s.Titles != nil {
		if _, err := s.Titles.Get(r.Context(), id); err != nil {
			if errors.Is(err, titles.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "No such title.")
				return
			}
			s.internalError(w, r, err)
			return
		}
	}
	items, err := s.History.ForTitle(r.Context(), id, int(limit))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GET /api/v1/titles/{id}/open → {"url", "server": {id, name, kind}}, or 404
// not_found_on_server when no media server has the title.
func (s *Server) handleTitleOpen(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
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
	res, err := s.MediaServers.OpenURL(r.Context(), d.Title)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, res)
	case errors.Is(err, mediaservers.ErrNotOnServer):
		writeError(w, http.StatusNotFound, "not_found_on_server", "That title is not on any of your media servers yet.")
	case r.Context().Err() != nil:
		// The client went away; there is nobody to answer.
	case errors.Is(err, mediaservers.ErrAuth), errors.Is(err, mediaservers.ErrUnreachable):
		writeError(w, http.StatusBadGateway, "media_server_unreachable", mediaservers.PlainError(err))
	default:
		s.internalError(w, r, err)
	}
}

// POST /api/v1/downloads/{id}/import: start the import of a finished (or failed)
// download again, in the background. 202 with the download as it is now; the
// state moves to importing and the outcome arrives on the events stream.
func (s *Server) handleDownloadImport(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such download.")
		return
	}
	err := s.Importer.Retry(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, downloads.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such download.")
		return
	case errors.Is(err, importer.ErrWrongState):
		writeError(w, http.StatusConflict, "invalid_state", "Only a finished or failed download can be imported again.")
		return
	case errors.Is(err, importer.ErrBusy):
		writeError(w, http.StatusConflict, "import_running", "That download is being imported right now.")
		return
	case r.Context().Err() != nil:
		return
	default:
		s.internalError(w, r, err)
		return
	}
	v, err := s.Downloads.Get(r.Context(), id)
	if err != nil {
		s.downloadsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "download": v})
}

// GET /api/v1/import/status → {"imports": [last 20 with outcome], "running": [download ids]}
func (s *Server) handleImportStatus(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	recent, err := s.Importer.Status(r.Context(), 20)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imports": recent, "running": s.Importer.Running()})
}
