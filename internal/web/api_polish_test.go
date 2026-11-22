package web

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/downloads"
	"github.com/BaileyPillon/marquee/internal/library"
)

// libraryFile writes a file into the movies folder and records it as matched to a
// title, the way an import or a scan would. It returns the row id and the path.
func (a *impApp) libraryFile(rel string, titleID int64, quality string) (int64, string) {
	a.t.Helper()
	ctx := context.Background()
	abs := filepath.Join(a.moviesTo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		a.t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("film data"), 0o644); err != nil {
		a.t.Fatal(err)
	}
	folders, err := a.lib.ListFolders(ctx)
	if err != nil || len(folders) == 0 {
		a.t.Fatalf("folders = %v, %v", folders, err)
	}
	if err := a.lib.ImportFiles(ctx, folders[0].ID, []library.ImportedFile{{RelPath: rel, TitleID: titleID, Quality: quality}}); err != nil {
		a.t.Fatal(err)
	}
	files, _, err := a.lib.Files(ctx, library.FileFilter{FolderID: folders[0].ID, Limit: 1000})
	if err != nil {
		a.t.Fatal(err)
	}
	for _, f := range files {
		if f.RelPath == rel {
			return f.ID, abs
		}
	}
	a.t.Fatalf("no row for %s", rel)
	return 0, ""
}

func fileRoute(id int64, suffix string) string {
	return "/api/v1/library/files/" + itoa(id) + suffix
}

// ---- history text -----------------------------------------------------------------

func TestTitleHistoryItemsCarryPlainEnglishText(t *testing.T) {
	a := newImpApp(t)
	owner, _, member, _ := a.people()
	titleID, _ := a.film(owner)
	for who, c := range map[string]*client{"owner": owner, "member": member} {
		r := c.do("GET", "/api/v1/titles/"+itoa(titleID)+"/history", nil)
		if r.Status != 200 {
			t.Fatalf("%s: %d %s", who, r.Status, r.Body)
		}
		items := r.JSON(t)["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("%s: items = %s", who, r.Body)
		}
		text := map[string]string{}
		for _, raw := range items {
			it := raw.(map[string]any)
			s, ok := it["text"].(string)
			if !ok || s == "" {
				t.Errorf("%s: an item without text: %v", who, it)
			}
			text[it["event"].(string)] = s
			if it["detail"] == nil {
				t.Errorf("the detail is still there for the UI's fallback: %v", it)
			}
		}
		if text["requested"] != "Bailey asked for HD" || text["approved"] != "Approved by Bailey" {
			t.Errorf("%s: texts = %v", who, text)
		}
	}
}

// ---- requests: the title filter, the owner's name, unlimited ---------------------------

