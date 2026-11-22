package downloads

import (
	"context"
	"time"
)

// PollJob is the scheduler job. The scheduler ticks it every PollTick; it only
// does the work when a poll is due: every few seconds while something is in
// progress, otherwise once a minute.
func (s *Service) PollJob(ctx context.Context) error {
	s.pollMu.Lock()
	interval := idleInterval
	if s.active.Load() {
		interval = activeInterval
	}
	due := s.lastPoll.IsZero() || s.now().Sub(s.lastPoll) >= interval-dueSlack
	s.pollMu.Unlock()
	if !due {
		return nil
	}
	return s.Poll(ctx)
}

// Poll reads the category of every enabled client and brings the downloads
// table up to date: progress, speed, ETA and state; a transition to finished
// stamps completed_at, publishes an event and tells the CompletedHandler once;
// a client error state marks the download failed; a download that has gained no
// progress for the "downloads" setting stallMinutes is given up on (see stall.go);
// a download missing from its client for two polls in a row is marked removed.
//
// A client that does not answer is skipped (and its error recorded); nothing
// is marked removed because of a failed read.
func (s *Service) Poll(ctx context.Context) error {
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	s.lastPoll = s.now()

	clients, err := s.store.enabledClients(ctx)
	if err != nil {
		return err
	}
	cfg := s.settings(ctx)
	for _, c := range clients {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.pollClient(ctx, c, cfg)
	}
	busy, err := s.store.anyBusy(ctx)
	if err != nil {
		return err
	}
	s.active.Store(busy)
	return nil
}

func (s *Service) pollClient(ctx context.Context, c clientRow, cfg Settings) {
	tracked, err := s.store.trackedFor(ctx, c.ID)
	if err != nil {
		s.log.Warn("downloads: cannot read tracked downloads", "client", c.ID, "err", err)
		return
	}
	a, err := s.adapter(c)
	var items []Item
	if err == nil {
		lctx, cancel := context.WithTimeout(ctx, clientTimeout)
		items, err = a.List(lctx, c.Category)
		cancel()
	}
	if err != nil {
		msg := PlainError(err)
		if c.LastError != msg { // say it once, not every five seconds
			s.log.Warn("download client does not answer", "client", c.Name, "err", msg)
		}
		if c.LastError != msg { // the first time it is seen; the stored time says since when
			_ = s.store.clientFailed(context.WithoutCancel(ctx), c.ID, msg, s.now())
		}
		return
	}
	// Healthy and said so within the last minute: no need to write it again.
	if c.LastError != "" || c.LastOKAt == nil || s.now().Sub(*c.LastOKAt) >= time.Minute {
		_ = s.store.clientOK(context.WithoutCancel(ctx), c.ID, s.now())
	}

	byRef := make(map[string]Item, len(items))
	for _, it := range items {
		byRef[it.Ref] = it
	}
	var progress []map[string]any
	for _, d := range tracked {
		it, found := byRef[d.ClientRef]
		if !found {
			s.missing(ctx, d)
			continue
		}
		delete(s.misses, d.ID)
		if changed := s.apply(ctx, d, it, cfg); changed != nil {
			progress = append(progress, changed)
		}
	}
	if len(progress) > 0 && s.hub != nil {
		s.hub.Publish(Topic, map[string]any{"type": "progress", "items": progress})
	}
}

// missing handles a download its client no longer lists.
func (s *Service) missing(ctx context.Context, d Download) {
	s.misses[d.ID]++
	if s.misses[d.ID] < missesToRemove {
		return
	}
	delete(s.misses, d.ID)
	ok, err := s.store.update(ctx, d.ID, activeStates, dlUpdate{State: StateRemoved, Progress: d.Progress, ETA: -1}, s.now())
	if err != nil {
		s.log.Warn("downloads: cannot mark a download removed", "id", d.ID, "err", err)
		return
	}
	if ok {
		d.State = StateRemoved
		s.publish("removed", d)
	}
}

// apply writes what the client says about one download. It returns a progress
// entry for the "progress" event when something changed, nil otherwise.
func (s *Service) apply(ctx context.Context, d Download, it Item, cfg Settings) map[string]any {
	now := s.now()
	switch {
	case it.Finished:
		path := it.ContentPath
		u := dlUpdate{
			State: StateCompleted, Progress: 1, ETA: 0, Size: it.Size,
			SavePath: it.SavePath, ContentPath: path, Completed: true,
		}
		// Only the poll that moves the row out of an active state gets to say
		// "finished", so the handler hears about each download once.
		claimed, err := s.store.update(ctx, d.ID, activeStates, u, now)
		if err != nil {
			s.log.Warn("downloads: cannot mark a download completed", "id", d.ID, "err", err)
			return nil
		}
		if claimed {
			d.State, d.Progress, d.ContentPath, d.SavePath = StateCompleted, 1, path, it.SavePath
			s.publish("completed", d)
			s.notifyCompleted(ctx, d.ID)
		}
		return nil

	case it.State == StateFailed:
		msg := it.Error
		if msg == "" {
			msg = "The download client reported an error."
		}
		claimed, err := s.store.update(ctx, d.ID, activeStates, dlUpdate{State: StateFailed, Progress: it.Progress, ETA: -1, Size: it.Size, Error: msg}, now)
		if err != nil {
			s.log.Warn("downloads: cannot mark a download failed", "id", d.ID, "err", err)
			return nil
		}
		if claimed {
			d.State, d.Error = StateFailed, msg
			s.publish("failed", d)
		}
		return nil
	}

	state := it.State
	if state == "" || !inList(activeStates, state) {
		state = StateDownloading
	}
	// The stall clock starts over when the download gains progress and when it
	// starts or resumes (it leaves queued or paused): waiting is not stalling.
	moved := it.Progress > d.Progress || (!stallEligible(d.State) && stallEligible(state))
	if !moved {
		if reason, stuck := s.stallReason(d, it, state, cfg); stuck {
			s.giveUp(ctx, d, it, reason, cfg)
			return nil
		}
	}
	if state == d.State && it.Progress == d.Progress && it.SpeedBps == d.SpeedBps && it.ETA == d.ETA &&
		(it.Size == 0 || it.Size == d.Size) {
		return nil // nothing moved: no write, no event
	}
	claimed, err := s.store.update(ctx, d.ID, activeStates, dlUpdate{
		State: state, Progress: it.Progress, SpeedBps: it.SpeedBps, ETA: it.ETA, Size: it.Size,
		SavePath: it.SavePath, ContentPath: "", Moved: moved,
	}, now)
	if err != nil {
		s.log.Warn("downloads: cannot update a download", "id", d.ID, "err", err)
		return nil
	}
	if !claimed {
		return nil
	}
	return map[string]any{"id": d.ID, "state": state, "progress": it.Progress, "speedBps": it.SpeedBps, "etaSeconds": etaOrNil(it.ETA)}
}

func etaOrNil(eta int) any {
	if eta < 0 {
		return nil
	}
	return eta
}

// notifyCompleted tells the importer. A handler that panics must not stop the
// rest of the poll.
func (s *Service) notifyCompleted(ctx context.Context, id int64) {
	h := s.completedHandler()
	if h == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("the completed-download handler panicked", "id", id, "panic", r)
		}
	}()
	h.OnCompleted(ctx, id)
}
