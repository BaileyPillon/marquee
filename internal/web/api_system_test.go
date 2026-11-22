package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
)

type fakeJobs []jobs.Status

func (f fakeJobs) Status() []jobs.Status { return f }

// configDirWith makes a config folder holding the two database files at known sizes.
func configDirWith(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, size := range map[string]int{db.MainFile: 4096, db.MainFile + "-wal": 100, db.EventsFile: 2048} {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestHealthStaysPublicAndSmall(t *testing.T) {
	a := newApp(t, nil)
	r := a.newClient().send("GET", "/api/v1/health", nil)
	if r.Status != 200 {
		t.Fatalf("health = %d %s", r.Status, r.Body)
	}
	m := r.JSON(t)
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	if len(keys) != 4 || m["status"] != "ok" || m["version"] != "9.9.9-test" || m["db"] != "ok" || m["uptimeSeconds"] == nil {
		t.Errorf("health = %v: it must stay the small public answer", m)
	}
	for _, leak := range []string{"configDir", "jobs", "connections", "databases", "disk", "path"} {
		if strings.Contains(string(r.Body), leak) {
			t.Errorf("health leaks %q: %s", leak, r.Body)
		}
	}
}

func TestSystemStatusIsForTheOwnerAndAdmins(t *testing.T) {
	a := newImpApp(t, func(d *Deps) { d.ConfigDir = configDirWith(t) })
	owner, admin, member, anon := a.people()
	path := "/api/v1/system/status"

	if r := anon.do("GET", path, nil); r.Status != 401 || r.errCode(t) != "unauthorized" {
		t.Errorf("signed out = %d %s", r.Status, r.Body)
	}
	if r := member.do("GET", path, nil); r.Status != 403 || r.errCode(t) != "forbidden" {
		t.Errorf("member = %d %s", r.Status, r.Body)
	}
	for who, c := range map[string]*client{"owner": owner, "admin": admin} {
		if r := c.do("GET", path, nil); r.Status != 200 {
			t.Errorf("%s = %d %s", who, r.Status, r.Body)
		}
	}
	// A key needs the admin tick.
	if r := a.newClient().send("GET", path, nil, bearer(a.keyFor(owner, "see"))...); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("a key without the admin tick = %d %s", r.Status, r.Body)
	}
	if r := a.newClient().send("GET", path, nil, bearer(a.keyFor(owner, "admin"))...); r.Status != 200 {
		t.Errorf("a key with the admin tick = %d %s", r.Status, r.Body)
	}
	if r := owner.do("POST", path, nil); r.Status == 200 {
		t.Errorf("POST = %d", r.Status)
	}
}

func TestSystemStatusShape(t *testing.T) {
	dir := configDirWith(t)
	lastRun := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	a := newImpApp(t, func(d *Deps) {
		d.ConfigDir = dir
		d.TrustedProxies = []string{"10.0.0.0/8"}
		d.Jobs = fakeJobs{
			{Name: "downloads.poll", Interval: 5 * time.Second, Runs: 40, LastRun: lastRun, Duration: 120 * time.Millisecond},
			{Name: "library.scan", Interval: 6 * time.Hour, Runs: 3, Failures: 1, LastRun: lastRun, Duration: 2 * time.Second, LastErr: "library: folder is gone", Running: true},
			{Name: "never.ran", Interval: time.Minute},
		}
	})
	owner, _, _, _ := a.people()
	// One download app that answers, one media server that does not.
	a.film(owner)
	jelly := fakeJelly(t, `{"Items":[]}`)
	if r := owner.do("POST", "/api/v1/mediaservers", map[string]any{"name": "Jellyfin", "kind": "jellyfin", "url": jelly.URL, "token": "wrong-token-0123456789"}); r.Status != 201 {
		t.Fatalf("add server = %d %s", r.Status, r.Body)
	}
	id := int64(owner.do("GET", "/api/v1/mediaservers", nil).JSON(t)["servers"].([]any)[0].(map[string]any)["id"].(float64))
	owner.do("POST", "/api/v1/mediaservers/"+itoa(id)+"/test", nil) // records the refusal on the row
	clients := owner.do("GET", "/api/v1/clients", nil).JSON(t)["clients"].([]any)
	owner.do("POST", "/api/v1/clients/"+itoa(int64(clients[0].(map[string]any)["id"].(float64)))+"/test", nil)
	if r := owner.do("PUT", "/api/v1/settings/server", map[string]any{"publicUrl": "https://marquee.example.com", "trustedProxies": []string{"192.168.0.0/16"}}); r.Status != 200 {
		t.Fatalf("server settings = %d %s", r.Status, r.Body)
	}

	r := owner.do("GET", "/api/v1/system/status", nil)
	if r.Status != 200 {
		t.Fatalf("status = %d %s", r.Status, r.Body)
	}
	m := r.JSON(t)
	if m["version"] != "9.9.9-test" || m["configDir"] != dir || m["repository"] != "https://github.com/BaileyPillon/marquee" || m["license"] != "MIT" {
		t.Errorf("identity = %v", m)
	}
	if g, _ := m["go"].(string); !strings.HasPrefix(g, "go") || m["os"] == "" || m["arch"] == "" {
		t.Errorf("runtime = %v %v %v", m["go"], m["os"], m["arch"])
	}
	if up, ok := m["uptimeSeconds"].(float64); !ok || up < 0 {
		t.Errorf("uptime = %v", m["uptimeSeconds"])
	}
	if _, err := time.Parse(time.RFC3339, m["startedAt"].(string)); err != nil {
		t.Errorf("startedAt = %v", m["startedAt"])
	}

	dbs := m["databases"].([]any)
	if len(dbs) != 2 {
		t.Fatalf("databases = %v", dbs)
	}
	main, events := dbs[0].(map[string]any), dbs[1].(map[string]any)
	if main["name"] != "main" || main["file"] != "marquee.db" || main["ok"] != true || main["sizeBytes"] != float64(4096+100) {
		t.Errorf("main = %v (the size is the file plus its write-ahead log)", main)
	}
	if events["name"] != "events" || events["ok"] != true || events["sizeBytes"] != float64(2048) {
		t.Errorf("events = %v", events)
	}
	disk := obj(t, m, "disk")
	if disk["path"] != dir || disk["freeBytes"].(float64) <= 0 || disk["totalBytes"].(float64) < disk["freeBytes"].(float64) {
		t.Errorf("disk = %v", disk)
	}

	js := m["jobs"].([]any)
	if len(js) != 3 {
		t.Fatalf("jobs = %v", js)
	}
	byName := map[string]map[string]any{}
	for _, j := range js {
		jm := j.(map[string]any)
		byName[jm["name"].(string)] = jm
	}
	poll, scan, never := byName["downloads.poll"], byName["library.scan"], byName["never.ran"]
	if poll["intervalSeconds"] != float64(5) || poll["runs"] != float64(40) || poll["lastRun"] != "2026-11-21T12:00:00Z" ||
		poll["lastDurationMs"] != float64(120) || poll["lastError"] != "" || poll["running"] != false {
		t.Errorf("poll = %v", poll)
	}
	if scan["failures"] != float64(1) || scan["lastError"] != "library: folder is gone" || scan["running"] != true || scan["intervalSeconds"] != float64(21600) {
		t.Errorf("scan = %v", scan)
	}
	if never["lastRun"] != nil || never["runs"] != float64(0) {
		t.Errorf("a job that never ran = %v", never)
	}

	// Each connection's ok and last error.
	conns := map[string]map[string]any{}
	for _, c := range m["connections"].([]any) {
		cm := c.(map[string]any)
		conns[cm["kind"].(string)+"/"+cm["name"].(string)] = cm
	}
	qb := conns["downloadClient/qb"]
	if qb == nil || qb["app"] != "qbittorrent" || qb["ok"] != true || qb["lastError"] != "" || qb["lastOkAt"] == nil {
		t.Errorf("the download app = %v", qb)
	}
	jf := conns["mediaServer/Jellyfin"]
	if jf == nil || jf["app"] != "jellyfin" || jf["ok"] != false || jf["lastError"] == "" || jf["lastOkAt"] != nil {
		t.Errorf("the media server (it refused the token) = %v", jf)
	}
	for _, c := range m["connections"].([]any) {
		if cm := c.(map[string]any); strings.Contains(strings.ToLower(cm["lastError"].(string)), "wrong-token") {
			t.Errorf("a token leaked into the status: %v", cm)
		}
	}
	if strings.Contains(string(r.Body), "wrong-token-0123456789") {
		t.Error("the media server's token is in the status")
	}

	srv := obj(t, m, "server")
	if srv["publicUrl"] != "https://marquee.example.com" ||
		len(srv["trustedProxies"].([]any)) != 1 || srv["trustedProxies"].([]any)[0] != "192.168.0.0/16" ||
		len(srv["trustedFromEnvironment"].([]any)) != 1 || srv["trustedFromEnvironment"].([]any)[0] != "10.0.0.0/8" {
		t.Errorf("server = %v", srv)
	}
}

func TestSystemStatusWithLittleWired(t *testing.T) {
	a := newApp(t, nil) // no download apps, no jobs, no config folder
	owner := a.owner()
	r := owner.do("GET", "/api/v1/system/status", nil)
	if r.Status != 200 {
		t.Fatalf("status = %d %s", r.Status, r.Body)
	}
	m := r.JSON(t)
	if jobs, ok := m["jobs"].([]any); !ok || len(jobs) != 0 {
		t.Errorf("jobs = %v, want an empty list", m["jobs"])
	}
	if conns, ok := m["connections"].([]any); !ok || len(conns) != 0 {
		t.Errorf("connections = %v, want an empty list", m["connections"])
	}
	if m["disk"] != nil || m["configDir"] != "" {
		t.Errorf("without a config folder: disk %v configDir %v", m["disk"], m["configDir"])
	}
	srv := obj(t, m, "server")
	if srv["publicUrl"] != "" || len(srv["trustedProxies"].([]any)) != 0 || len(srv["trustedFromEnvironment"].([]any)) != 0 {
		t.Errorf("server = %v", srv)
	}
	if dbs := m["databases"].([]any); len(dbs) != 2 || dbs[0].(map[string]any)["ok"] != true {
		t.Errorf("databases = %v", dbs)
	}
	_ = http.StatusOK
}
