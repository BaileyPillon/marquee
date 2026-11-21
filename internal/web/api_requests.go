package web

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/requests"
)

// registerRequests adds the request, approval, people and limits routes. Without
// the requests service (a test server that does not need it) it adds nothing,
// and those paths fall through to the JSON 404.
//
// Who may do what: any signed-in person with the "ask" permission asks and
// cancels their own; "see" reads (members only ever see their own); the owner
// and admins answer (with the "approve" permission on a key); the owner changes
// limits.
func (s *Server) registerRequests(api *http.ServeMux) {
	if s.Requests == nil {
		return
	}
	staff := []accounts.Role{accounts.RoleOwner, accounts.RoleAdmin}
	ask := guard{scope: accounts.ScopeAsk}
	see := guard{scope: accounts.ScopeSee}
	answer := guard{roles: staff, scope: accounts.ScopeApprove}
	manage := guard{roles: staff, scope: accounts.ScopeAdmin}
	owner := guard{roles: []accounts.Role{accounts.RoleOwner}, scope: accounts.ScopeSettings}

	api.HandleFunc("POST /api/v1/requests", s.guarded(ask, s.handleRequestAsk))
	api.HandleFunc("GET /api/v1/requests", s.guarded(see, s.handleRequestList))
	api.HandleFunc("GET /api/v1/requests/summary", s.guarded(see, s.handleRequestSummary))
	api.HandleFunc("GET /api/v1/requests/{id}", s.guarded(see, s.handleRequestGet))
	api.HandleFunc("POST /api/v1/requests/{id}/approve", s.guarded(answer, s.handleRequestApprove))
	api.HandleFunc("POST /api/v1/requests/{id}/decline", s.guarded(answer, s.handleRequestDecline))
	api.HandleFunc("DELETE /api/v1/requests/{id}", s.guarded(ask, s.handleRequestCancel))
	api.HandleFunc("GET /api/v1/people", s.guarded(manage, s.handlePeople))
	api.HandleFunc("PUT /api/v1/people/{userId}/limits", s.guarded(owner, s.handlePersonLimits))
}

// actorOf is the person behind a request, as the requests module sees them.
func actorOf(p *accounts.Principal) requests.Actor {
	return requests.Actor{UserID: p.User.ID, Role: string(p.User.Role), Source: requests.SourceManual}
}

// writeErrorExtra is writeError with more fields inside the error object, for
// the answers that carry data (a used-up limit, the existing ask).
func writeErrorExtra(w http.ResponseWriter, status int, code, message string, extra map[string]any) {
	body := map[string]any{"code": code, "message": message}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, map[string]any{"error": body})
}