func TestRequestListFiltersByTitle(t *testing.T) {
	a := newImpApp(t)
	owner, admin, member, anon := a.people()
	titleID, _ := a.film(owner)

	list := func(c *client, q string) reply {
		t.Helper()
		return c.do("GET", "/api/v1/requests"+q, nil)
	}
	r := list(owner, "?titleId="+itoa(titleID))
	if r.Status != 200 {
		t.Fatalf("by title = %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	items := body["items"].([]any)
	if body["total"] != float64(1) || len(items) != 1 || obj(t, items[0].(map[string]any), "title")["id"] != float64(titleID) {
		t.Errorf("by title = %s", r.Body)
	}
	if r := list(admin, "?titleId="+itoa(titleID)); r.JSON(t)["total"] != float64(1) {
		t.Errorf("an admin sees everyone's: %s", r.Body)
	}
	if r := list(owner, "?titleId=9999"); r.Status != 200 || r.JSON(t)["total"] != float64(0) || len(r.JSON(t)["items"].([]any)) != 0 {
		t.Errorf("a title nobody asked for = %d %s", r.Status, r.Body)
	}
	if r := list(owner, "?titleId="+itoa(titleID)+"&state=declined"); r.JSON(t)["total"] != float64(0) {
		t.Errorf("combined with a state = %s", r.Body)
	}
	// A member only ever sees their own, so the owner's ask is not in their list.
	if r := list(member, "?titleId="+itoa(titleID)); r.Status != 200 || r.JSON(t)["total"] != float64(0) {
		t.Errorf("a member must not see the owner's ask: %d %s", r.Status, r.Body)
	}
	for _, bad := range []string{"?titleId=abc", "?titleId=0", "?titleId=-4", "?titleId=1.5"} {
		if r := list(owner, bad); r.Status != 400 || r.errCode(t) != "invalid_request" {
			t.Errorf("%s = %d %s", bad, r.Status, r.Body)
		}
	}
	if r := list(anon, "?titleId=1"); r.Status != 401 {
		t.Errorf("signed out = %d", r.Status)
	}
}

func TestRequestSummaryNamesTheOwner(t *testing.T) {
	a := newImpApp(t)
	owner, admin, member, anon := a.people()
	for who, c := range map[string]*client{"owner": owner, "admin": admin, "member": member} {
		r := c.do("GET", "/api/v1/requests/summary", nil)
		if r.Status != 200 {
			t.Fatalf("%s = %d %s", who, r.Status, r.Body)
		}
		if got := obj(t, r.JSON(t), "owner")["name"]; got != "Bailey" {
			t.Errorf("%s: owner = %v in %s", who, got, r.Body)
		}
	}
	if r := anon.do("GET", "/api/v1/requests/summary", nil); r.Status != 401 {
		t.Errorf("signed out = %d", r.Status)
	}
}

func TestUnlimitedLimitsOverTheAPI(t *testing.T) {
	a := newImpApp(t)
	owner, admin, member, _ := a.people()
	path := "/api/v1/people/" + itoa(userID(t, member)) + "/limits"

	r := owner.do("PUT", path, map[string]any{"hdPerWeek": "unlimited"})
	if r.Status != 200 {
		t.Fatalf("unlimited = %d %s", r.Status, r.Body)
	}
	person := obj(t, r.JSON(t), "person")
	over, eff, use := obj(t, person, "limits", "overrides"), obj(t, person, "limits", "effective"), obj(t, person, "usage", "hd")
	if over["hdPerWeek"] != nil || over["hdUnlimited"] != true || over["uhdPerWeek"] != nil || over["uhdUnlimited"] != false {
		t.Errorf("overrides = %v", over)
	}
	if eff["hdPerWeek"] != nil || eff["hdUnlimited"] != true || eff["uhdPerWeek"] != float64(2) || eff["uhdUnlimited"] != false {
		t.Errorf("effective = %v", eff)
	}
	if use["limit"] != nil || use["unlimited"] != true {
		t.Errorf("usage = %v", use)
	}
	// The same through GET /people, for the screen.
	people := owner.do("GET", "/api/v1/people", nil).JSON(t)["people"].([]any)
	var found bool
	for _, p := range people {
		if m := p.(map[string]any); m["name"] == "Mia" {
			found = true
			if o := obj(t, m, "limits", "overrides"); o["hdUnlimited"] != true || o["hdPerWeek"] != nil {
				t.Errorf("GET /people overrides = %v", o)
			}
		}
	}
	if !found {
		t.Fatal("Mia is not in /people")
	}

	// The word is not picky about case; a bare -1 is a mistake; so is anything else.
	if r := owner.do("PUT", path, map[string]any{"uhdPerWeek": " Unlimited "}); r.Status != 200 ||
		obj(t, obj(t, r.JSON(t), "person"), "limits", "overrides")["uhdUnlimited"] != true {
		t.Errorf("UNLIMITED = %d %s", r.Status, r.Body)
	}
	if r := owner.do("PUT", path, map[string]any{"hdPerWeek": -1}); r.Status != 400 || r.errCode(t) != "invalid_limit" {
		t.Errorf("-1 = %d %s", r.Status, r.Body)
	}
	for body, want := range map[string]string{`{"hdPerWeek":"lots"}`: "invalid_request", `{"hdPerWeek":true}`: "invalid_request", `{"hdPerWeek":2.5}`: "invalid_request"} {
		if r := rawPut(owner, path, body); r.Status != 400 || r.errCode(t) != want {
			t.Errorf("%s = %d %s", body, r.Status, r.Body)
		}
	}
	// A limit left out stays, and null goes back to the default.
	if r := owner.do("PUT", path, map[string]any{"autoApproveHD": false}); obj(t, obj(t, r.JSON(t), "person"), "limits", "overrides")["hdUnlimited"] != true {
		t.Errorf("leaving the limit out changed it: %s", r.Body)
	}
	r = owner.do("PUT", path, map[string]any{"hdPerWeek": nil, "uhdPerWeek": nil})
	person = obj(t, r.JSON(t), "person")
	if over := obj(t, person, "limits", "overrides"); over["hdUnlimited"] != false || over["hdPerWeek"] != nil || over["uhdUnlimited"] != false {
		t.Errorf("null = %v", over)
	}
	if eff := obj(t, person, "limits", "effective"); eff["hdPerWeek"] != float64(10) || eff["hdUnlimited"] != false {
		t.Errorf("back on the default = %v", eff)
	}
	// Only the owner changes limits.
	if r := admin.do("PUT", path, map[string]any{"hdPerWeek": "unlimited"}); r.Status != 403 {
		t.Errorf("an admin = %d", r.Status)
	}
}

func rawPut(c *client, path, body string) reply {
	c.a.t.Helper()
	req, err := http.NewRequest("PUT", c.a.srv.URL+path, strings.NewReader(body))
	if err != nil {
		c.a.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Marquee", "1")
	resp, err := c.hc.Do(req)
	if err != nil {
		c.a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return reply{Status: resp.StatusCode, Header: resp.Header, Body: b}
}

func TestUnlimitedDefaultsOverTheSettingsAPI(t *testing.T) {
	a := newImpApp(t)
	owner, _, member, _ := a.people()

	r := owner.do("PUT", "/api/v1/settings/requests", map[string]any{"hdPerWeek": "unlimited", "uhdPerWeek": 4})
	if r.Status != 200 {
		t.Fatalf("PUT = %d %s", r.Status, r.Body)
	}
	v := obj(t, r.JSON(t), "value")
	if v["hdPerWeek"] != nil || v["hdUnlimited"] != true || v["uhdPerWeek"] != float64(4) || v["uhdUnlimited"] != false ||
		v["autoApproveHD"] != true {
		t.Errorf("stored section = %v", v)
	}
	// The screen reads it back through the People endpoint, and a member's own limit follows.
	d := obj(t, owner.do("GET", "/api/v1/people", nil).JSON(t), "defaults")
	if d["hdPerWeek"] != nil || d["hdUnlimited"] != true || d["uhdPerWeek"] != float64(4) {
		t.Errorf("defaults = %v", d)
	}
	for _, p := range owner.do("GET", "/api/v1/people", nil).JSON(t)["people"].([]any) {
		m := p.(map[string]any)
		if m["name"] == "Mia" {
			if e := obj(t, m, "limits", "effective"); e["hdUnlimited"] != true || e["hdPerWeek"] != nil || e["uhdPerWeek"] != float64(4) {
				t.Errorf("Mia's effective limits = %v", e)
			}
		}
	}
	if sum := member.do("GET", "/api/v1/requests/summary", nil); obj(t, obj(t, sum.JSON(t), "usage"), "hd")["unlimited"] != true {
		t.Errorf("a member's summary = %s", sum.Body)
	}

	// The stored shape can be sent back as it is (null plus the flag) and as a bare -1.
	if r := owner.do("PUT", "/api/v1/settings/requests", v); r.Status != 200 || obj(t, r.JSON(t), "value")["hdUnlimited"] != true {
		t.Errorf("round trip = %d %s", r.Status, r.Body)
	}
	if r := owner.do("PUT", "/api/v1/settings/requests", map[string]any{"hdPerWeek": -1}); r.Status != 200 ||
		obj(t, r.JSON(t), "value")["hdUnlimited"] != true {
		t.Errorf("-1 = %d %s", r.Status, r.Body)
	}
	// Mistakes are refused, and nothing is stored.
	for name, body := range map[string]map[string]any{
		"text": {"hdPerWeek": "lots"}, "negative": {"uhdPerWeek": -7}, "fraction": {"hdPerWeek": 1.5}, "huge": {"hdPerWeek": 1000000},
	} {
		if r := owner.do("PUT", "/api/v1/settings/requests", body); r.Status != 400 || r.errCode(t) != "invalid_limit" {
			t.Errorf("%s = %d %s", name, r.Status, r.Body)
		}
	}
	if got := obj(t, owner.do("GET", "/api/v1/settings/requests", nil).JSON(t), "value"); got["hdUnlimited"] != true {
		t.Errorf("a refused save changed the section: %v", got)
	}
	// Folders still pass through, and a plain number is a plain number.
	r = owner.do("PUT", "/api/v1/settings/requests", map[string]any{"hdPerWeek": 7, "folders": map[string]any{"movie": map[string]any{"hd": 1}}})
	v = obj(t, r.JSON(t), "value")
	if v["hdPerWeek"] != float64(7) || v["hdUnlimited"] != false || obj(t, v, "folders", "movie")["hd"] != float64(1) {
		t.Errorf("a plain number = %v", v)
	}
}

// ---- downloads: the import step, the target and the outcome ----------------------------

func TestDownloadsShowTheImportStepTheTargetAndTheOutcome(t *testing.T) {
	a := newImpApp(t)
	owner, admin, member, _ := a.people()
	titleID, dlID := a.film(owner)
	ctx := context.Background()

	a.setState(dlID, downloads.StateImporting)
	if err := a.dl.SetImportStep(ctx, dlID, downloads.StepMoving, `D:\Movies\Night of the Living Dead (1968)\Night of the Living Dead (1968) Bluray-1080p.mkv`); err != nil {
		t.Fatal(err)
	}
	find := func(c *client, q string) map[string]any {
		t.Helper()
		r := c.do("GET", "/api/v1/downloads"+q, nil)
		if r.Status != 200 {
			t.Fatalf("GET /downloads%s = %d %s", q, r.Status, r.Body)
		}
		for _, d := range r.JSON(t)["downloads"].([]any) {
			if m := d.(map[string]any); m["id"] == float64(dlID) {
				return m
			}
		}
		t.Fatalf("download %d is not in %s", dlID, r.Body)
		return nil
	}
	for who, c := range map[string]*client{"owner": owner, "admin": admin} {
		d := find(c, "")
		if d["state"] != "importing" || d["importStep"] != "moving" || d["outcome"] != "" ||
			d["importTarget"] != `D:\Movies\Night of the Living Dead (1968)\Night of the Living Dead (1968) Bluray-1080p.mkv` {
			t.Errorf("%s sees %v", who, d)
		}
	}
	// A member never reaches the list at all, so the path of the owner's folders stays private.
	if r := member.do("GET", "/api/v1/downloads", nil); r.Status != 403 || strings.Contains(string(r.Body), "Movies") {
		t.Errorf("a member = %d %s", r.Status, r.Body)
	}
	// The summary and the single-download answers do not carry the path either.
	if r := owner.do("GET", "/api/v1/downloads/summary", nil); strings.Contains(string(r.Body), "importTarget") {
		t.Errorf("summary = %s", r.Body)
	}

	// Finished: imported, step done; and an import that replaced a worse file is "upgraded".
	a.setState(dlID, downloads.StateImported)
	d := find(owner, "?state=history")
	if d["importStep"] != "done" || d["outcome"] != "imported" {
		t.Errorf("imported = %v", d)
	}
	if _, err := a.mainDB.Exec(`INSERT INTO history (at, title_id, download_id, event) VALUES ('2026-11-21T12:00:00.000Z', ?, ?, 'upgraded')`, titleID, dlID); err != nil {
		t.Fatal(err)
	}
	if d := find(owner, "?state=history"); d["outcome"] != "upgraded" {
		t.Errorf("upgraded = %v", d)
	}
	a.setState(dlID, downloads.StateFailed)
	if d := find(owner, "?state=history"); d["outcome"] != "failed" {
		t.Errorf("failed = %v", d)
	}
}

// ---- renaming and deleting a library file ------------------------------------------------

func TestFileRoutesAreLeftOutWithoutTheImporter(t *testing.T) {
	a := newImpApp(t, func(d *Deps) { d.Importer = nil })
	owner, _, _, _ := a.people()
	for _, rt := range []struct{ method, path string }{{"POST", fileRoute(1, "/rename")}, {"DELETE", fileRoute(1, "")}} {
		if r := owner.do(rt.method, rt.path, nil); r.Status != 404 || r.errCode(t) != "not_found" {
			t.Errorf("%s %s = %d %s", rt.method, rt.path, r.Status, r.Body)
		}
	}
}

func TestRenameFileRoute(t *testing.T) {
	a := newImpApp(t)
	owner, admin, member, anon := a.people()
	titleID, _ := a.film(owner)
	id, oldAbs := a.libraryFile("Night.of.the.Living.Dead.1968.1080p.BluRay.mkv", titleID, "Bluray-1080p")
	route := fileRoute(id, "/rename")

	if r := anon.do("POST", route, nil); r.Status != 401 {
		t.Errorf("signed out = %d", r.Status)
	}
	if r := member.do("POST", route, nil); r.Status != 403 || r.errCode(t) != "forbidden" {
		t.Errorf("member = %d %s", r.Status, r.Body)
	}
	if r := owner.send("POST", route, nil); r.Status != 403 || r.errCode(t) != "csrf" {
		t.Errorf("no csrf header = %d %s", r.Status, r.Body)
	}
	seeKey := a.keyFor(owner, "see")
	if r := a.newClient().send("POST", route, nil, bearer(seeKey)...); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("a key without the admin tick = %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(oldAbs); err != nil {
		t.Fatalf("a refused rename moved the file: %v", err)
	}

	// An admin may rename.
	r := admin.do("POST", route, nil)
	if r.Status != 200 {
		t.Fatalf("admin = %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	want := "Night of the Living Dead (1968)/Night of the Living Dead (1968) Bluray-1080p.mkv"
	if body["renamed"] != true || body["from"] != "Night.of.the.Living.Dead.1968.1080p.BluRay.mkv" || body["to"] != want {
		t.Errorf("body = %v", body)
	}
	if f := obj(t, body, "file"); f["relPath"] != want || f["id"] != float64(id) || f["quality"] != "Bluray-1080p" {
		t.Errorf("file = %v", f)
	}
	if s, ok := body["servers"].([]any); !ok || len(s) != 0 {
		t.Errorf("servers = %v, want an empty list", body["servers"])
	}
	if _, err := os.Stat(filepath.Join(a.moviesTo, filepath.FromSlash(want))); err != nil {
		t.Errorf("the file is not at its new name: %v", err)
	}
	if _, err := os.Stat(oldAbs); err == nil {
		t.Error("the old name is still there")
	}

	// Again: nothing to do, and nothing new in the history.
	if r := owner.do("POST", route, nil); r.Status != 200 || r.JSON(t)["renamed"] != false || r.JSON(t)["to"] != want {
		t.Errorf("second rename = %d %s", r.Status, r.Body)
	}
	hist := owner.do("GET", "/api/v1/titles/"+itoa(titleID)+"/history", nil).JSON(t)["items"].([]any)
	renames := 0
	for _, raw := range hist {
		if it := raw.(map[string]any); it["event"] == "renamed" {
			renames++
			if it["text"] != "Renamed Night.of.the.Living.Dead.1968.1080p.BluRay.mkv to Night of the Living Dead (1968) Bluray-1080p.mkv" {
				t.Errorf("history text = %v", it["text"])
			}
		}
	}
	if renames != 1 {
		t.Errorf("%d renamed entries in %v", renames, hist)
	}

	// Mistakes.
	for path, wantStatus := range map[string]int{fileRoute(99999, "/rename"): 404, "/api/v1/library/files/abc/rename": 404, "/api/v1/library/files/0/rename": 404} {
		if r := owner.do("POST", path, nil); r.Status != wantStatus || r.errCode(t) != "not_found" {
			t.Errorf("%s = %d %s", path, r.Status, r.Body)
		}
	}
}

func TestRenameFileRouteErrors(t *testing.T) {
	a := newImpApp(t)
	owner, _, _, _ := a.people()
	titleID, _ := a.film(owner)

	// The name is taken by a different file: refused, nothing touched.
	id, oldAbs := a.libraryFile("film.mkv", titleID, "Bluray-1080p")
	taken := filepath.Join(a.moviesTo, "Night of the Living Dead (1968)", "Night of the Living Dead (1968) Bluray-1080p.mkv")
	if err := os.MkdirAll(filepath.Dir(taken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taken, []byte("another file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := owner.do("POST", fileRoute(id, "/rename"), nil); r.Status != 409 || r.errCode(t) != "name_taken" {
		t.Errorf("name taken = %d %s", r.Status, r.Body)
	}
	if b, _ := os.ReadFile(taken); string(b) != "another file" {
		t.Error("a file was overwritten")
	}

	// The file has vanished from the disk.
	if err := os.Remove(oldAbs); err != nil {
		t.Fatal(err)
	}
	if r := owner.do("POST", fileRoute(id, "/rename"), nil); r.Status != 409 || r.errCode(t) != "file_missing" {
		t.Errorf("missing = %d %s", r.Status, r.Body)
	}

	// A file the scanner could not match to a title.
	if err := os.WriteFile(filepath.Join(a.moviesTo, "holiday video.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	folders, _ := a.lib.ListFolders(context.Background())
	if _, err := a.lib.ScanNow(context.Background(), folders[0].ID); err != nil {
		t.Fatal(err)
	}
	files, _, _ := a.lib.Files(context.Background(), library.FileFilter{State: library.StateUnmatched, Limit: 10})
	var holiday int64
	for _, f := range files {
		if f.RelPath == "holiday video.mkv" {
			holiday = f.ID
		}
	}
	if holiday == 0 {
		t.Fatalf("unmatched = %+v", files)
	}
	if r := owner.do("POST", fileRoute(holiday, "/rename"), nil); r.Status != 409 || r.errCode(t) != "not_matched" {
		t.Errorf("unmatched = %d %s", r.Status, r.Body)
	}

	// A row that says a path outside its folder: refused, and the file there is safe.
	id2, _ := a.libraryFile("another.mkv", titleID, "Bluray-1080p")
	outside := filepath.Join(filepath.Dir(a.moviesTo), "precious.txt")
	if err := os.WriteFile(outside, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.mainDB.Exec(`UPDATE files SET rel_path = '../precious.txt' WHERE id = ?`, id2); err != nil {
		t.Fatal(err)
	}
	if r := owner.do("POST", fileRoute(id2, "/rename"), nil); r.Status != 403 || r.errCode(t) != "outside_library" {
		t.Errorf("outside = %d %s", r.Status, r.Body)
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep me" {
		t.Error("a file outside the library was touched")
	}
}

func TestDeleteFileRoute(t *testing.T) {
	a := newImpApp(t)
	owner, admin, member, anon := a.people()
	titleID, _ := a.film(owner)

	id, abs := a.libraryFile("Night of the Living Dead (1968)/Night of the Living Dead (1968) Bluray-1080p.mkv", titleID, "Bluray-1080p")
	route := fileRoute(id, "")

	if r := anon.do("DELETE", route+"?deleteFile=true", nil); r.Status != 401 {
		t.Errorf("signed out = %d", r.Status)
	}
	for who, c := range map[string]*client{"member": member, "admin": admin} {
		if r := c.do("DELETE", route+"?deleteFile=true", nil); r.Status != 403 || r.errCode(t) != "forbidden" {
			t.Errorf("%s = %d %s (only the owner deletes)", who, r.Status, r.Body)
		}
	}
	if r := owner.send("DELETE", route+"?deleteFile=true", nil); r.Status != 403 || r.errCode(t) != "csrf" {
		t.Errorf("no csrf header = %d %s", r.Status, r.Body)
	}
	noDelete := a.keyFor(owner, "settings", "admin") // an owner's key, but without the delete tick
	if r := a.newClient().send("DELETE", route+"?deleteFile=true", nil, bearer(noDelete)...); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("a key without the delete tick = %d %s", r.Status, r.Body)
	}
	if r := owner.do("DELETE", route+"?deleteFile=maybe", nil); r.Status != 400 || r.errCode(t) != "invalid_request" {
		t.Errorf("a bad flag = %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("a refused delete removed the file: %v", err)
	}

	// With the delete tick a key works, and removes the file from the disk.
	key := a.keyFor(owner, "delete")
	r := a.newClient().send("DELETE", route+"?deleteFile=true", nil, bearer(key)...)
	if r.Status != 200 {
		t.Fatalf("delete = %d %s", r.Status, r.Body)
	}
	body := r.JSON(t)
	if body["deleted"] != true || body["fromDisk"] != true || body["wasMissing"] != false ||
		body["path"] != "Night of the Living Dead (1968)/Night of the Living Dead (1968) Bluray-1080p.mkv" {
		t.Errorf("body = %v", body)
	}
	if s, ok := body["servers"].([]any); !ok || len(s) != 0 {
		t.Errorf("servers = %v", body["servers"])
	}
	if _, err := os.Stat(abs); err == nil {
		t.Error("the file is still on the disk")
	}
	if r := owner.do("DELETE", route+"?deleteFile=true", nil); r.Status != 404 || r.errCode(t) != "not_found" {
		t.Errorf("deleting twice = %d %s", r.Status, r.Body)
	}
	for _, p := range []string{"/api/v1/library/files/abc", "/api/v1/library/files/0"} {
		if r := owner.do("DELETE", p, nil); r.Status != 404 {
			t.Errorf("%s = %d", p, r.Status)
		}
	}
	hist := owner.do("GET", "/api/v1/titles/"+itoa(titleID)+"/history", nil).JSON(t)["items"].([]any)
	first := hist[0].(map[string]any)
	if first["event"] != "deleted" || first["text"] != "Deleted Night of the Living Dead (1968) Bluray-1080p.mkv from disk" {
		t.Errorf("newest history = %v", first)
	}
}

func TestDeleteFileRouteWithoutTheDiskAndForAMissingFile(t *testing.T) {
	a := newImpApp(t)
	owner, _, _, _ := a.people()
	titleID, _ := a.film(owner)

	// No deleteFile: only the row goes; the file stays.
	id, abs := a.libraryFile("a.mkv", titleID, "WEBDL-720p")
	r := owner.do("DELETE", fileRoute(id, ""), nil)
	if r.Status != 200 || r.JSON(t)["fromDisk"] != false || r.JSON(t)["deleted"] != true {
		t.Fatalf("row only = %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Error("the file must stay when deleteFile is not set")
	}
	if r := owner.do("DELETE", fileRoute(id, "")+"?deleteFile=false", nil); r.Status != 404 {
		t.Errorf("the row is gone: %d", r.Status)
	}

	// A file that is already gone from the disk is not an error.
	id2, abs2 := a.libraryFile("b.mkv", titleID, "WEBDL-720p")
	if err := os.Remove(abs2); err != nil {
		t.Fatal(err)
	}
	r = owner.do("DELETE", fileRoute(id2, "")+"?deleteFile=true", nil)
	if r.Status != 200 || r.JSON(t)["wasMissing"] != true || r.JSON(t)["fromDisk"] != true {
		t.Errorf("a missing file = %d %s", r.Status, r.Body)
	}
}

func TestDeleteFileRouteRefusesPathsOutsideTheLibrary(t *testing.T) {
	a := newImpApp(t)
	owner, _, _, _ := a.people()
	titleID, _ := a.film(owner)
	outside := filepath.Join(filepath.Dir(a.moviesTo), "precious.txt")
	if err := os.WriteFile(outside, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, _ := a.libraryFile("x.mkv", titleID, "WEBDL-720p")
	for _, bad := range []string{"../precious.txt", "..\\precious.txt", "a/../../precious.txt", outside, "/precious.txt", "C:\\precious.txt", ""} {
		if _, err := a.mainDB.Exec(`UPDATE files SET rel_path = ? WHERE id = ?`, bad, id); err != nil {
			t.Fatal(err)
		}
		r := owner.do("DELETE", fileRoute(id, "")+"?deleteFile=true", nil)
		if r.Status != 403 || r.errCode(t) != "outside_library" {
			t.Errorf("%q = %d %s", bad, r.Status, r.Body)
		}
		if b, err := os.ReadFile(outside); err != nil || string(b) != "keep me" {
			t.Fatalf("a file outside the library was touched with path %q: %v", bad, err)
		}
	}
	// Even asking only for the row to go, a path outside the folder is not trusted.
	if _, err := a.mainDB.Exec(`UPDATE files SET rel_path = '../precious.txt' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if r := owner.do("DELETE", fileRoute(id, ""), nil); r.Status != 403 {
		t.Errorf("row-only delete of a bad path = %d %s", r.Status, r.Body)
	}
}

// ---- the watchlist settings ------------------------------------------------------------------

func TestPlexWatchlistSettingsRoute(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()
	member := a.member(owner, "Priya")
	admin := a.admin(owner, "Adam")
	path := "/api/v1/watchlists/plex"

	if r := a.newClient().do("PUT", path, map[string]any{}); r.Status != 401 {
		t.Errorf("signed out = %d", r.Status)
	}
	for name, c := range map[string]*client{"member": member, "admin": admin} {
		if r := c.do("PUT", path, map[string]any{"enabled": false}); r.Status != 403 || r.errCode(t) != "forbidden" {
			t.Errorf("%s = %d %s", name, r.Status, r.Body)
		}
	}
	if r := a.key(owner, "see").send("PUT", path, map[string]any{"enabled": false}); r.Status != 403 || r.errCode(t) != "insufficient_scope" {
		t.Errorf("a key without the settings tick = %d %s", r.Status, r.Body)
	}
	if r := owner.send("PUT", path, map[string]any{"enabled": false}); r.Status != 403 || r.errCode(t) != "csrf" {
		t.Errorf("no csrf header = %d %s", r.Status, r.Body)
	}

	// All four, and the answer is the status the screen reads.
	r := owner.do("PUT", path, map[string]any{"enabled": false, "includeFriends": false, "autoCreateMembers": true, "quality": "uhd"})
	if r.Status != 200 {
		t.Fatalf("PUT = %d %s", r.Status, r.Body)
	}
	plex := obj(t, r.JSON(t), "plex")
	if plex["enabled"] != false || plex["includeFriends"] != false || plex["autoCreateMembers"] != true || plex["quality"] != "uhd" || plex["linked"] != false {
		t.Errorf("plex = %v", plex)
	}
	got := obj(t, owner.do("GET", "/api/v1/watchlists", nil).JSON(t), "plex")
	if got["enabled"] != false || got["includeFriends"] != false || got["autoCreateMembers"] != true || got["quality"] != "uhd" {
		t.Errorf("GET after PUT = %v", got)
	}

	// A field left out keeps its value.
	r = owner.do("PUT", path, map[string]any{"enabled": true})
	plex = obj(t, r.JSON(t), "plex")
	if plex["enabled"] != true || plex["includeFriends"] != false || plex["autoCreateMembers"] != true || plex["quality"] != "uhd" {
		t.Errorf("partial = %v", plex)
	}
	// Mistakes: nothing changes.
	for name, body := range map[string]string{
		"quality":   `{"quality":"8k"}`,
		"empty":     `{"quality":""}`,
		"text bool": `{"enabled":"yes"}`,
		"unknown":   `{"enabled":true,"colour":"red"}`,
		"list":      `[1]`,
	} {
		if r := rawPut(owner, path, body); r.Status != 400 {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if r := rawPut(owner, path, `{"quality":"8k"}`); r.errCode(t) != "invalid_request" {
		t.Errorf("code = %s", r.Body)
	}
	if got := obj(t, owner.do("GET", "/api/v1/watchlists", nil).JSON(t), "plex"); got["quality"] != "uhd" || got["enabled"] != true {
		t.Errorf("a refused change was kept: %v", got)
	}
	// An empty body changes nothing and is not an error.
	if r := owner.do("PUT", path, map[string]any{}); r.Status != 200 {
		t.Errorf("empty = %d %s", r.Status, r.Body)
	}
}
