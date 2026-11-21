package requests

import (
	"context"
	"errors"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/history"
)

// SettleCopy is what the importer calls once it has put files into a copy's
// folder. It looks at what the copy still lacks (the same reading Wanted
// uses) and moves the requests behind the copy to "available" when nothing is
// missing, or to "partial" when something is, through SetState (so each change
// is published). It returns the state it chose. Requests that are waiting,
// declined or cancelled are left alone.
func (s *Service) SettleCopy(ctx context.Context, copyID int64) (string, error) {
	row, err := s.store.q().copyByID(ctx, copyID)
	if err != nil {
		return "", err
	}
	missing, err := s.copyMissing(ctx, row)
	if err != nil {
		return "", err
	}
	state := StateAvailable
	if missing {
		state = StatePartial
	}
	ids, err := s.store.q().settleTargets(ctx, row.TitleID, row.Quality)
	if err != nil {
		return state, err
	}
	for _, id := range ids {
		switch err := s.SetState(ctx, id, state); {
		case err == nil:
		case errors.Is(err, ErrBadState), errors.Is(err, ErrNotFound):
			// It moved on (cancelled, say) while we looked.
		case db.IsUniqueViolation(err):
			// An older request that went partial, and a newer one for the same
			// title and quality that is open: only one may be "available".
			s.log.Info("request left as it was: another request for the same copy is already open", "id", id)
		default:
			return state, err
		}
	}
	return state, nil
}

// copyMissing reports whether a copy still lacks something, by the same rules
// as Wanted but for one copy and without caring whether it is monitored.
func (s *Service) copyMissing(ctx context.Context, row copyRow) (bool, error) {
	facts, err := s.fileFacts(ctx, row.TitleID)
	if err != nil {
		return false, err
	}
	ff := facts[row.TitleID]
	if row.Kind != KindShow {
		return !ff.has(row.Quality), nil
	}
	d, err := s.cat.Get(ctx, row.TitleID)
	if err != nil {
		return false, err
	}
	scopes, err := s.store.q().liveScopes(ctx)
	if err != nil {
		return false, err
	}
	today := s.now().UTC().Format("2006-01-02")
	return showMissing(d, row.Quality, ff, scopes[copyKey{row.TitleID, row.Quality}], today), nil
}

// ---- history ---------------------------------------------------------------

type recorderBox struct{ r history.Recorder }

// SetRecorder wires in the history, so asks, approvals and declines are
// written to the title's story. Nil turns it off.
func (s *Service) SetRecorder(r history.Recorder) {
	if r == nil {
		s.rec.Store(nil)
		return
	}
	s.rec.Store(&recorderBox{r: r})
}

func userName(u *UserRef) string {
	if u == nil {
		return ""
	}
	return u.Name
}

func requestedEntry(it Item, copyID int64, auto bool) history.Entry {
	return history.Entry{TitleID: it.Title.ID, CopyID: copyID, Event: history.Requested, Detail: map[string]any{
		"requestId": it.ID, "user": userName(it.User), "quality": it.Quality, "scope": it.Scope, "source": it.Source,
		"autoApproved": auto,
	}}
}

func approvedEntry(it Item, copyID int64, auto bool) history.Entry {
	d := map[string]any{"requestId": it.ID, "quality": it.Quality, "auto": auto}
	if it.DecidedBy != nil {
		d["by"] = it.DecidedBy.Name
	}
	return history.Entry{TitleID: it.Title.ID, CopyID: copyID, Event: history.Approved, Detail: d}
}

func declinedEntry(it Item) history.Entry {
	d := map[string]any{"requestId": it.ID, "quality": it.Quality, "reason": it.DeclineReason}
	if it.DecidedBy != nil {
		d["by"] = it.DecidedBy.Name
	}
	return history.Entry{TitleID: it.Title.ID, Event: history.Declined, Detail: d}
}

// record writes one history entry. A failure is logged, never returned: the
// history is a record of what happened, not a step of it.
func (s *Service) record(ctx context.Context, e history.Entry) {
	b := s.rec.Load()
	if b == nil {
		return
	}
	if err := b.r.Record(context.WithoutCancel(ctx), e); err != nil {
		s.log.Warn("could not record request history", "event", e.Event, "title", e.TitleID, "err", err)
	}
}
