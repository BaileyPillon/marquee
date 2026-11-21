package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/calendar"
)

// registerCalendar adds the calendar route. Without the service (a test server
// that does not need it) it adds nothing, and the path falls through to the JSON
// 404.
func (s *Server) registerCalendar(api *http.ServeMux) {
	if s.Calendar == nil {
		return
	}
	api.HandleFunc("GET /api/v1/calendar", s.guarded(guard{scope: accounts.ScopeSee}, s.handleCalendar))
}

// defaultCalendarDays is how far ahead the calendar looks when no end is given.
const defaultCalendarDays = 30

// GET /api/v1/calendar?from=YYYY-MM-DD&to=YYYY-MM-DD[&mine=true] -> {items: [...]}.
// The range may be at most 92 days. Without from the calendar starts today, and
// without to it ends 30 days after its start. Any signed-in person may read it.
func (s *Server) handleCalendar(w http.ResponseWriter, r *http.Request, p *accounts.Principal) {
	q := r.URL.Query()
	parse := func(name string) (time.Time, bool, bool) {
		v := q.Get(name)
		if v == "" {
			return time.Time{}, false, true
		}
		t, err := time.Parse(calendar.DateLayout, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_date", name+" must be a date written YYYY-MM-DD.")
			return time.Time{}, false, false
		}
		return t, true, true
	}
	from, hasFrom, ok := parse("from")
	if !ok {
		return
	}
	to, hasTo, ok := parse("to")
	if !ok {
		return
	}
	if !hasFrom {
		now := time.Now()
		from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	}
	if !hasTo {
		to = from.AddDate(0, 0, defaultCalendarDays)
	}
	var opts []calendar.Option
	if v := q.Get("mine"); v != "" {
		mine, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "mine must be true or false.")
			return
		}
		if mine {
			opts = append(opts, calendar.OnlyMine())
		}
	}
	items, err := s.Calendar.Range(r.Context(), from, to, calendar.Viewer{UserID: p.User.ID, Role: string(p.User.Role)}, opts...)
	switch {
	case errors.Is(err, calendar.ErrInvalidRange):
		writeError(w, http.StatusBadRequest, "invalid_range", "to cannot be before from.")
		return
	case errors.Is(err, calendar.ErrRangeTooLarge):
		writeErrorExtra(w, http.StatusBadRequest, "range_too_large",
			"The calendar shows at most "+strconv.Itoa(calendar.MaxRangeDays)+" days at a time.",
			map[string]any{"maxDays": calendar.MaxRangeDays})
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
