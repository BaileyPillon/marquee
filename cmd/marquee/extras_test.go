package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BaileyPillon/marquee/internal/accounts"
	"github.com/BaileyPillon/marquee/internal/alerts"
	"github.com/BaileyPillon/marquee/internal/core/db"
	"github.com/BaileyPillon/marquee/internal/core/events"
	"github.com/BaileyPillon/marquee/internal/core/jobs"
	"github.com/BaileyPillon/marquee/internal/core/outbound"
	"github.com/BaileyPillon/marquee/internal/core/secrets"
	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/metadata"
	"github.com/BaileyPillon/marquee/internal/requests"
	"github.com/BaileyPillon/marquee/internal/settings"
	"github.com/BaileyPillon/marquee/internal/titles"
	"github.com/BaileyPillon/marquee/internal/watchlists"
)

// ---- a small Plex ------------------------------------------------------------------

type plexItem struct {
	Key, Title, Type string
	Year             int
	Guids            []string
}

// miniPlex answers the Plex calls the watchlists make: the link, the owner's
// watchlist, the friends and their watchlists, and the ids of an item.
type miniPlex struct {
	srv     *httptest.Server
	mu      sync.Mutex
	owner   []plexItem
	friends map[string][]plexItem // friend id -> items
	names   map[string]string     // friend id -> username
}

func newMiniPlex(t *testing.T) *miniPlex {
	p := &miniPlex{friends: map[string][]plexItem{}, names: map[string]string{}}
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *miniPlex) urls() watchlists.PlexURLs {
	return watchlists.PlexURLs{Plex: p.srv.URL + "/plextv", Discover: p.srv.URL + "/discover", Community: p.srv.URL + "/community"}
}

func plexMeta(it plexItem, guids bool) map[string]any {
	m := map[string]any{"ratingKey": it.Key, "type": it.Type, "title": it.Title, "year": it.Year}
	if guids {
		var g []map[string]string
		for _, id := range it.Guids {
			g = append(g, map[string]string{"id": id})
		}
		m["Guid"] = g
	}
	return m
}

func plexJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (p *miniPlex) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/plextv/api/v2/pins":
		plexJSON(w, map[string]any{"id": 4242, "code": "ABCD"})
	case strings.HasPrefix(r.URL.Path, "/plextv/api/v2/pins/"):
		plexJSON(w, map[string]any{"id": 4242, "code": "ABCD", "authToken": "tok-owner-secret"})
	case r.URL.Path == "/plextv/api/v2/user":
		plexJSON(w, map[string]any{"id": 1001, "username": "bailey-plex"})
	case r.URL.Path == "/discover/library/sections/watchlist/all":
		var out []map[string]any
		for _, it := range p.owner {
			out = append(out, plexMeta(it, true))
		}
		plexJSON(w, map[string]any{"MediaContainer": map[string]any{"totalSize": len(out), "size": len(out), "Metadata": out}})
	case strings.HasPrefix(r.URL.Path, "/discover/library/metadata/"):
		key := strings.TrimPrefix(r.URL.Path, "/discover/library/metadata/")
		for _, items := range p.friends {
			for _, it := range items {
				if it.Key == key {
					plexJSON(w, map[string]any{"MediaContainer": map[string]any{"Metadata": []any{plexMeta(it, true)}}})
					return
				}
			}
		}
		http.NotFound(w, r)
	case r.URL.Path == "/community":
		var in struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if strings.Contains(in.Query, "allFriendsV2") {
			var fr []map[string]any
			for id, name := range p.names {
				fr = append(fr, map[string]any{"user": map[string]any{"id": id, "username": name, "displayName": name}})
			}
			plexJSON(w, map[string]any{"data": map[string]any{"allFriendsV2": fr}})
			return
		}
		id, _ := in.Variables["user"].(string)
		var nodes []map[string]any
		for _, it := range p.friends[id] {
			nodes = append(nodes, map[string]any{"id": it.Key, "title": it.Title, "type": strings.ToUpper(it.Type), "publicationYear": it.Year})
		}
		plexJSON(w, map[string]any{"data": map[string]any{"user": map[string]any{"watchlist": map[string]any{
			"nodes": nodes, "pageInfo": map[string]any{"hasNextPage": false},
		}}}})
	default:
		http.NotFound(w, r)
	}
}

