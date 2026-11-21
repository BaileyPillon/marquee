package alerts

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/BaileyPillon/marquee/internal/core/events"
)

// The hub topics the service listens to. The names are the other modules' own
// (requests.Topic, downloads.Topic, the importer's library topic); they are
// repeated here because this module may not import its own layer.
var topics = []string{"requests", "downloads", "library", HealthTopic}

// Run listens to the event hub until ctx ends, and sends what it hears. A
// listener the hub drops for being slow is replaced at once, so a burst of
// events cannot silence the alerts. Run returns when ctx ends or the hub closes.
func (s *Service) Run(ctx context.Context) {
	if s.o.Hub == nil {
		return
	}
	for ctx.Err() == nil {
		c, unsubscribe := s.o.Hub.Subscribe(topics...)
		s.listen(ctx, c)
		unsubscribe()
		if !c.Dropped() {
			return // the hub is closed, or ctx ended
		}
		s.log.Warn("alerts: the event hub dropped the listener for being slow; listening again")
	}
}

func (s *Service) listen(ctx context.Context, c *events.Client) {
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-c.C():
			if !ok {
				return
			}
			s.handle(ctx, m)
		}
	}
}

// handle turns one hub message into an event, when it is one worth telling.
func (s *Service) handle(ctx context.Context, m events.Message) {
	var p map[string]any
	if err := json.Unmarshal(m.Data, &p); err != nil {
		return
	}
	switch m.Topic {
	case "requests":
		s.onRequest(ctx, p)
	case "downloads":
		s.onDownload(ctx, p)
	case "library":
		s.onLibrary(ctx, p)
	case HealthTopic:
		s.onHealth(ctx, p)
	}
}

func str(p map[string]any, k string) string {
	s, _ := p[k].(string)
	return s
}

func num(p map[string]any, k string) int64 {
	f, _ := p[k].(float64)
	return int64(f)
}

// ---- requests -----------------------------------------------------------------

func (s *Service) onRequest(ctx context.Context, p map[string]any) {
	id := num(p, "id")
	var typ string
	switch ev := str(p, "event"); {
	case ev == "created":
		typ = EventRequestCreated
	case ev == "approved":
		typ = EventRequestApproved
	case ev == "declined":
		typ = EventRequestDeclined
	case ev == "state" && str(p, "state") == "available":
		typ = EventTitleAvailable
	default:
		return
	}
	if id == 0 || s.o.Requests == nil {
		return
	}
	r, err := s.o.Requests.Request(ctx, id)
	if err != nil {
		s.log.Debug("alerts: could not read the ask an event is about", "id", id, "err", err)
		return
	}
	title, body := requestMessage(typ, r)
	s.Notify(ctx, Event{
		Type: typ, Title: title, Body: body, Key: strconv.FormatInt(id, 10),
		Data: map[string]any{"requestId": id, "state": r.State, "quality": r.Quality, "kind": r.Kind, "title": r.TitleName, "year": r.Year, "user": r.UserName},
	})
}

// qualityWord is how people say a quality.
func qualityWord(q string) string {
	if q == "uhd" {
		return "4K"
	}
	return "HD"
}

// nameWithYear is "Metropolis (1927)"; an artist has no year worth showing.
func nameWithYear(r RequestInfo) string {
	if r.Year > 0 && r.Kind != "artist" {
		return r.TitleName + " (" + strconv.Itoa(r.Year) + ")"
	}
	return r.TitleName
}

