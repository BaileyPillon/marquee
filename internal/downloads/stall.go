package downloads

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/BaileyPillon/marquee/internal/grab"
)

// Some torrents never finish: an Internet Archive torrent whose files changed
// after it was made sits at 99.97% for ever, and the client says "stalled". The
// poll gives up on a download that has made no progress for a while, so the
// search can pick another release.

// SettingsSection is the settings section that holds how downloads are followed.
const SettingsSection = "downloads"

// DefaultStallMinutes is how long a download may make no progress before Marquee
// gives up on it.
const DefaultStallMinutes = 30

// maxStallMinutes keeps a typo from becoming a wait of years (one week).
const maxStallMinutes = 7 * 24 * 60

// Settings is the "downloads" settings section: {"stallMinutes": 30,
// "removeStalled": true}.
type Settings struct {
	// StallMinutes is how long a downloading or stalled download may go without
	// gaining progress before it is treated as failed. 0 turns the check off.
	StallMinutes int `json:"stallMinutes"`
	// RemoveStalled also removes a download that was given up on from the client,
	// with what it had downloaded.
	RemoveStalled bool `json:"removeStalled"`
}

// DefaultSettings are what holds until the owner changes them.
func DefaultSettings() Settings {
	return Settings{StallMinutes: DefaultStallMinutes, RemoveStalled: true}
}

// SettingsReader is the part of the settings service the poll reads. A field the
// stored section leaves out keeps the value dst held, so dst starts as the
// defaults.
type SettingsReader interface {
	Get(ctx context.Context, section string, dst any) (bool, error)
}

// NormalizeSettings checks what the owner sent for the "downloads" section and
// returns it as it is stored: stallMinutes a whole number of minutes from 0 (off)
// to a week, removeStalled true or false. Other fields pass through.
func NormalizeSettings(in map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	if v, ok := in["stallMinutes"]; ok && v != nil {
		n, isNum := v.(float64)
		if !isNum || n != math.Trunc(n) || n < 0 || n > maxStallMinutes {
			return nil, &InvalidError{Message: fmt.Sprintf("stallMinutes must be a whole number of minutes from 0 (never give up) to %d.", maxStallMinutes)}
		}
		out["stallMinutes"] = int(n)
	}
	if v, ok := in["removeStalled"]; ok && v != nil {
		if _, isBool := v.(bool); !isBool {
			return nil, &InvalidError{Message: "removeStalled must be true or false."}
		}
	}
	return out, nil
}

// settings reads the section for one poll. A section that cannot be read leaves
// the defaults, so a settings problem never switches the check off or fails a
// poll.
func (s *Service) settings(ctx context.Context) Settings {
	v := DefaultSettings()
	if s.cfg == nil {
		return v
	}
	if _, err := s.cfg.Get(ctx, SettingsSection, &v); err != nil {
		s.log.Warn("downloads: the downloads settings cannot be read; using the defaults", "err", err)
		return DefaultSettings()
	}
	v.StallMinutes = min(max(v.StallMinutes, 0), maxStallMinutes)
	return v
}

// SetStalledHandler sets who finishes the job when a download is given up on
// (the importer: blocklist, forget the grab, search again). Nil is allowed.
func (s *Service) SetStalledHandler(h grab.StalledHandler) {
	s.handlerMu.Lock()
	s.stalled = h
	s.handlerMu.Unlock()
}

func (s *Service) stalledHandler() grab.StalledHandler {
	s.handlerMu.RLock()
	defer s.handlerMu.RUnlock()
	return s.stalled
}

// stallEligible reports whether a download in this state can be stalled: it is
// being fetched. Queued and paused ones are waiting, not stuck.
func stallEligible(state string) bool {
	return state == StateDownloading || state == StateStalled
}

// stallReason says whether a download that did not gain progress in this poll
// has been stuck long enough, and if so how to put it.
func (s *Service) stallReason(d Download, it Item, state string, cfg Settings) (string, bool) {
	// Everything downloaded and the client busy with it (checking, moving,
	// unpacking) is not stuck either.
	if cfg.StallMinutes <= 0 || !stallEligible(state) || it.Working || it.Progress >= 1 {
		return "", false
	}
	idle := s.now().Sub(d.ProgressChangedAt)
	if idle < time.Duration(cfg.StallMinutes)*time.Minute {
		return "", false
	}
	return stallText(it.Progress, idle), true
}

// stallText is the reason a download shows: where it stopped, for how long, and
// the likely cause.
func stallText(progress float64, idle time.Duration) string {
	cause := "The source has stopped sending it (no seeds or sources are left)."
	if progress >= 0.99 {
		cause = "The source can't finish it (often an Internet Archive torrent whose info files changed)."
	}
	return "Stuck at " + percentText(progress) + " for " + idleText(idle) + ". " + cause
}

// percentText writes a progress as a percent without rounding it up to 100: a
// download at 99.97% must not read "stuck at 100%". From 99% up it has two
// decimals ("99.97%"), below that none ("40%").
func percentText(progress float64) string {
	p := min(max(progress, 0), 1) * 100
	switch {
	case p >= 100:
		return "100%"
	case p >= 99:
		return strconv.FormatFloat(math.Floor(p*100+1e-6)/100, 'f', 2, 64) + "%"
	}
	return strconv.Itoa(int(math.Floor(p+1e-6))) + "%"
}

func idleText(idle time.Duration) string {
	minutes := int(idle / time.Minute)
	if minutes >= 120 {
		return fmt.Sprintf("%d hours", minutes/60)
	}
	if minutes == 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}

// giveUp fails a download that has made no progress: the reason goes on the
// download, the stream and the Downloads screen hear of it, the download is taken
// out of its client (with its data) when the owner wants that, and the stalled
// handler blocklists the release and searches again. Only the poll that moves
// the row out of an active state does any of this, so it happens once.
func (s *Service) giveUp(ctx context.Context, d Download, it Item, reason string, cfg Settings) {
	claimed, err := s.store.update(ctx, d.ID, activeStates,
		dlUpdate{State: StateFailed, Progress: it.Progress, ETA: -1, Size: it.Size, Error: reason}, s.now())
	if err != nil {
		s.log.Warn("downloads: cannot mark a stalled download failed", "id", d.ID, "err", err)
		return
	}
	if !claimed {
		return
	}
	d.State, d.Error, d.Progress = StateFailed, reason, it.Progress
	s.log.Warn("download gave up: no progress", "id", d.ID, "title", d.TitleID, "release", d.ReleaseTitle, "reason", reason)
	s.publish("failed", d)

	removed := false
	if cfg.RemoveStalled {
		if err := s.removeFromClient(ctx, d, true); err != nil {
			s.log.Warn("downloads: could not remove a stalled download from its client", "id", d.ID, "err", err)
		} else {
			removed = true
		}
	}
	s.notifyStalled(ctx, d.ID, reason, removed)
}

// removeFromClient takes a download out of its client without touching its row.
func (s *Service) removeFromClient(ctx context.Context, d Download, deleteData bool) error {
	a, err := s.adapterFor(ctx, d)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, clientTimeout)
	defer cancel()
	if err := a.Remove(cctx, d.ClientRef, deleteData); err != nil {
		return clientError(err)
	}
	return nil
}

// notifyStalled tells the stalled handler. One that panics must not stop the poll.
func (s *Service) notifyStalled(ctx context.Context, id int64, reason string, removed bool) {
	h := s.stalledHandler()
	if h == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("the stalled-download handler panicked", "id", id, "panic", r)
		}
	}()
	h.OnStalled(ctx, id, reason, removed)
}