// ---- fakes for the catalog ---------------------------------------------------------

type movieSource struct{}

func (movieSource) SearchMovies(context.Context, string, int) ([]metadata.SearchHit, error) {
	return nil, nil
}

func (movieSource) Movie(_ context.Context, id string) (*metadata.MovieInfo, error) {
	names := map[string]struct {
		title string
		year  int
	}{"19": {"Metropolis", 1927}, "45745": {"Sintel", 2010}, "603": {"The Matrix", 1999}}
	m, ok := names[id]
	if !ok {
		return nil, metadata.ErrNotFound
	}
	n := int64(len(id)) // any number will do; the id is what matters
	return &metadata.MovieInfo{TMDBID: map[string]int64{"19": 19, "45745": 45745, "603": 603}[id], Title: m.title, Year: m.year,
		ReleaseDate: "2010-09-30", IMDbID: "tt000" + id, RuntimeMin: int(n)}, nil
}

type showSource struct{}

func (showSource) SearchShows(context.Context, string) ([]metadata.SearchHit, error) { return nil, nil }

func (showSource) ShowByIMDb(_ context.Context, imdb string) (*metadata.SearchHit, error) {
	if imdb == "tt1234567" {
		return &metadata.SearchHit{Kind: "show", Provider: "tvmaze", ProviderID: "99001", Name: "Pioneer One"}, nil
	}
	return nil, metadata.ErrNotFound
}

func (showSource) Show(_ context.Context, id string) (*metadata.ShowInfo, error) {
	if id != "99001" {
		return nil, metadata.ErrNotFound
	}
	return &metadata.ShowInfo{
		TVmazeID: 99001, Name: "Pioneer One", Year: 2010, Status: "ended",
		Seasons:  []metadata.SeasonInfo{{Number: 1}},
		Episodes: []metadata.EpisodeInfo{{Season: 1, Number: 1, Name: "Earthfall", AirDate: "2010-06-19"}},
	}, nil
}

// artistSource can answer with the artist alone (quickly) or with everything.
type artistSource struct{}

func (artistSource) SearchArtists(context.Context, string) ([]metadata.SearchHit, error) {
	return nil, nil
}

const testMBID = "a1b2c3d4-0000-4000-8000-000000000001"

func (artistSource) ArtistBase(_ context.Context, id string) (*metadata.ArtistInfo, error) {
	return &metadata.ArtistInfo{MBID: id, Name: "Chopin", Begin: "1810", End: "1849", Ended: true}, nil
}

func (a artistSource) Artist(ctx context.Context, id string) (*metadata.ArtistInfo, error) {
	info, _ := a.ArtistBase(ctx, id)
	info.Albums = []metadata.AlbumInfo{
		{MBID: "b0000000-0000-4000-8000-000000000101", Title: "Nocturnes", Year: 1998, Type: "album"},
		{MBID: "b0000000-0000-4000-8000-000000000102", Title: "Live", Year: 2005, Type: "album", SecondaryTypes: []string{"Live"}},
	}
	return info, nil
}

// ---- the rig -------------------------------------------------------------------------

type extrasRig struct {
	t      *testing.T
	db     *sql.DB
	hub    *events.Hub
	acc    *accounts.Service
	cfg    *settings.Service
	cat    *titles.Service
	reqs   *requests.Service
	x      *extras
	plex   *miniPlex
	owner  accounts.User
	priya  accounts.User
	hook   *httptest.Server
	hookMu sync.Mutex
	hooked []map[string]any
}

