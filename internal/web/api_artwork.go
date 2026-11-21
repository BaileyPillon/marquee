package web

import (
	"net/http"

	"github.com/BaileyPillon/marquee/internal/accounts"
)

// artworkCacheControl lets a browser keep a poster for a week.
const artworkCacheControl = "public, max-age=604800"

// handleArtwork serves GET /api/v1/artwork/{provider}/{rest...}. It is the only
// way the UI loads posters, backdrops and covers, so the page's
// Content-Security-Policy can say img-src 'self' and the third-party image
// hosts never see a browser. Only the three allowlisted providers work; any
// other address is refused before a request is made.
func (s *Server) handleArtwork(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	f, err := s.Metadata.Artwork.Open(r.Context(), r.PathValue("provider"), r.PathValue("rest"))
	if err != nil {
		s.titlesError(w, r, err)
		return
	}
	defer f.Close()

	h := w.Header()
	h.Set("Content-Type", f.ContentType)
	h.Set("Cache-Control", artworkCacheControl) // replaces the API-wide no-store
	h.Set("ETag", f.Tag)
	http.ServeContent(w, r, "", f.ModTime, f.File)
}