// requestsError maps what the requests module returns to an API error. The
// codes are stable; the UI keys off them. Anything it does not know is handed to
// the catalog's mapping (a title that cannot be found or fetched).
func (s *Server) requestsError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		inv *requests.InvalidError
		lim *requests.LimitError
		dup *requests.DuplicateError
	)
	switch {
	case errors.As(err, &inv):
		writeError(w, http.StatusBadRequest, inv.Code, inv.Message)
	case errors.As(err, &lim):
		word := "HD"
		if lim.Quality == requests.QualityUHD {
			word = "4K"
		}
		if lim.ResetsAt != nil {
			if secs := int(math.Ceil(time.Until(*lim.ResetsAt).Seconds())); secs > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(secs))
			}
		}
		writeErrorExtra(w, http.StatusTooManyRequests, "limit_reached",
			"You have used all "+strconv.Itoa(lim.Limit)+" of your "+word+" asks for this week.",
			map[string]any{"quality": lim.Quality, "used": lim.Used, "limit": lim.Limit, "resetsAt": lim.ResetsAt})
	case errors.As(err, &dup):
		writeErrorExtra(w, http.StatusConflict, "already_requested",
			"That title has already been asked for at this quality.",
			map[string]any{"request": dup.Existing})
	case errors.Is(err, requests.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such request.")
	case errors.Is(err, requests.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such person.")
	case errors.Is(err, requests.ErrCopyNotFound):
		writeError(w, http.StatusNotFound, "not_found", "No such copy.")
	case errors.Is(err, requests.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", "You are not allowed to do that.")
	case errors.Is(err, requests.ErrNotPending):
		writeError(w, http.StatusConflict, "not_pending", "That request has already been answered.")
	case errors.Is(err, requests.ErrNotCancellable):
		writeError(w, http.StatusConflict, "not_cancellable", "That request can no longer be cancelled.")
	case errors.Is(err, requests.ErrBadState):
		writeError(w, http.StatusConflict, "bad_state", "That request cannot move to that state.")
	case errors.Is(err, requests.ErrNoFolder):
		writeError(w, http.StatusConflict, "no_library_folder",
			"There is no library folder for this kind of title yet. Add one in Settings, then try again.")
	default:
		s.titlesError(w, r, err)
	}
}

func requestPathID(r *http.Request) (int64, bool) { return pathID(r) }

// ---- asking ----------------------------------------------------------------

// POST /api/v1/requests {titleId | kind, provider, providerId, quality, scope?, note?}
func (s *Server) handleRequestAsk(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	var in struct {
		TitleID    *int64          `json:"titleId"`
		Kind       string          `json:"kind"`
		Provider   string          `json:"provider"`
		ProviderID providerID      `json:"providerId"`
		Quality    string          `json:"quality"`
		Scope      *requests.Scope `json:"scope"`
		Note       string          `json:"note"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	ai := requests.AskInput{
		Kind: in.Kind, Provider: in.Provider, ProviderID: string(in.ProviderID),
		Quality: in.Quality, Note: in.Note,
	}
	if in.TitleID != nil {
		if *in.TitleID <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", "titleId must be the id of a title.")
			return
		}
		ai.TitleID = *in.TitleID
	}
	if in.Scope != nil {
		ai.Scope = *in.Scope
	}
	res, err := s.Requests.Ask(r.Context(), actorOf(p), ai)
	if err != nil {
		s.requestsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

// ---- reading ---------------------------------------------------------------

// GET /api/v1/requests?state=&mine=1&limit=&offset=
func (s *Server) handleRequestList(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	q := r.URL.Query()
	f := requests.ListFilter{State: q.Get("state")}
	if v := q.Get("mine"); v != "" {
		mine, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "mine must be 1 or 0.")
			return
		}
		f.Mine = mine
	}
	limit, ok := queryInt(w, q.Get("limit"), "limit", 0, 1, 1<<20)
	if !ok {
		return
	}
	offset, ok := queryInt(w, q.Get("offset"), "offset", 0, 0, 1<<40)
	if !ok {
		return
	}
	f.Limit, f.Offset = int(limit), int(offset)
	res, err := s.Requests.List(r.Context(), actorOf(p), f)
	if err != nil {
		s.requestsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /api/v1/requests/{id}
func (s *Server) handleRequestGet(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	id, ok := requestPathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such request.")
		return
	}
	it, err := s.Requests.Get(r.Context(), actorOf(p), id)
	if err != nil {
		s.requestsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": it})
}

// GET /api/v1/requests/summary
func (s *Server) handleRequestSummary(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	sum, err := s.Requests.Summary(r.Context(), actorOf(p))
	if err != nil {
		s.requestsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

// ---- answering -------------------------------------------------------------

// POST /api/v1/requests/{id}/approve {alwaysAllowUHD?}
func (s *Server) handleRequestApprove(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	id, ok := requestPathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such request.")
		return
	}
	var in struct {
		AlwaysAllowUHD bool `json:"alwaysAllowUHD"`
	}
	if !decodeOptionalJSON(w, r, &in) {
		return
	}
	it, err := s.Requests.Approve(r.Context(), actorOf(p), id, requests.ApproveOptions{AlwaysAllowUHD: in.AlwaysAllowUHD})
	if err != nil {
		s.requestsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": it})
}

// POST /api/v1/requests/{id}/decline {reason}
func (s *Server) handleRequestDecline(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	id, ok := requestPathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such request.")
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !decodeOptionalJSON(w, r, &in) {
		return
	}
	it, err := s.Requests.Decline(r.Context(), actorOf(p), id, in.Reason)
	if err != nil {
		s.requestsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": it})
}

// DELETE /api/v1/requests/{id}: the request becomes cancelled.
func (s *Server) handleRequestCancel(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	id, ok := requestPathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "No such request.")
		return
	}
	it, err := s.Requests.Cancel(r.Context(), actorOf(p), id)
	if err != nil {
		s.requestsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"request": it})
}

// ---- people and limits -----------------------------------------------------

// GET /api/v1/people
func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	res, err := s.Requests.People(r.Context(), actorOf(p))
	if err != nil {
		s.requestsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// PUT /api/v1/people/{userId}/limits {hdPerWeek?, uhdPerWeek?, autoApproveHD?, autoApproveUHD?}
//
// A field left out stays as it is; a field sent as null goes back to the default.
func (s *Server) handlePersonLimits(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	userID, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil || userID <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "No such person.")
		return
	}
	var raw map[string]json.RawMessage
	if !decodeJSON(w, r, &raw) {
		return
	}
	var patch requests.LimitsPatch
	for k, v := range raw {
		null := string(v) == "null"
		switch k {
		case "hdPerWeek", "uhdPerWeek":
			dst := &patch.HDPerWeek
			if k == "uhdPerWeek" {
				dst = &patch.UHDPerWeek
			}
			dst.Set = true
			if !null {
				var n int
				if err := json.Unmarshal(v, &n); err != nil {
					writeError(w, http.StatusBadRequest, "invalid_request", k+" must be a whole number, or null for the default.")
					return
				}
				dst.Value = &n
			}
		case "autoApproveHD", "autoApproveUHD":
			dst := &patch.AutoApproveHD
			if k == "autoApproveUHD" {
				dst = &patch.AutoApproveUHD
			}
			dst.Set = true
			if !null {
				var b bool
				if err := json.Unmarshal(v, &b); err != nil {
					writeError(w, http.StatusBadRequest, "invalid_request", k+" must be true or false, or null for the default.")
					return
				}
				dst.Value = &b
			}
		default:
			writeError(w, http.StatusBadRequest, "invalid_request", "Unknown field "+strconv.Quote(k)+".")
			return
		}
	}
	info, err := s.Requests.SetLimits(r.Context(), actorOf(p), userID, patch)
	if err != nil {
		s.requestsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"person": info})
}
