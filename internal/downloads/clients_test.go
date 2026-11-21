package downloads

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/settings"
)

const (
	longPassword = "correct horse battery staple" // 28 characters: the mask shows the last four
	cliKey       = "0123456789abcdef0123456789abcdef"
)

func (e *env) sealedColumns(id int64) (password, key string) {
	e.t.Helper()
	if err := e.db.QueryRow(`SELECT password_sealed, api_key_sealed FROM clients WHERE id = ?`, id).Scan(&password, &key); err != nil {
		e.t.Fatal(err)
	}
	return
}

func TestClientSecretsAreSealedAndMasked(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.addClient("qb", KindQBittorrent, "http://qb:8080", func(in *ClientInput) {
		in.Username = str("admin")
		in.Password = str(longPassword)
		in.APIKey = str(cliKey)
	})

	// What comes back is masked, in the same style as settings.
	if c.Password != settings.Mask(longPassword) || c.Password != "••••aple" || c.APIKey != "••••cdef" {
		t.Errorf("masks = %q %q", c.Password, c.APIKey)
	}
	if c.Username != "admin" {
		t.Errorf("username = %q (it is not a secret)", c.Username)
	}
	b, _ := json.Marshal(c)
	for _, secret := range []string{longPassword, cliKey} {
		if strings.Contains(string(b), secret) {
			t.Errorf("the view leaks %q: %s", secret, b)
		}
	}
	list, _ := e.svc.Clients(ctx)
	b, _ = json.Marshal(list)
	if strings.Contains(string(b), longPassword) || strings.Contains(string(b), cliKey) {
		t.Errorf("the list leaks a secret: %s", b)
	}

	// In the database only sealed text is kept, and it opens with the master key.
	pw, key := e.sealedColumns(c.ID)
	if pw == "" || strings.Contains(pw, longPassword) || strings.Contains(key, cliKey) {
		t.Errorf("sealed columns = %q %q", pw, key)
	}
	if plain, err := e.box.Open(pw); err != nil || plain != longPassword {
		t.Errorf("password opens to %q, %v", plain, err)
	}
	if plain, err := e.box.Open(key); err != nil || plain != cliKey {
		t.Errorf("key opens to %q, %v", plain, err)
	}

	// The adapter is built with the real values.
	if _, err := e.svc.TestClient(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if cfg := e.fake.at("http://qb:8080").cfg; cfg.Password != longPassword || cfg.APIKey != cliKey || cfg.Username != "admin" {
		t.Errorf("adapter config = %+v", cfg)
	}

	// A short secret shows only the dots.
	short := e.addClient("deluge", KindDeluge, "http://d:8112", func(in *ClientInput) { in.Password = str("deluge") })
	if short.Password != "••••" || short.APIKey != "" {
		t.Errorf("short masks = %q %q", short.Password, short.APIKey)
	}
}

func TestUpdateClientKeepsReplacesAndClearsSecrets(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.addClient("qb", KindQBittorrent, "http://qb:8080", func(in *ClientInput) {
		in.Username = str("admin")
		in.Password = str(longPassword)
	})
	pw0, _ := e.sealedColumns(c.ID)

	// Left out: kept. The other fields keep their values too.
	v, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{Name: str("renamed")})
	if err != nil {
		t.Fatal(err)
	}
	if pw, _ := e.sealedColumns(c.ID); pw != pw0 {
		t.Error("an update that did not mention the password changed it")
	}
	if v.Name != "renamed" || v.URL != "http://qb:8080" || v.Username != "admin" || v.Category != DefaultCategory ||
		!v.Enabled || v.Priority != 1 {
		t.Errorf("partial update = %+v", v)
	}
	// Sent back masked: kept.
	if _, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{Password: str(v.Password)}); err != nil {
		t.Fatal(err)
	}
	if pw, _ := e.sealedColumns(c.ID); pw != pw0 {
		t.Error("a masked password sent back changed the stored one")
	}
	// A new value replaces it, and the adapter is rebuilt with it.
	if _, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{Password: str("a brand new password")}); err != nil {
		t.Fatal(err)
	}
	if pw, _ := e.sealedColumns(c.ID); pw == pw0 {
		t.Error("the new password was not stored")
	}
	if _, err := e.svc.TestClient(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if got := e.fake.at("http://qb:8080").cfg.Password; got != "a brand new password" {
		t.Errorf("adapter still has the old password: %q", got)
	}
	// Empty clears it.
	v, err = e.svc.UpdateClient(ctx, c.ID, ClientInput{Password: str("")})
	if err != nil {
		t.Fatal(err)
	}
	if pw, _ := e.sealedColumns(c.ID); pw != "" || v.Password != "" {
		t.Errorf("password not cleared: %q %q", pw, v.Password)
	}
	// Other fields change too.
	v, err = e.svc.UpdateClient(ctx, c.ID, ClientInput{
		Category: str("films"), SavePath: str(`M:\dl`), Enabled: boolp(false), Priority: intp(7), Username: str("bob"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Category != "films" || v.SavePath != `M:\dl` || v.Enabled || v.Priority != 7 || v.Username != "bob" {
		t.Errorf("update = %+v", v)
	}
	// An empty category goes back to the default; an empty save path clears it.
	v, _ = e.svc.UpdateClient(ctx, c.ID, ClientInput{Category: str(""), SavePath: str("")})
	if v.Category != DefaultCategory || v.SavePath != "" {
		t.Errorf("category/save path = %q %q", v.Category, v.SavePath)
	}
}

func TestChangingTheAddressNeverSendsASavedSecretToTheNewHost(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.addClient("sab", KindSABnzbd, "http://sab:8080")

	// A different host with the stored key kept: refused.
	if _, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{URL: str("http://evil.example:8080")}); err == nil {
		t.Fatal("moved a saved key to another host")
	} else {
		var ie *InvalidError
		if !errors.As(err, &ie) || !strings.Contains(ie.Message, "again") {
			t.Errorf("err = %v", err)
		}
	}
	if _, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{URL: str("http://evil.example:8080"), APIKey: str(c.APIKey)}); err == nil {
		t.Error("a masked key sent with a new host was accepted")
	}
	// A different port is a different host too.
	if _, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{URL: str("http://sab:9090")}); err == nil {
		t.Error("moved a saved key to another port")
	}
	// Retyped for the new host: fine.
	v, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{URL: str("http://new-sab:8080"), APIKey: str("another-key-value-1234")})
	if err != nil || v.URL != "http://new-sab:8080" {
		t.Fatalf("retyped key: %+v, %v", v, err)
	}
	// Same host, different path or scheme: not a move.
	if _, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{URL: str("https://new-sab:8080/sabnzbd/")}); err != nil {
		t.Errorf("path/scheme change on the same host: %v", err)
	}
	// No secret stored: any address will do.
	q := e.addClient("qb", KindQBittorrent, "http://qb:8080")
	if _, err := e.svc.UpdateClient(ctx, q.ID, ClientInput{URL: str("http://elsewhere:8080")}); err != nil {
		t.Errorf("client without secrets: %v", err)
	}
}

func TestClientKindAndNameRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.addClient("qb", KindQBittorrent, "http://qb:8080")

	if _, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{Kind: str(KindDeluge)}); err == nil {
		t.Error("changed the kind of a client")
	}
	if _, err := e.svc.UpdateClient(ctx, c.ID, ClientInput{Kind: str(KindQBittorrent)}); err != nil {
		t.Errorf("same kind: %v", err)
	}
	if _, err := e.svc.UpdateClient(ctx, 999, ClientInput{Name: str("x")}); !errors.Is(err, ErrClientNotFound) {
		t.Errorf("unknown id: %v", err)
	}

	// Names are unique, whatever the case.
	if _, err := e.svc.AddClient(ctx, ClientInput{Name: str("QB"), Kind: str(KindDeluge), URL: str("http://d:8112")}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("duplicate name: %v", err)
	}
	other := e.addClient("other", KindDeluge, "http://d:8112")
	if _, err := e.svc.UpdateClient(ctx, other.ID, ClientInput{Name: str("qb")}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("renaming onto another client: %v", err)
	}
	if _, err := e.svc.UpdateClient(ctx, other.ID, ClientInput{Name: str("other")}); err != nil {
		t.Errorf("renaming to its own name: %v", err)
	}
}

func TestAddClientValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	long := strings.Repeat("x", 101)
	cases := map[string]ClientInput{
		"no kind":            {Name: str("a"), URL: str("http://a:1")},
		"unknown kind":       {Name: str("a"), Kind: str("plex"), URL: str("http://a:1")},
		"transmission":       {Name: str("a"), Kind: str(KindTransmission), URL: str("http://a:1")},
		"nzbget":             {Name: str("a"), Kind: str(KindNZBGet), URL: str("http://a:1")},
		"no name":            {Kind: str(KindQBittorrent), URL: str("http://a:1")},
		"blank name":         {Name: str("   "), Kind: str(KindQBittorrent), URL: str("http://a:1")},
		"long name":          {Name: str(long), Kind: str(KindQBittorrent), URL: str("http://a:1")},
		"control in name":    {Name: str("a\x00b"), Kind: str(KindQBittorrent), URL: str("http://a:1")},
		"no url":             {Name: str("a"), Kind: str(KindQBittorrent)},
		"bad url":            {Name: str("a"), Kind: str(KindQBittorrent), URL: str("qb:8080")},
		"ftp url":            {Name: str("a"), Kind: str(KindQBittorrent), URL: str("ftp://a")},
		"sab without key":    {Name: str("a"), Kind: str(KindSABnzbd), URL: str("http://a:1")},
		"priority too low":   {Name: str("a"), Kind: str(KindQBittorrent), URL: str("http://a:1"), Priority: intp(-1)},
		"priority too high":  {Name: str("a"), Kind: str(KindQBittorrent), URL: str("http://a:1"), Priority: intp(1000)},
		"category too long":  {Name: str("a"), Kind: str(KindQBittorrent), URL: str("http://a:1"), Category: str(strings.Repeat("c", 65))},
		"save path too long": {Name: str("a"), Kind: str(KindQBittorrent), URL: str("http://a:1"), SavePath: str(strings.Repeat("p", 501))},
		"secret too long":    {Name: str("a"), Kind: str(KindQBittorrent), URL: str("http://a:1"), Password: str(strings.Repeat("p", 501))},
	}
	for name, in := range cases {
		_, err := e.svc.AddClient(ctx, in)
		var ie *InvalidError
		if !errors.As(err, &ie) || ie.Message == "" {
			t.Errorf("%s: %v, want an InvalidError with a message", name, err)
		}
	}
	if list, _ := e.svc.Clients(ctx); len(list) != 0 {
		t.Errorf("rejected clients were stored: %v", list)
	}
	// Defaults.
	c := e.addClient("ok", KindQBittorrent, "http://ok:8080/")
	if c.Category != "marquee" || !c.Enabled || c.Priority != 1 || c.URL != "http://ok:8080" || c.SavePath != "" ||
		c.LastOKAt != nil || c.LastError != "" || c.CreatedAt.IsZero() {
		t.Errorf("defaults = %+v", c)
	}
}

