package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// newImportingDownload grabs a torrent and puts it into the importing state.
func newImportingDownload(t *testing.T, e *env) int64 {
	t.Helper()
	title := e.addTitle("Metropolis", 1927)
	e.addClient("qb", KindQBittorrent, "http://qb:8080")
	e.fake.at("http://qb:8080").fixedRef = bbbHex // grabbing it again is the same download
	id, err := e.svc.Grab(context.Background(), e.target(title), torrentRelease(bbbHex))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetState(context.Background(), id, StateImporting, ""); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestImportStepFollowsTheImport(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := newImportingDownload(t, e)
	if d := e.download(id); d.ImportStep != "" || d.ImportTarget != "" {
		t.Fatalf("a download that has not started its import has a step: %q %q", d.ImportStep, d.ImportTarget)
	}
	ev := e.collect()

	steps := []struct{ step, target string }{
		{StepChecking, ""}, {StepRenaming, "/library/Metropolis (1927)/Metropolis (1927) Bluray-1080p.mkv"}, {StepMoving, ""}, {StepRescanning, ""},
	}
	for _, s := range steps {
		if err := e.svc.SetImportStep(ctx, id, s.step, s.target); err != nil {
			t.Fatalf("%s: %v", s.step, err)
		}
		d := e.download(id)
		if d.ImportStep != s.step {
			t.Errorf("step = %q, want %q", d.ImportStep, s.step)
		}
	}
	// The target is kept by the steps that do not name one.
	const target = "/library/Metropolis (1927)/Metropolis (1927) Bluray-1080p.mkv"
	if d := e.download(id); d.ImportTarget != target {
		t.Errorf("target = %q, want %q", d.ImportTarget, target)
	}

	// Every change goes out on the stream as a "state" event, with the step but never the path.
	got := ev.drain()
	if len(got) != len(steps) {
		t.Fatalf("events = %v", got)
	}
	for i, m := range got {
		if m["type"] != "state" || m["importStep"] != steps[i].step || m["state"] != StateImporting || m["id"] != float64(id) {
			t.Errorf("event %d = %v", i, m)
		}
		for k, v := range m {
			if strings.Contains(strings.ToLower(k), "target") || strings.Contains(strings.ToLower(k), "path") {
				t.Errorf("event %d carries %s: %v", i, k, v)
			}
			if s, ok := v.(string); ok && strings.Contains(s, "/library") {
				t.Errorf("event %d carries a path in %s: %v", i, k, v)
			}
		}
	}

	// Done follows the state, and it keeps where the files went.
	if err := e.svc.SetState(ctx, id, StateImported, ""); err != nil {
		t.Fatal(err)
	}
	if d := e.download(id); d.ImportStep != StepDone || d.ImportTarget != target {
		t.Errorf("after the import: %q %q", d.ImportStep, d.ImportTarget)
	}
	if last := ev.drain(); len(last) != 1 || last[0]["importStep"] != StepDone {
		t.Errorf("the final event = %v", last)
	}

	// The API view has both.
	v, err := e.svc.Get(ctx, id)
	if err != nil || v.ImportStep != "done" || v.ImportTarget != target {
		t.Errorf("view = %+v, %v", v, err)
	}
	b, _ := json.Marshal(v)
	if !strings.Contains(string(b), `"importStep":"done"`) || !strings.Contains(string(b), `"importTarget":"/library/Metropolis`) {
		t.Errorf("view JSON = %s", b)
	}
	list, _, err := e.svc.List(ctx, ListFilter{State: FilterHistory})
	if err != nil || len(list) != 1 || list[0].ImportStep != "done" {
		t.Errorf("history list = %+v, %v", list, err)
	}
}

func TestImportStepIsKeptWhenTheImportFailsAndClearedWhenItStartsAgain(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := newImportingDownload(t, e)
	if err := e.svc.SetImportStep(ctx, id, StepMoving, "/library/x.mkv"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetState(ctx, id, StateFailed, "Import failed: disk full"); err != nil {
		t.Fatal(err)
	}
	if d := e.download(id); d.ImportStep != StepMoving || d.ImportTarget != "/library/x.mkv" {
		t.Errorf("a failure should say where it stopped: %q %q", d.ImportStep, d.ImportTarget)
	}
	// A late step cannot change a download that is no longer importing.
	if err := e.svc.SetImportStep(ctx, id, StepRescanning, "/elsewhere"); err != nil {
		t.Fatal(err)
	}
	if d := e.download(id); d.ImportStep != StepMoving || d.ImportTarget != "/library/x.mkv" {
		t.Errorf("a step arrived after the failure and was kept: %q %q", d.ImportStep, d.ImportTarget)
	}
	// Importing again starts with nothing.
	if err := e.svc.SetState(ctx, id, StateImporting, ""); err != nil {
		t.Fatal(err)
	}
	if d := e.download(id); d.ImportStep != "" || d.ImportTarget != "" {
		t.Errorf("a new import starts with a step: %q %q", d.ImportStep, d.ImportTarget)
	}
	// And so does a download that goes round again after it was imported.
	if err := e.svc.SetImportStep(ctx, id, StepChecking, "/library/y.mkv"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetState(ctx, id, StateImported, ""); err != nil {
		t.Fatal(err)
	}
	title := e.download(id).TitleID
	again, err := e.svc.Grab(ctx, e.target(title), torrentRelease(bbbHex))
	if err != nil || again != id {
		t.Fatalf("re-grab = %d, %v", again, err)
	}
	if d := e.download(id); d.State != StateQueued || d.ImportStep != "" || d.ImportTarget != "" {
		t.Errorf("a re-grabbed download keeps the old import: %s %q %q", d.State, d.ImportStep, d.ImportTarget)
	}
}

func TestSetImportStepChecksTheStep(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := newImportingDownload(t, e)
	var inv *InvalidError
	if err := e.svc.SetImportStep(ctx, id, "teleporting", ""); !errors.As(err, &inv) {
		t.Errorf("an unknown step = %v, want an InvalidError", err)
	}
	if err := e.svc.SetImportStep(ctx, id, StepDone, ""); err != nil {
		t.Errorf("done: %v", err)
	}
	if err := e.svc.SetImportStep(ctx, id, "", ""); err != nil {
		t.Errorf("clearing the step: %v", err)
	}
	if d := e.download(id); d.ImportStep != "" {
		t.Errorf("step = %q", d.ImportStep)
	}
	// The database refuses a step the code does not know, whatever writes it.
	if _, err := e.db.Exec(`UPDATE downloads SET import_step = 'teleporting' WHERE id = ?`, id); err == nil {
		t.Error("the database accepted an unknown import step")
	}
}

func TestOutcomeOfAFinishedDownload(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := newImportingDownload(t, e)
	outcome := func() string {
		t.Helper()
		v, err := e.svc.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return v.Outcome
	}
	if got := outcome(); got != "" {
		t.Errorf("a download still going has outcome %q", got)
	}
	if err := e.svc.SetState(ctx, id, StateImported, ""); err != nil {
		t.Fatal(err)
	}
	if got := outcome(); got != OutcomeImported {
		t.Errorf("imported: %q", got)
	}
	// An import that replaced a worse file wrote an "upgraded" entry for the download.
	e.writeHistory(id, "imported")
	if got := outcome(); got != OutcomeImported {
		t.Errorf("an imported entry does not make an upgrade: %q", got)
	}
	e.writeHistory(id+1, "upgraded") // somebody else's
	if got := outcome(); got != OutcomeImported {
		t.Errorf("another download's upgrade: %q", got)
	}
	e.writeHistory(id, "upgraded")
	if got := outcome(); got != OutcomeUpgraded {
		t.Errorf("upgraded: %q", got)
	}
	list, _, err := e.svc.List(ctx, ListFilter{State: FilterHistory})
	if err != nil || len(list) != 1 || list[0].Outcome != "upgraded" {
		t.Errorf("history list = %+v, %v", list, err)
	}
	// Failed and removed say so, upgrade entry or not; an upgrade only counts for an imported download.
	if err := e.svc.SetState(ctx, id, StateFailed, "Import failed: x"); err != nil {
		t.Fatal(err)
	}
	if got := outcome(); got != OutcomeFailed {
		t.Errorf("failed: %q", got)
	}
	if err := e.svc.SetState(ctx, id, StateRemoved, ""); err != nil {
		t.Fatal(err)
	}
	if got := outcome(); got != OutcomeRemoved {
		t.Errorf("removed: %q", got)
	}
	b, _ := json.Marshal(mustView(t, e, id))
	if !strings.Contains(string(b), `"outcome":"removed"`) {
		t.Errorf("view JSON = %s", b)
	}
}

func mustView(t *testing.T, e *env, id int64) View {
	t.Helper()
	v, err := e.svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// writeHistory adds a history row for a download, as the importer does.
func (e *env) writeHistory(downloadID int64, event string) {
	e.t.Helper()
	if _, err := e.db.Exec(`INSERT INTO history (at, title_id, download_id, event) VALUES ('2026-11-21T12:00:00.000Z', 1, ?, ?)`, downloadID, event); err != nil {
		e.t.Fatal(err)
	}
}
