package web

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

const hookSecretURLPart = "very-secret-webhook-token"

func (a *m7App) hookPaths() []string {
	a.hookMu.Lock()
	defer a.hookMu.Unlock()
	return append([]string(nil), a.hooks...)
}

func TestAlertRoutesAreForTheOwnerOnly(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()
	member := a.member(owner, "Priya")
	admin := a.admin(owner, "Adam")
	noTick := a.key(owner, "see")
	for _, rt := range []struct{ method, path string }{
		{"GET", "/api/v1/alerts"},
		{"POST", "/api/v1/alerts"},
		{"PUT", "/api/v1/alerts/1"},
		{"DELETE", "/api/v1/alerts/1"},
		{"POST", "/api/v1/alerts/1/test"},
	} {
		if r := a.newClient().do(rt.method, rt.path, map[string]any{}); r.Status != http.StatusUnauthorized {
			t.Errorf("%s %s signed out = %d", rt.method, rt.path, r.Status)
		}
		for name, c := range map[string]*client{"member": member, "admin": admin} {
			if r := c.do(rt.method, rt.path, map[string]any{}); r.Status != http.StatusForbidden {
				t.Errorf("%s %s as %s = %d %s", rt.method, rt.path, name, r.Status, r.Body)
			}
		}
		if r := noTick.send(rt.method, rt.path, map[string]any{}); r.Status != http.StatusForbidden {
			t.Errorf("%s %s with a key without the settings tick = %d", rt.method, rt.path, r.Status)
		}
	}
}

