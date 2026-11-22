package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/importer"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// registerLibraryFiles adds the two actions on a file that is already in the
// library: renaming it the way the importer would name it, and deleting it. They
// need the importer (it owns the naming and the disk work), so without it, or
// without the library, nothing is added and the paths fall through to the JSON 404.
//
// Renaming is for the owner and admins. Deleting is for the owner, and for a key
// only when it has the "delete" tick.
func (s *Server) registerLibraryFiles(api *http.ServeMux) {
	if s.Importer == nil || s.Library == nil {
		return
	}
	manage := guard{roles: []accounts.Role{accounts.RoleOwner, accounts.RoleAdmin}, scope: accounts.ScopeAdmin}
	del := guard{roles: []accounts.Role{accounts.RoleOwner}, scope: accounts.ScopeDelete}
	api.HandleFunc("POST /api/v1/library/files/{id}/rename", s.guarded(manage, s.handleLibraryFileRename))
	api.HandleFunc("DELETE /api/v1/library/files/{id}", s.guarded(del, s.handleLibraryFileDelete))
}

// fileActionError maps what the importer's file actions return to an API error.
// The codes are stable; the UI keys off them.
func (s *Server) fileActionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, library.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such file.")
	case errors.Is(err, importer.ErrNotMatched), errors.Is(err, titles.ErrNotFound):
		writeError(w, http.StatusConflict, "not_matched",
			"That file is not matched to a title yet, so Marquee does not know what to call it.")
	case errors.Is(err, importer.ErrFileMissing):
		writeError(w, http.StatusConflict, "file_missing",
			"That file is not on disk any more. Scan the library to update the list.")
	case errors.Is(err, importer.ErrNameTaken), errors.Is(err, library.ErrNameInUse):
		writeError(w, http.StatusConflict, "name_taken", "A different file already has that name.")
	case errors.Is(err, importer.ErrOutsideLibrary), errors.Is(err, library.ErrBadRelPath):
		writeError(w, http.StatusForbidden, "outside_library",
			"That file is not inside its library folder, so Marquee will not touch it.")
	case errors.Is(err, importer.ErrImporting):
		writeError(w, http.StatusConflict, "import_running", "An import is running right now. Try again in a moment.")
	case r.Context().Err() != nil:
		// The client went away; there is nobody to answer.
	default:
		s.internalError(w, r, err)
	}
}

// POST /api/v1/library/files/{id}/rename: gives the file the name the importer
// would give it today. 200 {renamed, from, to, file, servers}; renamed is false
// when the name was already right.
func (s *Server) handleLibraryFileRename(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such file.")
		return
	}
	res, err := s.Importer.RenameFile(r.Context(), id)
	if err != nil {
		s.fileActionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// DELETE /api/v1/library/files/{id}?deleteFile=true|false: takes the file out of
// the library; with deleteFile=true the file on disk goes too (never a path
// outside the library folder). 200 {deleted, path, fromDisk, wasMissing, servers}.
func (s *Server) handleLibraryFileDelete(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such file.")
		return
	}
	fromDisk := false
	if v := r.URL.Query().Get("deleteFile"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "deleteFile must be true or false.")
			return
		}
		fromDisk = b
	}
	res, err := s.Importer.DeleteFile(r.Context(), id, fromDisk)
	if err != nil {
		s.fileActionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted": true, "path": res.Path, "fromDisk": res.FromDisk, "wasMissing": res.WasMissing, "servers": res.Servers,
	})
}
