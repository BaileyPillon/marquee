package web

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/library"
)

// File listing page sizes.
const (
	defaultFileLimit = 100
	maxFileLimit     = 500
)

// registerLibrary adds the library routes (folders, scans, files, stats). It
// adds nothing when the server was built without a library, so those paths
// answer with the ordinary JSON 404.
func (s *Server) registerLibrary(api *http.ServeMux) {
	if s.Library == nil {
		return
	}
	see := guard{scope: accounts.ScopeSee}
	owner := guard{roles: []accounts.Role{accounts.RoleOwner}, scope: accounts.ScopeSettings}
	manage := guard{roles: []accounts.Role{accounts.RoleOwner, accounts.RoleAdmin}, scope: accounts.ScopeAdmin}

	api.HandleFunc("GET /api/v1/library/folders", s.guarded(see, s.handleLibraryFolders))
	api.HandleFunc("POST /api/v1/library/folders", s.guarded(owner, s.handleLibraryFolderAdd))
	api.HandleFunc("DELETE /api/v1/library/folders/{id}", s.guarded(owner, s.handleLibraryFolderDelete))
	api.HandleFunc("POST /api/v1/library/scan", s.guarded(manage, s.handleLibraryScan))
	api.HandleFunc("GET /api/v1/library/files", s.guarded(manage, s.handleLibraryFiles))
	api.HandleFunc("GET /api/v1/library/stats", s.guarded(see, s.handleLibraryStats))
}

// GET /api/v1/library/folders
func (s *Server) handleLibraryFolders(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	folders, err := s.Library.Folders(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if folders == nil {
		folders = []library.FolderInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"folders": folders})
}

// POST /api/v1/library/folders {kind, path, label}
func (s *Server) handleLibraryFolderAdd(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	var in struct {
		Kind  string `json:"kind"`
		Path  string `json:"path"`
		Label string `json:"label"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	f, err := s.Library.AddFolder(r.Context(), in.Kind, in.Path, in.Label)
	if err != nil {
		s.libraryError(w, r, err)
		return
	}
	info, err := s.Library.FolderInfo(r.Context(), f.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"folder": info})
}

// DELETE /api/v1/library/folders/{id}. Rows only: no file on disk is touched.
func (s *Server) handleLibraryFolderDelete(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such library folder.")
		return
	}
	if err := s.Library.RemoveFolder(r.Context(), id); err != nil {
		s.libraryError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/v1/library/scan {folderId?}: starts scanning and answers at once.
// Progress arrives on the event stream as "library.scan" events.
func (s *Server) handleLibraryScan(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	var in struct {
		FolderID *int64 `json:"folderId"`
	}
	if !decodeOptionalJSON(w, r, &in) {
		return
	}
	var id int64
	if in.FolderID != nil {
		if *in.FolderID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", "folderId must be the id of a library folder.")
			return
		}
		id = *in.FolderID
	}
	started, running, err := s.Library.StartScan(r.Context(), id)
	if err != nil {
		s.libraryError(w, r, err)
		return
	}
	if started == nil {
		started = []int64{}
	}
	if running == nil {
		running = []int64{}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": started, "running": running})
}

// GET /api/v1/library/files?state=&folderId=&limit=&offset=
func (s *Server) handleLibraryFiles(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	q := r.URL.Query()
	f := library.FileFilter{State: q.Get("state")}
	switch f.State {
	case "", library.StateMatched, library.StateUnmatched, library.StateIgnored, "missing":
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "state must be matched, unmatched, ignored or missing.")
		return
	}
	var ok bool
	if f.FolderID, ok = queryInt(w, q.Get("folderId"), "folderId", 0, 0, 1<<62); !ok {
		return
	}
	limit, ok := queryInt(w, q.Get("limit"), "limit", defaultFileLimit, 1, maxFileLimit)
	if !ok {
		return
	}
	offset, ok := queryInt(w, q.Get("offset"), "offset", 0, 0, 1<<40)
	if !ok {
		return
	}
	f.Limit, f.Offset = int(limit), int(offset)

	files, total, err := s.Library.Files(r.Context(), f)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files": files, "total": total, "limit": f.Limit, "offset": f.Offset,
	})
}

// GET /api/v1/library/stats
func (s *Server) handleLibraryStats(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	st, err := s.Library.Stats(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// ---- helpers ---------------------------------------------------------------

// queryInt reads an optional whole-number query value in [lo, hi]. On a bad
// value it has already written the error and returns false.
func queryInt(w http.ResponseWriter, raw, name string, def, lo, hi int64) (int64, bool) {
	if raw == "" {
		return def, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < lo || n > hi {
		writeError(w, http.StatusBadRequest, "invalid_request", name+" must be a whole number from "+
			strconv.FormatInt(lo, 10)+" up.")
		return 0, false
	}
	return n, true
}

// decodeOptionalJSON is decodeJSON for a body that may be left out entirely.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "That request is too big.")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_json", "The request body could not be read.")
		}
		return false
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return decodeJSON(w, r, dst)
}

// libraryError maps what the library module returns to an API error. The
// codes are stable; the UI keys off them.
func (s *Server) libraryError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, library.ErrInvalidKind):
		writeError(w, http.StatusBadRequest, "invalid_kind", "Kind must be \"movie\", \"show\" or \"music\".")
	case errors.Is(err, library.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, "invalid_path", "Give the full path to a folder on the machine Marquee runs on, like D:\\Movies or /media/movies.")
	case errors.Is(err, library.ErrPathNotFound):
		writeError(w, http.StatusBadRequest, "path_not_found", "Marquee cannot find that folder.")
	case errors.Is(err, library.ErrNotDirectory):
		writeError(w, http.StatusBadRequest, "not_a_folder", "That path is a file, not a folder.")
	case errors.Is(err, library.ErrNested):
		writeError(w, http.StatusConflict, "folder_nested", "That folder is inside another library folder.")
	case errors.Is(err, library.ErrContainsFolder):
		writeError(w, http.StatusConflict, "folder_nested", "That folder contains another library folder.")
	case errors.Is(err, library.ErrDuplicate):
		writeError(w, http.StatusConflict, "folder_exists", "That folder is already in the library.")
	case errors.Is(err, library.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such library folder.")
	case errors.Is(err, library.ErrClosed):
		writeError(w, http.StatusServiceUnavailable, "shutting_down", "Marquee is shutting down.")
	default:
		s.internalError(w, r, err)
	}
}