func TestAlertChannelsOverTheAPI(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()

	r := owner.send("GET", "/api/v1/alerts", nil)
	list := r.JSON(t)
	if r.Status != 200 || len(list["alerts"].([]any)) != 0 || len(list["kinds"].([]any)) != 7 || len(list["events"].([]any)) != 7 {
		t.Fatalf("empty list = %d %s", r.Status, r.Body)
	}

	// Create. The secret comes back masked, never in the clear.
	url := a.hook.URL + "/" + hookSecretURLPart
	r = owner.do("POST", "/api/v1/alerts", map[string]any{
		"name": "Phone", "kind": "webhook", "config": map[string]any{"url": url, "secret": "hmac-secret-12345"},
		"events": []string{"request.created", "download.failed"},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("create = %d %s", r.Status, r.Body)
	}
	if strings.Contains(string(r.Body), hookSecretURLPart) || strings.Contains(string(r.Body), "hmac-secret-12345") {
		t.Errorf("a secret is in the answer: %s", r.Body)
	}
	ch := r.JSON(t)["alert"].(map[string]any)
	id := int64(ch["id"].(float64))
	cfg := ch["config"].(map[string]any)
	if ch["name"] != "Phone" || ch["kind"] != "webhook" || ch["enabled"] != true || ch["lastOkAt"] != nil || ch["lastError"] != "" || ch["createdAt"] == nil ||
		!strings.HasPrefix(cfg["url"].(string), "••••") || !strings.HasPrefix(cfg["secret"].(string), "••••") {
		t.Errorf("alert = %v", ch)
	}
	if evs := ch["events"].([]any); len(evs) != 2 || evs[0] != "request.created" || evs[1] != "download.failed" {
		t.Errorf("events = %v", evs)
	}

	// The list holds it, masked too.
	r = owner.send("GET", "/api/v1/alerts", nil)
	if strings.Contains(string(r.Body), hookSecretURLPart) {
		t.Errorf("a secret is in the list: %s", r.Body)
	}
	if got := r.JSON(t)["alerts"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != float64(id) {
		t.Errorf("list = %s", r.Body)
	}

	// Test: the message goes to the real address (the secret behind the mask).
	r = owner.do("POST", "/api/v1/alerts/"+strconv.FormatInt(id, 10)+"/test", nil)
	if res := r.JSON(t); r.Status != 200 || res["ok"] != true || res["error"] != nil {
		t.Errorf("test = %d %s", r.Status, r.Body)
	}
	if paths := a.hookPaths(); len(paths) != 1 || paths[0] != "/"+hookSecretURLPart {
		t.Errorf("the test went to %v", paths)
	}
	if ch := owner.send("GET", "/api/v1/alerts", nil).JSON(t)["alerts"].([]any)[0].(map[string]any); ch["lastOkAt"] == nil {
		t.Errorf("lastOkAt not set after a good test: %v", ch)
	}

	// Update with what the UI was shown: the masked secrets keep the stored ones.
	body := map[string]any{"name": "Phone 2", "config": cfg, "events": []string{"health.problem"}, "enabled": false}
	r = owner.do("PUT", "/api/v1/alerts/"+strconv.FormatInt(id, 10), body)
	if r.Status != 200 {
		t.Fatalf("update = %d %s", r.Status, r.Body)
	}
	up := r.JSON(t)["alert"].(map[string]any)
	if up["name"] != "Phone 2" || up["enabled"] != false || len(up["events"].([]any)) != 1 {
		t.Errorf("updated = %v", up)
	}
	// A disabled channel can still be tested, and the old address still works.
	r = owner.do("POST", "/api/v1/alerts/"+strconv.FormatInt(id, 10)+"/test", nil)
	if r.JSON(t)["ok"] != true || len(a.hookPaths()) != 2 || a.hookPaths()[1] != "/"+hookSecretURLPart {
		t.Errorf("second test = %s, paths %v", r.Body, a.hookPaths())
	}

	// Delete.
	if r := owner.do("DELETE", "/api/v1/alerts/"+strconv.FormatInt(id, 10), nil); r.Status != http.StatusNoContent {
		t.Errorf("delete = %d %s", r.Status, r.Body)
	}
	if r := owner.do("DELETE", "/api/v1/alerts/"+strconv.FormatInt(id, 10), nil); r.Status != http.StatusNotFound || r.errCode(t) != "not_found" {
		t.Errorf("second delete = %d %s", r.Status, r.Body)
	}
}

func TestAlertTestReportsAFailureInPlainWords(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()
	r := owner.do("POST", "/api/v1/alerts", map[string]any{"name": "Broken", "kind": "webhook", "config": map[string]any{"url": a.hook.URL + "/broken"}})
	id := int64(r.JSON(t)["alert"].(map[string]any)["id"].(float64))
	r = owner.do("POST", "/api/v1/alerts/"+strconv.FormatInt(id, 10)+"/test", nil)
	res := r.JSON(t)
	if r.Status != http.StatusOK || res["ok"] != false || res["error"] != "The webhook answered HTTP 500: nope" {
		t.Errorf("test = %d %s", r.Status, r.Body)
	}
	if ch := owner.send("GET", "/api/v1/alerts", nil).JSON(t)["alerts"].([]any)[0].(map[string]any); ch["lastError"] != "The webhook answered HTTP 500: nope" || ch["lastOkAt"] != nil {
		t.Errorf("channel = %v", ch)
	}
	if r := owner.do("POST", "/api/v1/alerts/999/test", nil); r.Status != http.StatusNotFound || r.errCode(t) != "not_found" {
		t.Errorf("test on nothing = %d %s", r.Status, r.Body)
	}
}

func TestAlertErrorsUseTheStandardShape(t *testing.T) {
	a := newM7App(t)
	owner := a.owner()
	owner.do("POST", "/api/v1/alerts", map[string]any{"name": "One", "kind": "webhook", "config": map[string]any{"url": "https://x.test/h"}})
	for name, tc := range map[string]struct {
		method, path string
		body         any
		status       int
		code         string
	}{
		"no body":         {"POST", "/api/v1/alerts", nil, 400, "invalid_json"},
		"no name":         {"POST", "/api/v1/alerts", map[string]any{"kind": "webhook", "config": map[string]any{"url": "https://x.test/h"}}, 400, "invalid_request"},
		"bad kind":        {"POST", "/api/v1/alerts", map[string]any{"name": "x", "kind": "sms"}, 400, "invalid_request"},
		"missing setting": {"POST", "/api/v1/alerts", map[string]any{"name": "x", "kind": "telegram", "config": map[string]any{"chatId": "1"}}, 400, "invalid_request"},
		"name taken":      {"POST", "/api/v1/alerts", map[string]any{"name": "one", "kind": "webhook", "config": map[string]any{"url": "https://x.test/h"}}, 409, "name_taken"},
		"update nothing":  {"PUT", "/api/v1/alerts/999", map[string]any{"name": "x"}, 404, "not_found"},
		"bad id":          {"PUT", "/api/v1/alerts/abc", map[string]any{"name": "x"}, 404, "not_found"},
		"change the kind": {"PUT", "/api/v1/alerts/1", map[string]any{"kind": "discord"}, 400, "invalid_request"},
		"unknown event":   {"PUT", "/api/v1/alerts/1", map[string]any{"events": []string{"x"}}, 400, "invalid_request"},
	} {
		r := owner.do(tc.method, tc.path, tc.body)
		if r.Status != tc.status || r.errCode(t) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, r.Status, r.Body, tc.status, tc.code)
		}
	}
	// A missing CSRF header is refused like everywhere else.
	if r := owner.send("POST", "/api/v1/alerts", map[string]any{}); r.Status != http.StatusForbidden || r.errCode(t) != "csrf" {
		t.Errorf("without X-Marquee = %d %s", r.Status, r.Body)
	}
}