func TestDeleteClient(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.addClient("qb", KindQBittorrent, "http://qb:8080")
	title := e.addTitle("Metropolis", 1927)
	id, err := e.svc.Grab(ctx, e.target(title), torrentRelease(bbbHex))
	if err != nil {
		t.Fatal(err)
	}
	// Still downloading: refused.
	if err := e.svc.DeleteClient(ctx, c.ID); !errors.Is(err, ErrClientInUse) {
		t.Fatalf("delete with a live download: %v", err)
	}
	// Waiting for the importer counts as live too.
	_ = e.svc.SetState(ctx, id, StateCompleted, "")
	if err := e.svc.DeleteClient(ctx, c.ID); !errors.Is(err, ErrClientInUse) {
		t.Fatalf("delete with a completed download: %v", err)
	}
	_ = e.svc.SetState(ctx, id, StateImported, "")
	if err := e.svc.DeleteClient(ctx, c.ID); err != nil {
		t.Fatalf("delete with only history: %v", err)
	}
	if _, total, _ := e.svc.List(ctx, ListFilter{State: FilterAll}); total != 0 {
		t.Errorf("history rows survived their client: %d", total)
	}
	if err := e.svc.DeleteClient(ctx, c.ID); !errors.Is(err, ErrClientNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
}

func TestTestClient(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.addClient("qb", KindQBittorrent, "http://qb:8080", func(in *ClientInput) { in.Password = str(longPassword) })
	fake := e.fake.at("http://qb:8080")

	res, err := e.svc.TestClient(ctx, c.ID)
	if err != nil || !res.OK || res.Version != "fake 1.0" || res.Error != "" {
		t.Fatalf("ok result = %+v, %v", res, err)
	}
	clients, _ := e.svc.Clients(ctx)
	if clients[0].LastOKAt == nil || clients[0].LastError != "" {
		t.Errorf("success not recorded: %+v", clients[0])
	}

	fake.setListErr(ErrAuth) // the fake's Test returns this
	res, err = e.svc.TestClient(ctx, c.ID)
	if err != nil || res.OK || !strings.Contains(res.Error, "sign-in") {
		t.Fatalf("auth failure = %+v, %v", res, err)
	}
	fake.setListErr(unreachable(errors.New("dial tcp: connection refused")))
	res, _ = e.svc.TestClient(ctx, c.ID)
	if res.OK || !strings.HasPrefix(res.Error, "Marquee could not reach the client.") || strings.Contains(res.Error, longPassword) {
		t.Errorf("unreachable = %+v", res)
	}
	clients, _ = e.svc.Clients(ctx)
	if clients[0].LastError != res.Error || clients[0].LastErrorAt == nil {
		t.Errorf("failure not recorded: %+v", clients[0])
	}
	if _, err := e.svc.TestClient(ctx, 999); !errors.Is(err, ErrClientNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestSecretsThatCannotBeOpenedSayWhatToDo(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.addClient("qb", KindQBittorrent, "http://qb:8080", func(in *ClientInput) { in.Password = str(longPassword) })

	// The master key file was replaced: the stored password cannot be read.
	otherBox, err := secrets.LoadOrCreate(filepath.Join(t.TempDir(), "other.key"), testLogger())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(e.db, Options{Logger: testLogger(), HTTP: e.hc, Box: otherBox, Adapters: e.fake.factory()})

	res, err := svc.TestClient(ctx, c.ID)
	if err != nil || res.OK || !strings.Contains(res.Error, "master key") {
		t.Errorf("test = %+v, %v", res, err)
	}
	list, _ := svc.Clients(ctx)
	if list[0].Password != settings.MaskPrefix {
		t.Errorf("masked = %q, want plain dots (never the sealed text)", list[0].Password)
	}
	// Grabbing through it fails cleanly rather than panicking.
	title := e.addTitle("Metropolis", 1927)
	if _, err := svc.Grab(ctx, e.target(title), torrentRelease(bbbHex)); !errors.Is(err, ErrClientUnreachable) {
		t.Errorf("grab = %v", err)
	}
	// Polling records the problem and carries on.
	if err := svc.Poll(ctx); err != nil {
		t.Errorf("poll = %v", err)
	}
}
