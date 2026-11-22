package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/watchlists"
)

// registerWatchlists adds the Plex watchlist routes. All of them are for the
// owner: they hold the link to the owner's Plex account. Without the service
// (a test server that does not need it) it adds nothing, and the paths fall
// through to the JSON 404.
func (s *Server) registerWatchlists(api *http.ServeMux) {
	if s.Watchlists == nil {
		return
	}
	owner := guard{roles: []accounts.Role{accounts.RoleOwner}, scope: accounts.ScopeSettings}
	api.HandleFunc("GET /api/v1/watchlists", s.guarded(owner, s.handleWatchlists))
	api.HandleFunc("PUT /api/v1/watchlists/plex", s.guarded(owner, s.handlePlexSettings))
	api.HandleFunc("POST /api/v1/watchlists/plex/link", s.guarded(owner, s.handlePlexLinkStart))
	api.HandleFunc("GET /api/v1/watchlists/plex/link/{pinId}", s.guarded(owner, s.handlePlexLinkCheck))
	api.HandleFunc("DELETE /api/v1/watchlists/plex/link", s.guarded(owner, s.handlePlexUnlink))
	api.HandleFunc("PUT /api/v1/watchlists/plex/friends/{plexUserId}", s.guarded(owner, s.handlePlexFriendLink))
	api.HandleFunc("POST /api/v1/watchlists/check", s.guarded(owner, s.handleWatchlistCheck))
}

// watchlistsError maps what the watchlists module returns to an API error. The
// codes are stable; the UI keys off them.
func (s *Server) watchlistsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, watchlists.ErrNotLinked):
		writeError(w, http.StatusConflict, "not_linked", "No Plex account is linked yet.")
	case errors.Is(err, watchlists.ErrDisabled):
		writeError(w, http.StatusConflict, "disabled", "Plex watchlists are switched off in the settings.")
	case errors.Is(err, watchlists.ErrRunning):
		writeError(w, http.StatusConflict, "already_running", "A check is already running.")
	case errors.Is(err, watchlists.ErrPinExpired):
		writeError(w, http.StatusGone, "pin_expired", "That Plex link request has expired. Start again.")
	case errors.Is(err, watchlists.ErrUnknownFriend):
		writeError(w, http.StatusNotFound, "not_found", "No such Plex friend.")
	case errors.Is(err, watchlists.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such person.")
	case errors.Is(err, watchlists.ErrUserAlreadyLinked):
		writeError(w, http.StatusConflict, "user_already_linked", "That person is already linked to another Plex friend.")
	case errors.Is(err, watchlists.ErrPlexAuth):
		writeError(w, http.StatusConflict, "plex_auth", "Plex did not accept the link. Link the account again.")
	case r.Context().Err() != nil:
		// The client went away; there is nobody to answer.
	default:
		s.log.Warn("plex request failed", "id", RequestID(r.Context()), "path", r.URL.Path, "err", err)
		writeError(w, http.StatusBadGateway, "plex_unreachable", "Plex did not answer as expected. Try again in a minute.")
	}
}

// GET /api/v1/watchlists
func (s *Server) handleWatchlists(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	st, err := s.Watchlists.Status(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plex": st})
}

// PUT /api/v1/watchlists/plex {enabled?, includeFriends?, autoCreateMembers?, quality?}:
// changes how the Plex watchlists are read. A field left out keeps its value; the
// quality is "hd" or "uhd". The answer is {"plex": status}, the same as GET
// /api/v1/watchlists.
func (s *Server) handlePlexSettings(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	var in struct {
		Enabled           *bool   `json:"enabled"`
		IncludeFriends    *bool   `json:"includeFriends"`
		AutoCreateMembers *bool   `json:"autoCreateMembers"`
		Quality           *string `json:"quality"`
	}
	if !decodeStrictJSON(w, r, &in) {
		return
	}
	st, err := s.Watchlists.SetPlexConfig(r.Context(), watchlists.PlexPatch{
		Enabled: in.Enabled, IncludeFriends: in.IncludeFriends, AutoCreateMembers: in.AutoCreateMembers, Quality: in.Quality,
	})
	var bad *watchlists.InvalidError
	switch {
	case errors.As(err, &bad):
		writeError(w, http.StatusBadRequest, "invalid_request", bad.Message)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plex": st})
}

// POST /api/v1/watchlists/plex/link -> {pinId, code, authUrl}
func (s *Server) handlePlexLinkStart(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	l, err := s.Watchlists.StartLink(r.Context())
	if err != nil {
		s.watchlistsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// GET /api/v1/watchlists/plex/link/{pinId} -> {linked}
func (s *Server) handlePlexLinkCheck(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	pin, err := strconv.ParseInt(r.PathValue("pinId"), 10, 64)
	if err != nil || pin <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "That is not a link request id.")
		return
	}
	linked, err := s.Watchlists.CheckLink(r.Context(), pin, p.User.ID)
	if err != nil {
		s.watchlistsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"linked": linked})
}

// DELETE /api/v1/watchlists/plex/link
func (s *Server) handlePlexUnlink(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	if err := s.Watchlists.Unlink(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/v1/watchlists/plex/friends/{plexUserId} {"userId": 5 | null}: links the
// friend to a Marquee person, or unlinks them. The answer is the friend.
func (s *Server) handlePlexFriendLink(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	var in struct {
		UserID json.RawMessage `json:"userId"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if len(in.UserID) == 0 { // not "null": the key is missing
		writeError(w, http.StatusBadRequest, "invalid_request", "userId is required: a person's id, or null to unlink.")
		return
	}
	var userID *int64
	if err := json.Unmarshal(in.UserID, &userID); err != nil || (userID != nil && *userID <= 0) {
		writeError(w, http.StatusBadRequest, "invalid_request", "userId must be a person's id, or null.")
		return
	}
	plexUserID := r.PathValue("plexUserId")
	if err := s.Watchlists.SetFriendLink(r.Context(), plexUserID, userID); err != nil {
		s.watchlistsError(w, r, err)
		return
	}
	st, err := s.Watchlists.Status(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	for _, f := range st.Friends {
		if f.PlexUserID == plexUserID {
			writeJSON(w, http.StatusOK, f)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "No such Plex friend.")
}

// checkTimeout bounds one "check now": a big watchlist is a lot of lookups.
const checkTimeout = 10 * time.Minute

// POST /api/v1/watchlists/check runs the check now and answers with what it did:
// {asked, skipped: {reason: count}, checkedAt}.
func (s *Server) handleWatchlistCheck(w http.ResponseWriter, r *http.Request, _ *accounts.Principal) {
	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()
	res, err := s.Watchlists.Sync(ctx)
	if err != nil {
		s.watchlistsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"asked": res.Asked, "skipped": res.Skipped, "checkedAt": time.Now().UTC()})
}
