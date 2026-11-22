package web

import (
	"net/http"
	"strconv"

	"github.com/BaileyPillon/marquee/internal/accounts"
)

// registerMusic adds the album search route. It needs the release search; without
// it the path falls through to the JSON 404.
func (s *Server) registerMusic(api *http.ServeMux) {
	if s.Search == nil {
		return
	}
	download := guard{roles: []accounts.Role{accounts.RoleOwner, accounts.RoleAdmin}, scope: accounts.ScopeDownloads}
	api.HandleFunc("POST /api/v1/titles/{id}/albums/{albumId}/search", s.guarded(download, s.handleAlbumSearch))
}

// POST /api/v1/titles/{id}/albums/{albumId}/search
//
// Looks for one album of an artist in every source now and sends the best
// acceptable release to the download client. The artist's copy is made if it is
// not there yet. Nothing found is not an error:
//
//	200 {"grabbed": false, "found": 4, "note": "Found 4 releases, none acceptable. ...", "indexersFailed": [...]}
//	201 {"grabbed": true, "downloadId": 12, "found": 4, "release": {"title","quality","score","reasons"}}
func (s *Server) handleAlbumSearch(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	titleID, ok := pathID(r)
	albumID, err := strconv.ParseInt(r.PathValue("albumId"), 10, 64)
	if !ok || err != nil || albumID <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "No such album.")
		return
	}
	res, err := s.Search.SearchAlbum(r.Context(), titleID, albumID)
	if err != nil {
		s.searchError(w, r, err)
		return
	}
	failed := make([]indexerFailureJSON, len(res.IndexersFailed))
	for i, f := range res.IndexersFailed {
		failed[i] = indexerFailureJSON{ID: f.ID, Name: f.Name, Error: f.Error}
	}
	out := map[string]any{"grabbed": res.Grabbed, "found": res.Found, "indexersFailed": failed}
	status := http.StatusOK
	if res.Grabbed {
		status = http.StatusCreated
		out["downloadId"] = res.DownloadID
		out["release"] = map[string]any{"title": res.Release, "quality": res.Quality, "score": res.Score, "reasons": nonNil(res.Reasons)}
	} else {
		out["note"] = res.Note
	}
	writeJSON(w, status, out)
}