func newExtrasRig(t *testing.T) *extrasRig {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := db.Open(filepath.Join(dir, db.MainFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(ctx, d, db.SetMain); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.LoadOrCreate(filepath.Join(dir, "marquee.key"), log)
	if err != nil {
		t.Fatal(err)
	}
	hub := events.NewHub(log)
	t.Cleanup(hub.Close)
	rig := &extrasRig{t: t, db: d, hub: hub, plex: newMiniPlex(t)}
	rig.acc = accounts.NewService(d, accounts.Options{Logger: log, PasswordParams: &accounts.PasswordParams{Time: 1, Memory: 64, Threads: 1, KeyLen: 32, SaltLen: 16}})
	rig.cfg = settings.New(d, box)
	rig.cat = titles.NewService(d, titles.Providers{Movies: movieSource{}, Shows: showSource{}, Artists: artistSource{}}, titles.Options{Logger: log})
	lib := library.NewService(d, library.Options{Logger: log, Hub: hub})
	t.Cleanup(lib.Close)
	for kind, name := range map[string]string{"movie": "Movies", "show": "Shows"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := lib.AddFolder(ctx, kind, p, name); err != nil {
			t.Fatal(err)
		}
	}
	rig.reqs = newRequests(d, hub, rig.cat, lib, rig.cfg, log)
	t.Cleanup(rig.reqs.Close)
	hc := outbound.New("test", outbound.Options{Logger: log, MaxRetries: -1, HostRPS: 100000, HostBurst: 100000})
	dl := newDownloads(d, hub, hc, box, rig.cfg, log)

	rig.x = newExtras(extrasDeps{
		DB: d, Hub: hub, HTTP: hc, Box: box, Settings: rig.cfg, Accounts: rig.acc,
		Shows: showSource{}, HasTMDBKey: func(context.Context) bool { return true },
		Catalog: rig.cat, Requests: rig.reqs, Downloads: dl, Log: log, PlexURLs: rig.plex.urls(),
		AlertOpts: alerts.Options{},
	})
	t.Cleanup(rig.x.Close)

	login, err := rig.acc.SetupOwner(ctx, "Bailey", "correct horse", accounts.ClientInfo{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	rig.owner = login.User
	if rig.priya, err = rig.acc.CreateMember(ctx, "Priya"); err != nil {
		t.Fatal(err)
	}

	rig.hook = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		rig.hookMu.Lock()
		rig.hooked = append(rig.hooked, m)
		rig.hookMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(rig.hook.Close)
	return rig
}

func (r *extrasRig) count(q string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.db.QueryRow(q, args...).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *extrasRig) messages() []string {
	r.hookMu.Lock()
	defer r.hookMu.Unlock()
	var out []string
	for _, m := range r.hooked {
		out = append(out, m["event"].(string)+" | "+m["message"].(string))
	}
	return out
}

// ---- the tests -----------------------------------------------------------------------

func TestWatchlistItemsBecomeRealAsksThroughTheRequestsService(t *testing.T) {
	ctx := context.Background()
	r := newExtrasRig(t)

	// Link the owner's Plex account through the PIN flow.
	l, err := r.x.Watchlists.StartLink(ctx)
	if err != nil || l.PinID != 4242 {
		t.Fatalf("StartLink = %+v, %v", l, err)
	}
	if ok, err := r.x.Watchlists.CheckLink(ctx, l.PinID, r.owner.ID); err != nil || !ok {
		t.Fatalf("CheckLink = %v, %v", ok, err)
	}

	// The owner has Metropolis on their watchlist. Priya (a friend) has Metropolis
	// too, and Pioneer One (found through its IMDb id), and Sintel.
	r.plex.owner = []plexItem{{Key: "o1", Title: "Metropolis", Type: "movie", Year: 1927, Guids: []string{"tmdb://19", "imdb://tt0017136"}}}
	r.plex.names["fr-priya"] = "priya_p"
	r.plex.friends["fr-priya"] = []plexItem{
		{Key: "k1", Title: "Metropolis", Type: "movie", Year: 1927, Guids: []string{"tmdb://19"}},
		{Key: "k2", Title: "Pioneer One", Type: "show", Year: 2010, Guids: []string{"imdb://tt1234567", "tvdb://81189"}},
		{Key: "k3", Title: "Sintel", Type: "movie", Year: 2010, Guids: []string{"tmdb://45745"}},
	}
	// Priya's weekly HD limit is one; asks for HD are approved at once.
	if err := requests.SaveConfig(ctx, r.cfg, requests.Config{HDPerWeek: 1, UHDPerWeek: 0, AutoApproveHD: true}); err != nil {
		t.Fatal(err)
	}

	// A webhook channel to see what the alerts say.
	if _, err := r.x.Alerts.Create(ctx, alerts.Input{
		Name: ptr("Hook"), Kind: ptr("webhook"), Config: map[string]any{"url": r.hook.URL + "/h"},
		Events: &[]string{alerts.EventRequestApproved, alerts.EventRequestCreated},
	}); err != nil {
		t.Fatal(err)
	}

	// First check: Priya is a friend but not linked yet, so only the owner's item is asked.
	res, err := r.x.Watchlists.Sync(ctx)
	if err != nil || res.Asked != 1 || res.Skipped[watchlists.SkipUnlinkedFriend] != 3 {
		t.Fatalf("first check = %+v, %v", res, err)
	}
	if err := r.x.Watchlists.SetFriendLink(ctx, "fr-priya", &r.priya.ID); err != nil {
		t.Fatal(err)
	}

	// Second check: Priya's Metropolis is already asked for (by the owner), Pioneer One
	// uses her one place this week, and Sintel waits for the limit.
	res, err = r.x.Watchlists.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Asked != 1 || res.Skipped[watchlists.SkipAlreadyAsked] != 1 || res.Skipped[watchlists.SkipLimitReached] != 1 || len(res.Skipped) != 2 {
		t.Fatalf("second check = %+v", res)
	}

	type ask struct {
		user          int64
		source, ref   string
		state, title  string
		quality, kind string
	}
	rows, err := r.db.Query(`SELECT r.user_id, r.source, r.source_ref, r.state, t.name, r.quality, r.kind
		FROM requests r JOIN titles t ON t.id = r.title_id ORDER BY r.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []ask
	for rows.Next() {
		var a ask
		if err := rows.Scan(&a.user, &a.source, &a.ref, &a.state, &a.title, &a.quality, &a.kind); err != nil {
			t.Fatal(err)
		}
		got = append(got, a)
	}
	want := []ask{
		{r.owner.ID, "watchlist", "plex:1001:o1", "approved", "Metropolis", "hd", "movie"},
		{r.priya.ID, "watchlist", "plex:fr-priya:k2", "approved", "Pioneer One", "hd", "show"},
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("asks =\n%+v\nwant\n%+v", got, want)
	}

	// The items point at their asks, and the limit-blocked one has none yet.
	if n := r.count(`SELECT COUNT(*) FROM watchlist_items WHERE request_id IS NOT NULL`); n != 2 {
		t.Errorf("%d items with an ask, want 2", n)
	}
	var reason string
	if err := r.db.QueryRow(`SELECT skipped_reason FROM watchlist_items WHERE item_key = 'k3'`).Scan(&reason); err != nil || reason != watchlists.SkipLimitReached {
		t.Errorf("Sintel: %q, %v", reason, err)
	}

	// A third check changes nothing: the limit still holds, nothing is asked twice.
	res, err = r.x.Watchlists.Sync(ctx)
	if err != nil || res.Asked != 0 || res.Skipped[watchlists.SkipLimitReached] != 1 || len(res.Skipped) != 1 {
		t.Fatalf("third check = %+v, %v", res, err)
	}
	if n := r.count(`SELECT COUNT(*) FROM requests`); n != 2 {
		t.Errorf("%d asks after the third check, want 2", n)
	}

	// The owner gives Priya a bigger limit; the next check asks for Sintel, as her.
	five := 5
	if _, err := r.reqs.SetLimits(ctx, requests.Actor{UserID: r.owner.ID, Role: "owner"}, r.priya.ID,
		requests.LimitsPatch{HDPerWeek: requests.Patch[int]{Set: true, Value: &five}}); err != nil {
		t.Fatal(err)
	}
	res, err = r.x.Watchlists.Sync(ctx)
	if err != nil || res.Asked != 1 || len(res.Skipped) != 0 {
		t.Fatalf("after the limit was raised = %+v, %v", res, err)
	}
	if n := r.count(`SELECT COUNT(*) FROM requests WHERE user_id = ? AND source = 'watchlist'`, r.priya.ID); n != 2 {
		t.Errorf("Priya has %d watchlist asks, want 2", n)
	}

	// The alerts heard about the approvals, in plain words, with the names filled in.
	deadline := time.Now().Add(5 * time.Second)
	for len(r.messages()) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	msgs := strings.Join(r.messages(), "\n")
	for _, want := range []string{
		"request.approved | Bailey added Metropolis (1927) in HD",
		"request.approved | Priya's ask for Pioneer One (2010) in HD was approved automatically",
		"request.approved | Priya's ask for Sintel (2010) in HD was approved automatically",
	} {
		if !strings.Contains(msgs, want) {
			t.Errorf("no message %q in\n%s", want, msgs)
		}
	}
}

func TestAnAskThatWaitsForTheOwnerIsAnAlert(t *testing.T) {
	ctx := context.Background()
	r := newExtrasRig(t)
	if _, err := r.x.Alerts.Create(ctx, alerts.Input{Name: ptr("Hook"), Kind: ptr("webhook"), Config: map[string]any{"url": r.hook.URL + "/h"}}); err != nil {
		t.Fatal(err)
	}
	// 4K asks wait for the owner: a member asks for Metropolis in 4K.
	if err := requests.SaveConfig(ctx, r.cfg, requests.Config{HDPerWeek: 5, UHDPerWeek: 5, AutoApproveHD: true, AutoApproveUHD: false}); err != nil {
		t.Fatal(err)
	}
	asker := watchlistAsker{r.reqs}
	res, err := asker.Ask(ctx, watchlists.AskInput{UserID: r.priya.ID, Role: "member", Kind: "movie", Provider: "tmdb", ProviderID: "19", Quality: "uhd", SourceRef: "plex:x:1"})
	if err != nil || res.AutoApproved {
		t.Fatalf("ask = %+v, %v", res, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(r.messages()) < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := r.messages(); len(got) != 1 || got[0] != "request.created | Priya asked for Metropolis (1927) in 4K — waiting for your yes" {
		t.Errorf("messages = %v", got)
	}
}

func TestTheAskAdapterTranslatesWhatRequestsSays(t *testing.T) {
	ctx := context.Background()
	r := newExtrasRig(t)
	asker := watchlistAsker{r.reqs}
	ask := func(user int64, role, kind, provider, id, quality string) error {
		_, err := asker.Ask(ctx, watchlists.AskInput{UserID: user, Role: role, Kind: kind, Provider: provider, ProviderID: id, Quality: quality})
		return err
	}
	if err := ask(r.owner.ID, "owner", "movie", "tmdb", "19", "hd"); err != nil {
		t.Fatal(err)
	}
	if err := ask(r.priya.ID, "member", "movie", "tmdb", "19", "hd"); !isErr(err, watchlists.ErrAlreadyAsked) {
		t.Errorf("a duplicate = %v, want ErrAlreadyAsked", err)
	}
	if err := requests.SaveConfig(ctx, r.cfg, requests.Config{HDPerWeek: 0, UHDPerWeek: 0, AutoApproveHD: true}); err != nil {
		t.Fatal(err)
	}
	if err := ask(r.priya.ID, "member", "movie", "tmdb", "45745", "hd"); !isErr(err, watchlists.ErrLimitReached) {
		t.Errorf("a limit of 0 = %v, want ErrLimitReached", err)
	}
	// Without a movie folder, an owner's ask cannot be approved.
	if _, err := r.db.Exec(`DELETE FROM library_folders WHERE kind = 'movie'`); err != nil {
		t.Fatal(err)
	}
	if err := ask(r.owner.ID, "owner", "movie", "tmdb", "603", "hd"); !isErr(err, watchlists.ErrNoFolder) {
		t.Errorf("no folder = %v, want ErrNoFolder", err)
	}
	// Anything else comes through untouched.
	if err := ask(r.owner.ID, "owner", "movie", "tmdb", "404", "hd"); err == nil || isErr(err, watchlists.ErrNoFolder) || isErr(err, watchlists.ErrLimitReached) {
		t.Errorf("a title that is not there = %v", err)
	}
}

func isErr(err, target error) bool {
	return err != nil && strings.Contains(err.Error(), target.Error()) || err == target
}

func TestMemberMakerPutsANameTakenInTheModulesOwnWords(t *testing.T) {
	r := newExtrasRig(t)
	m := memberMaker{r.acc}
	id, err := m.CreateMember(context.Background(), "Omar")
	if err != nil || id == 0 {
		t.Fatalf("CreateMember = %d, %v", id, err)
	}
	if _, err := m.CreateMember(context.Background(), "omar"); err != watchlists.ErrNameTaken {
		t.Errorf("a name that is taken = %v, want watchlists.ErrNameTaken", err)
	}
}

func TestHealthJobReportsADownloadClientThatDoesNotAnswerOnce(t *testing.T) {
	ctx := context.Background()
	r := newExtrasRig(t)
	if _, err := r.x.Alerts.Create(ctx, alerts.Input{Name: ptr("Hook"), Kind: ptr("webhook"), Config: map[string]any{"url": r.hook.URL + "/h"}, Events: &[]string{alerts.EventHealthProblem}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO clients (name, kind, url, enabled, last_error, created_at) VALUES ('qbit', 'qbittorrent', 'http://x', 1, 'connection refused', '2026-01-01T00:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO clients (name, kind, url, enabled, last_error, created_at) VALUES ('off', 'deluge', 'http://y', 0, 'down', '2026-01-01T00:00:00.000Z')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ { // the job runs every few minutes; the owner hears of it once
		if err := r.x.checkHealth(ctx); err != nil {
			t.Fatal(err)
		}
	}
	r.x.Alerts.Wait()
	if got := r.messages(); len(got) != 1 || got[0] != "health.problem | The download client qbit is not answering: connection refused" {
		t.Errorf("messages = %v", got)
	}
}

func TestExtrasRegisterTheirJobs(t *testing.T) {
	r := newExtrasRig(t)
	sched := jobs.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := r.x.registerJobs(sched); err != nil {
		t.Fatal(err)
	}
	names := map[string]time.Duration{}
	for _, s := range sched.Status() {
		names[s.Name] = s.Interval
	}
	if names["watchlists.sync"] != 15*time.Minute || names["alerts.health"] != 5*time.Minute {
		t.Errorf("jobs = %v", names)
	}
	// Running the Plex job with nothing linked does nothing and is not an error.
	if err := sched.RunNow("watchlists.sync"); err != nil {
		t.Errorf("watchlists.sync with no account linked = %v", err)
	}
}

func TestTheCatalogReportsAnArtistsAlbumsOnTheHub(t *testing.T) {
	ctx := context.Background()
	r := newExtrasRig(t)
	c, unsub := r.hub.Subscribe("titles")
	defer unsub()
	d, created, hydrating, err := r.cat.AddAsync(ctx, "artist", "musicbrainz", testMBID, titles.AddOptions{Monitored: true})
	if err != nil || !created || !hydrating || len(d.Albums) != 0 {
		t.Fatalf("AddAsync = %+v %v %v %v", d, created, hydrating, err)
	}
	select {
	case m := <-c.C():
		var ev struct {
			ID    int64  `json:"id"`
			Event string `json:"event"`
		}
		if err := json.Unmarshal(m.Data, &ev); err != nil || ev.ID != d.ID || ev.Event != "hydrated" {
			t.Errorf("event = %s (%v)", m.Data, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the catalog's hub was not wired up: no titles event")
	}
	got, _ := r.cat.Get(ctx, d.ID)
	if len(got.Albums) != 2 {
		t.Fatalf("albums = %d", len(got.Albums))
	}
	for _, a := range got.Albums {
		if a.Monitored != (a.Name == "Nocturnes") {
			t.Errorf("album %q monitored = %v", a.Name, a.Monitored)
		}
	}
	if got.LifeSpan == nil || got.LifeSpan.Begin != "1810" || got.LifeSpan.End != "1849" {
		t.Errorf("life span = %+v", got.LifeSpan)
	}
}

// TestServeWiresUpWatchlistsCalendarAndAlerts starts the whole program and
// checks that the three new groups of routes answer for the owner.
func TestServeWiresUpWatchlistsCalendarAndAlerts(t *testing.T) {
	t.Setenv("MARQUEE_MASTER_KEY_FILE", "")
	cfg := filepath.Join(t.TempDir(), "config")
	logs := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, newLogger(logs, "info"), "127.0.0.1:0", cfg) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("serve did not stop")
		}
	}()
	started := regexp.MustCompile(`msg="marquee started" version=\S+ addr=(127\.0\.0\.1:\d+)`)
	var addr string
	for deadline := time.Now().Add(10 * time.Second); addr == "" && time.Now().Before(deadline); {
		if m := started.FindStringSubmatch(logs.String()); m != nil {
			addr = m[1]
			break
		}
		select {
		case err := <-done:
			t.Fatalf("serve returned early: %v\nlogs:\n%s", err, logs.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if addr == "" {
		t.Fatalf("no startup line:\n%s", logs.String())
	}

	jar, _ := cookiejar.New(nil)
	hc := &http.Client{Jar: jar}
	call := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var rd io.Reader = bytes.NewReader(nil)
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, err := http.NewRequest(method, "http://"+addr+path, rd)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Marquee", "1")
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	for _, p := range []string{"/api/v1/watchlists", "/api/v1/calendar", "/api/v1/alerts"} {
		if status, _ := call("GET", p, nil); status != http.StatusUnauthorized {
			t.Errorf("GET %s signed out = %d, want 401 (the route exists)", p, status)
		}
	}
	if status, _ := call("POST", "/api/v1/setup/owner", map[string]string{"name": "Bailey", "password": "correct horse"}); status != http.StatusCreated {
		t.Fatalf("setup = %d", status)
	}
	status, wl := call("GET", "/api/v1/watchlists", nil)
	if plex, _ := wl["plex"].(map[string]any); status != http.StatusOK || plex["linked"] != false {
		t.Errorf("watchlists = %d %v", status, wl)
	}
	if status, cal := call("GET", "/api/v1/calendar", nil); status != http.StatusOK || cal["items"] == nil {
		t.Errorf("calendar = %d %v", status, cal)
	}
	if status, al := call("GET", "/api/v1/alerts", nil); status != http.StatusOK || al["alerts"] == nil {
		t.Errorf("alerts = %d %v", status, al)
	}
	if status, _ := call("POST", "/api/v1/watchlists/check", nil); status != http.StatusConflict {
		t.Errorf("check with nothing linked = %d, want 409", status)
	}
}

func ptr[T any](v T) *T { return &v }