// requestMessage writes the short, plain message for an ask event.
func requestMessage(typ string, r RequestInfo) (title, body string) {
	what := nameWithYear(r) + " in " + qualityWord(r.Quality)
	who := r.UserName
	if who == "" {
		who = "Someone"
	}
	switch typ {
	case EventRequestCreated:
		return "New ask", who + " asked for " + what + " — waiting for your yes"
	case EventRequestApproved:
		switch {
		case r.DecidedByName == "":
			return "Ask approved", who + "'s ask for " + what + " was approved automatically"
		case r.DecidedByName == r.UserName:
			return "Added", who + " added " + what
		}
		return "Ask approved", r.DecidedByName + " approved " + who + "'s ask for " + what
	case EventRequestDeclined:
		by := r.DecidedByName
		if by == "" {
			by = "The owner"
		}
		body = by + " declined " + who + "'s ask for " + what
		if r.DeclineReason != "" {
			body += ": " + shorten(r.DeclineReason, 200)
		}
		return "Ask declined", body
	default: // title.available
		return "Ready to watch", what + " is in the library, asked for by " + who
	}
}

func shorten(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// ---- downloads and imports ------------------------------------------------------

// importFailPrefix starts the error text of a download whose import failed (the
// importer writes it).
const importFailPrefix = "Import failed: "

func (s *Service) onDownload(ctx context.Context, p map[string]any) {
	// {"type": "failed", "id": N, "titleId": N, "state": "failed", ...}. Progress
	// messages carry no id and are ignored.
	if str(p, "type") != "failed" || num(p, "id") == 0 || s.o.Downloads == nil {
		return
	}
	id := num(p, "id")
	d, err := s.o.Downloads.Download(ctx, id)
	if err != nil {
		s.log.Debug("alerts: could not read the download an event is about", "id", id, "err", err)
		return
	}
	name := d.ReleaseTitle
	if name == "" {
		name = "A download"
	}
	reason := strings.TrimSpace(d.Error)
	if after, isImport := strings.CutPrefix(reason, importFailPrefix); isImport {
		body := "The import of " + name + " failed"
		if after = strings.TrimSpace(after); after != "" {
			body += ": " + shorten(after, 240)
		}
		s.Notify(ctx, Event{Type: EventImportFailed, Title: "Import failed", Body: body, Key: strconv.FormatInt(id, 10),
			Data: map[string]any{"downloadId": id, "titleId": d.TitleID, "release": d.ReleaseTitle}})
		return
	}
	body := name + " failed to download"
	if reason != "" {
		body += ": " + shorten(reason, 240)
	}
	s.Notify(ctx, Event{Type: EventDownloadFailed, Title: "Download failed", Body: body, Key: strconv.FormatInt(id, 10),
		Data: map[string]any{"downloadId": id, "titleId": d.TitleID, "release": d.ReleaseTitle}})
}

// onLibrary reads the library topic: {"event": "import.failed", "titleId": N,
// "message": "..."} (what a module that does not go through the downloads
// service publishes when an import fails). Other library events ("imported" and
// so on) are not alerts.
func (s *Service) onLibrary(ctx context.Context, p map[string]any) {
	ev := strings.NewReplacer("_", ".", "-", ".").Replace(strings.ToLower(firstText(str(p, "event"), str(p, "type"))))
	if ev != "import.failed" {
		return
	}
	msg := shorten(firstText(str(p, "message"), str(p, "error"), str(p, "reason")), 240)
	body := "An import failed"
	if msg != "" {
		body += ": " + msg
	}
	key := firstText(strconv.FormatInt(num(p, "downloadId"), 10), strconv.FormatInt(num(p, "titleId"), 10))
	s.Notify(ctx, Event{Type: EventImportFailed, Title: "Import failed", Body: body, Key: key + ":" + msg, Data: p})
}

func firstText(v ...string) string {
	for _, s := range v {
		if s = strings.TrimSpace(s); s != "" && s != "0" {
			return s
		}
	}
	return ""
}

// onHealth reads the health topic: {"event": "problem", "key": "...",
// "message": "..."}.
func (s *Service) onHealth(ctx context.Context, p map[string]any) {
	if str(p, "event") != "problem" {
		return
	}
	msg := shorten(str(p, "message"), 300)
	if msg == "" {
		return
	}
	key := firstText(str(p, "key"), msg)
	s.Notify(ctx, Event{Type: EventHealthProblem, Title: "Something needs a look", Body: msg, Key: key})
}
