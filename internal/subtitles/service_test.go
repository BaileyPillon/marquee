package subtitles

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// goodOS is a subtitle that fits the film in testenv: same source, resolution
// and codec, a release group the video's renamed file does not name.
func goodOS(fileID int64, lang string) osItem {
	return osItem{FileID: fileID, Release: "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP", Language: lang, Downloads: 100, Year: 1968}
}

// offers makes the fake OpenSubtitles answer every search with these items,
// whatever language was asked for.
func (f *fakeOS) offers(items ...osItem) {
	f.mu.Lock()
	f.items = func(url.Values) []osItem { return items }
	f.mu.Unlock()
}

// ---- manual: search, then download -------------------------------------------------

func TestManualSearchAndDownload(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(true, nil)
	id, video := r.nol()
	wantHash, _, err := HashFile(video)
	if err != nil {
		t.Fatal(err)
	}
	r.os.offers(
		osItem{FileID: 21, Release: "Totally.Unrelated.Name", Language: "en", Downloads: 3, HashMatch: true, Year: 1968},
		goodOS(22, "en"),
		osItem{FileID: 23, Release: "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP", Language: "en", HI: true, Downloads: 50, Year: 1968},
		osItem{FileID: 24, Release: "Dawn.of.the.Dead.1978.1080p.BluRay-GRP", Language: "en", Downloads: 99999, Year: 1978},
		goodOS(25, "fr"), // another language: dropped
	)
	r.os.linkBody[21] = []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nCaf\xe9 \x93hi\x94\r\n\r\n") // Windows-1252

	res, err := r.svc.Search(bg(), id, "en")
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "en" || res.MinScore != 60 || len(res.Sources) != 1 || !res.Sources[0].OK {
		t.Errorf("result = %+v", res)
	}
	if q := r.os.lastSearch(); q.Get("moviehash") != wantHash || q.Get("imdb_id") != "63350" || q.Get("languages") != "en" {
		t.Errorf("the search was %v, want the file's hash %s", q, wantHash)
	}
	var order []string
	for _, c := range res.Results {
		order = append(order, c.ProviderID)
	}
	// The exact file (100), then the group match, then the HI one nudged down
	// by "prefer not", then the other film last (rejected, 0).
	if strings.Join(order, ",") != "21,22,23,24" {
		t.Fatalf("order = %v, want 21,22,23,24", order)
	}
	if res.Results[0].Score != 100 || res.Results[1].Score <= res.Results[2].Score || res.Results[3].Score != 0 || len(res.Results[3].Reasons) != 1 {
		t.Errorf("scores = %d %d %d %d", res.Results[0].Score, res.Results[1].Score, res.Results[2].Score, res.Results[3].Score)
	}

	sub, err := r.svc.Download(bg(), id, "opensubtitles", "21", "en")
	if err != nil {
		t.Fatal(err)
	}
	path := SubtitlePath(video, "en", false)
	if got := r.read(path); got != "1\r\n00:00:01,000 --> 00:00:02,000\r\nCafé “hi”\r\n\r\n" {
		t.Errorf("the file is %q, want the text as UTF-8", got)
	}
	if sub.Language != "en" || sub.HI || sub.Provider != "opensubtitles" || sub.ProviderID != "21" || sub.Score != 100 || sub.FileID != id {
		t.Errorf("row = %+v", sub)
	}
	if len(r.notified) != 1 || r.notified[0] != path {
		t.Errorf("the media servers were told %v, want %s", r.notified, path)
	}
	if got := r.results(id, "en"); len(got) != 1 || got[0] != ResultFound {
		t.Errorf("searches = %v", got)
	}

	// The hearing-impaired one goes next to it under its own name.
	hi, err := r.svc.Download(bg(), id, "opensubtitles", "23", "en")
	if err != nil || !hi.HI {
		t.Fatalf("HI download: %+v, %v", hi, err)
	}
	if !exists(SubtitlePath(video, "en", true)) || !strings.HasSuffix(SubtitlePath(video, "en", true), ".en.hi.srt") {
		t.Error("the hearing-impaired subtitle is not at .en.hi.srt")
	}
	if rows := r.subtitleRows(id); len(rows) != 2 {
		t.Errorf("rows = %+v", rows)
	}
	r.os.noBadHeaders()
}

func TestManualDownloadReplacesOnlyOurOwnFile(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, video := r.nol()
	r.os.offers(goodOS(31, "en"), goodOS(32, "en"))
	r.os.files[31], r.os.files[32] = srtOne, strings.Replace(srtOne, "Hello there", "Second try", 1)
	if _, err := r.svc.Search(bg(), id, "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Download(bg(), id, "opensubtitles", "31", "en"); err != nil {
		t.Fatal(err)
	}
	// The owner picks another one: it replaces the first, there is still one row.
	if _, err := r.svc.Download(bg(), id, "opensubtitles", "32", "en"); err != nil {
		t.Fatal(err)
	}
	path := SubtitlePath(video, "en", false)
	if !strings.Contains(r.read(path), "Second try") {
		t.Errorf("the file was not replaced: %q", r.read(path))
	}
	rows := r.subtitleRows(id)
	if len(rows) != 1 || rows[0].ProviderID != "32" {
		t.Errorf("rows = %+v", rows)
	}
	if entries, _ := os.ReadDir(filepath.Dir(video)); len(entries) != 2 {
		t.Errorf("the folder holds %d files, want the video and one subtitle", len(entries))
	}
}

func TestManualDownloadNeverTouchesAForeignSubtitle(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, video := r.nol()
	path := SubtitlePath(video, "en", false)
	if err := os.WriteFile(path, []byte("my own subtitle, made by hand"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.os.offers(goodOS(41, "en"))
	if _, err := r.svc.Search(bg(), id, "en"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Download(bg(), id, "opensubtitles", "41", "en"); !errors.Is(err, ErrForeignFile) {
		t.Fatalf("err = %v, want ErrForeignFile", err)
	}
	if got := r.read(path); got != "my own subtitle, made by hand" {
		t.Errorf("the foreign subtitle was changed: %q", got)
	}
	if rows := r.subtitleRows(id); len(rows) != 0 {
		t.Errorf("a row was written for a file Marquee did not write: %+v", rows)
	}
	if len(r.notified) != 0 {
		t.Errorf("the media servers were told about a file Marquee did not write: %v", r.notified)
	}
}

func TestRemoveOnlyRemovesWhatMarqueeWrote(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, video := r.nol()
	other := SubtitlePath(video, "fr", false)
	if err := os.WriteFile(other, []byte("a french one I made"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.os.offers(goodOS(51, "en"))
	if _, err := r.svc.Search(bg(), id, "en"); err != nil {
		t.Fatal(err)
	}
	sub, err := r.svc.Download(bg(), id, "opensubtitles", "51", "en")
	if err != nil {
		t.Fatal(err)
	}
	path := SubtitlePath(video, "en", false)
	if err := r.svc.Remove(bg(), sub.ID); err != nil {
		t.Fatal(err)
	}
	if exists(path) || len(r.subtitleRows(id)) != 0 {
		t.Error("the subtitle Marquee wrote is still there")
	}
	if !exists(other) || !exists(video) {
		t.Error("Remove touched a file that is not Marquee's")
	}
	if err := r.svc.Remove(bg(), sub.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a second Remove: %v", err)
	}
	if err := r.svc.Remove(bg(), 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown id: %v", err)
	}

	// A subtitle whose file the owner already deleted: the row goes, no error.
	if _, err := r.svc.Download(bg(), id, "opensubtitles", "51", "en"); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
	rows := r.subtitleRows(id)
	if len(rows) != 1 {
		t.Fatal("no row")
	}
	if err := r.svc.Remove(bg(), rows[0].ID); err != nil || len(r.subtitleRows(id)) != 0 {
		t.Errorf("Remove of a file already gone: %v", err)
	}
}

func TestManualErrors(t *testing.T) {
	r := newRig(t)
	id, video := r.nol()
	var inv *InvalidError

	// Nothing is turned on.
	r.configure(map[string]any{"providers": map[string]any{"podnapisi": map[string]any{"enabled": false}}})
	if _, err := r.svc.Search(bg(), id, "en"); !errors.Is(err, ErrNoProvider) {
		t.Errorf("no provider: %v", err)
	}
	if _, err := r.svc.Download(bg(), id, "opensubtitles", "1", "en"); !errors.Is(err, ErrNoProvider) {
		t.Errorf("no provider (download): %v", err)
	}

	r.useOpenSubtitles(false, nil)
	if _, err := r.svc.Search(bg(), 99999, "en"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown file: %v", err)
	}
	if _, err := r.svc.Search(bg(), id, "not a language"); !errors.As(err, &inv) {
		t.Errorf("bad language: %v", err)
	}
	if _, err := r.svc.Download(bg(), id, "", "1", "en"); !errors.As(err, &inv) {
		t.Errorf("no provider named: %v", err)
	}
	if _, err := r.svc.Download(bg(), id, "podnapisi", "1", "en"); !errors.As(err, &inv) {
		t.Errorf("a provider that is off: %v", err)
	}

	// A music file is not a video.
	music, err := r.lib.AddFolder(bg(), "music", mkdir(t, filepath.Join(r.root, "music")), "Music")
	if err != nil {
		t.Fatal(err)
	}
	mid, _ := r.videoFile(music, "Artist/Album/01 - Song.flac", 1000, 77, nil)
	if _, err := r.svc.Search(bg(), mid, "en"); !errors.Is(err, ErrNotVideo) {
		t.Errorf("music: %v", err)
	}
	if _, err := r.svc.ForFile(bg(), mid); !errors.Is(err, ErrNotVideo) {
		t.Errorf("music (ForFile): %v", err)
	}

	// An episode the library could not match to an episode.
	showFile, _ := r.videoFile(r.tv, "Pioneer One/Season 01/Pioneer One - whole.mkv", 1000, showID, nil)
	if _, err := r.svc.Search(bg(), showFile, "en"); !errors.As(err, &inv) {
		t.Errorf("an episode of unknown number: %v", err)
	}

	// The video is not on disk any more.
	if err := os.Remove(video); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Search(bg(), id, "en"); !errors.Is(err, ErrFileMissing) {
		t.Errorf("a missing video: %v", err)
	}
	if _, err := r.svc.Download(bg(), id, "opensubtitles", "1", "en"); !errors.Is(err, ErrFileMissing) {
		t.Errorf("a missing video (download): %v", err)
	}
}

func mkdir(t *testing.T, p string) string {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestManualDownloadOfASearchNobodyRemembers(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, video := r.nol()
	sub, err := r.svc.Download(bg(), id, "opensubtitles", "61", "en") // no search first
	if err != nil || sub.HI || sub.Score != 0 {
		t.Fatalf("Download = %+v, %v", sub, err)
	}
	if !exists(SubtitlePath(video, "en", false)) {
		t.Error("no file")
	}
}

func TestManualSearchResultsBelowTheMinimumCanStillBeTaken(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, map[string]any{"minScore": 90})
	id, video := r.nol()
	r.os.offers(goodOS(71, "en")) // scores 82
	res, err := r.svc.Search(bg(), id, "en")
	if err != nil || len(res.Results) != 1 || res.MinScore != 90 || res.Results[0].Score >= 90 {
		t.Fatalf("Search = %+v, %v", res, err)
	}
	if _, err := r.svc.Download(bg(), id, "opensubtitles", "71", "en"); err != nil {
		t.Errorf("a person's choice stands: %v", err)
	}
	if !exists(SubtitlePath(video, "en", false)) {
		t.Error("no file")
	}
}

// ---- automatic: after an import ----------------------------------------------------

func TestAfterImportFetchesTheBestSubtitle(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(true, nil)
	id, video := r.nol()
	r.os.offers(
		osItem{FileID: 81, Release: "Night.of.the.Living.Dead.1968.1080p.BluRay.x264-OTHER", Language: "en", Downloads: 5000, Year: 1968},
		goodOS(82, "en"),
		osItem{FileID: 83, Release: "Dawn.of.the.Dead.1978.1080p.BluRay-GRP", Language: "en", Downloads: 99999, Year: 1978},
	)
	r.svc.AfterImport(bg(), []int64{id})

	if got := r.os.downloaded(); len(got) != 1 {
		t.Fatalf("downloads = %v, want one", got)
	}
	rows := r.subtitleRows(id)
	if len(rows) != 1 || rows[0].Language != "en" || rows[0].Score < 60 {
		t.Fatalf("rows = %+v", rows)
	}
	if !exists(SubtitlePath(video, "en", false)) {
		t.Error("no subtitle file next to the video")
	}
	if got := r.results(id, "en"); len(got) != 1 || got[0] != ResultFound {
		t.Errorf("searches = %v", got)
	}
	// Found, so a second AfterImport has nothing to look for.
	before := r.os.searchCount()
	r.svc.AfterImport(bg(), []int64{id})
	if r.os.searchCount() != before {
		t.Error("a file that has its subtitle was searched for again")
	}
}

func TestAfterImportSearchesOnlyTheMissingLanguages(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, map[string]any{"languages": []any{"en", "fr", "es"}})
	id, video := r.nol()
	// An English subtitle is already there, written by hand.
	if err := os.WriteFile(SubtitlePath(video, "en", false), []byte(srtOne), 0o644); err != nil {
		t.Fatal(err)
	}
	r.os.offers(goodOS(91, "fr"), goodOS(92, "es"), goodOS(93, "en"))
	r.svc.AfterImport(bg(), []int64{id})
	if q := r.os.lastSearch(); q.Get("languages") != "fr,es" {
		t.Errorf("languages = %q, want only the missing ones, fr,es", q.Get("languages"))
	}
	if r.os.searchCount() != 1 {
		t.Errorf("%d searches, want one for both languages", r.os.searchCount())
	}
	for _, lang := range []string{"fr", "es"} {
		if !exists(SubtitlePath(video, lang, false)) {
			t.Errorf("no %s subtitle", lang)
		}
	}
	if got := r.read(SubtitlePath(video, "en", false)); got != srtOne {
		t.Errorf("the English file was changed: %q", got)
	}
	if len(r.subtitleRows(id)) != 2 {
		t.Errorf("rows = %+v", r.subtitleRows(id))
	}
}

func TestAfterImportDoesNothingWhenOffOrNotSetUp(t *testing.T) {
	r := newRig(t)
	id, _ := r.nol()
	r.os.offers(goodOS(101, "en"))

	// "Search after import" is off.
	r.useOpenSubtitles(false, map[string]any{"searchAfterImport": false})
	r.svc.AfterImport(bg(), []int64{id})
	// No provider is turned on.
	r.configure(map[string]any{"providers": map[string]any{"podnapisi": map[string]any{"enabled": false}}})
	r.svc.AfterImport(bg(), []int64{id})
	// OpenSubtitles is on but has no key.
	r.configure(map[string]any{"providers": map[string]any{"opensubtitles": map[string]any{"enabled": true}, "podnapisi": map[string]any{"enabled": false}}})
	r.svc.AfterImport(bg(), []int64{id})
	// No languages are wanted.
	r.useOpenSubtitles(false, map[string]any{"languages": []any{}})
	r.svc.AfterImport(bg(), []int64{id})
	if r.os.searchCount() != 0 || len(r.subtitleRows(id)) != 0 {
		t.Errorf("%d searches, %d rows; want none", r.os.searchCount(), len(r.subtitleRows(id)))
	}

	// Files that are not videos, or are gone, are skipped without a fuss.
	r.useOpenSubtitles(false, nil)
	r.svc.AfterImport(bg(), []int64{99999})
	if r.os.searchCount() != 0 {
		t.Error("an unknown file was searched for")
	}
}

func TestAfterImportIgnoresTheBackoffOfOtherFiles(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, _ := r.nol()
	r.os.offers() // nothing found
	r.svc.AfterImport(bg(), []int64{id})
	r.svc.AfterImport(bg(), []int64{id}) // a new import of the same file: looked at again at once
	if r.os.searchCount() != 2 {
		t.Errorf("%d searches, want 2: the import hook does not wait out the backoff", r.os.searchCount())
	}
	if got := r.results(id, "en"); len(got) != 2 || got[0] != ResultNone {
		t.Errorf("searches = %v", got)
	}
}

func TestAutomaticDownloadHonoursTheMinimumScore(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, map[string]any{"minScore": 95})
	id, _ := r.nol()
	r.os.offers(goodOS(111, "en")) // scores 82: under 95
	r.svc.AfterImport(bg(), []int64{id})
	if len(r.os.downloaded()) != 0 || len(r.subtitleRows(id)) != 0 {
		t.Error("a subtitle under the minimum was downloaded")
	}
	if got := r.results(id, "en"); len(got) != 1 || got[0] != ResultNone {
		t.Errorf("searches = %v", got)
	}
}

func TestHearingImpairedPreferencesInAutomaticPicks(t *testing.T) {
	plain := goodOS(121, "en")
	hi := goodOS(122, "en")
	hi.HI = true
	hi.Downloads = 1
	for _, tc := range []struct {
		pref   string
		wantID string
		wantHI bool
	}{
		{"prefer-not", "121", false},
		{"prefer", "122", true},
		{"only", "122", true},
	} {
		r := newRig(t)
		r.useOpenSubtitles(false, map[string]any{"hearingImpaired": tc.pref})
		id, video := r.nol()
		r.os.offers(plain, hi)
		r.svc.AfterImport(bg(), []int64{id})
		rows := r.subtitleRows(id)
		if len(rows) != 1 || rows[0].ProviderID != tc.wantID || rows[0].HI != tc.wantHI {
			t.Errorf("%s: rows = %+v, want provider id %s", tc.pref, rows, tc.wantID)
			continue
		}
		if !exists(SubtitlePath(video, "en", tc.wantHI)) {
			t.Errorf("%s: no file at the expected name", tc.pref)
		}
	}
	// "Only": with nothing for the hearing impaired, nothing is taken.
	r := newRig(t)
	r.useOpenSubtitles(false, map[string]any{"hearingImpaired": "only"})
	id, _ := r.nol()
	r.os.offers(plain)
	r.svc.AfterImport(bg(), []int64{id})
	if len(r.subtitleRows(id)) != 0 {
		t.Error("a plain subtitle was taken although only hearing-impaired ones are wanted")
	}
}

func TestAutomaticDownloadTriesTheNextWhenOneIsNotASubtitle(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, video := r.nol()
	best, next := goodOS(131, "en"), goodOS(132, "en")
	best.Downloads, next.Downloads = 900, 10
	r.os.offers(best, next)
	r.os.files[131] = "<html>Error 500, sorry</html>"
	r.os.files[132] = srtOne
	r.svc.AfterImport(bg(), []int64{id})
	if got := r.os.downloaded(); len(got) != 2 || got[0] != 131 || got[1] != 132 {
		t.Errorf("downloads = %v, want the best first, then the next", got)
	}
	if !strings.Contains(r.read(SubtitlePath(video, "en", false)), "Hello there") {
		t.Error("the good subtitle was not written")
	}
	if rows := r.subtitleRows(id); len(rows) != 1 || rows[0].ProviderID != "132" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestAutomaticDownloadGivesUpAfterThreeTries(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, _ := r.nol()
	var items []osItem
	for i := int64(0); i < 6; i++ {
		it := goodOS(140+i, "en")
		it.Downloads = int(100 - i)
		items = append(items, it)
		r.os.files[140+i] = "not a subtitle"
	}
	r.os.offers(items...)
	r.svc.AfterImport(bg(), []int64{id})
	if got := r.os.downloaded(); len(got) != downloadAttempts {
		t.Errorf("%d downloads, want %d", len(got), downloadAttempts)
	}
	if got := r.results(id, "en"); len(got) != 1 || got[0] != ResultNone {
		t.Errorf("searches = %v (bad files are the providers' fault, not an error to retry at once)", got)
	}
}

func TestAutomaticPassLeavesAForeignSubtitleAlone(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, video := r.nol()
	// A file named like Marquee's would name it, but not Marquee's: it covers the language.
	path := SubtitlePath(video, "en", false)
	if err := os.WriteFile(path, []byte("hand made"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.os.offers(goodOS(151, "en"))
	r.svc.AfterImport(bg(), []int64{id})
	if r.os.searchCount() != 0 {
		t.Error("a language a file already has was searched for")
	}
	if got := r.read(path); got != "hand made" {
		t.Errorf("changed: %q", got)
	}

	// Race: the file appears between the search and the write. Simulated by a
	// provider that drops one in while it downloads.
	r2 := newRig(t)
	id2, video2 := r2.nol()
	late := SubtitlePath(video2, "en", false)
	prov := &fakeProvider{name: "fake", cands: []Candidate{{Provider: "fake", ProviderID: "1", Language: "en", IDMatch: true, Year: 1968,
		Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}}},
		onDownload: func() { _ = os.WriteFile(late, []byte("appeared late"), 0o644) }}
	r2.svc.custom = func(Config) []Provider { return []Provider{prov} }
	r2.svc.AfterImport(bg(), []int64{id2})
	if got := r2.read(late); got != "appeared late" {
		t.Errorf("the file that appeared was overwritten: %q", got)
	}
	if rows := r2.subtitleRows(id2); len(rows) != 0 {
		t.Errorf("a row for a file Marquee did not write: %+v", rows)
	}
}

// fakeProvider is a Provider written in the test, for the orchestration tests.
type fakeProvider struct {
	name       string
	cands      []Candidate
	data       string
	searchErr  error
	dlErr      error
	remaining  *int
	onDownload func()
	searches   int
	downloads  int
	languages  [][]string
}

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) Search(_ context.Context, q Query) ([]Candidate, error) {
	p.searches++
	p.languages = append(p.languages, q.Languages)
	return p.cands, p.searchErr
}
func (p *fakeProvider) Download(context.Context, Candidate) (DownloadResult, error) {
	p.downloads++
	if p.onDownload != nil {
		p.onDownload()
	}
	if p.dlErr != nil {
		return DownloadResult{}, p.dlErr
	}
	data := p.data
	if data == "" {
		data = srtOne
	}
	return DownloadResult{Data: []byte(data), Remaining: p.remaining}, nil
}
func (p *fakeProvider) Test(context.Context) error { return p.searchErr }

// ---- the scheduled pass and its backoff ------------------------------------------------

func TestMissingBacksOffOneThreeThenSevenDays(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, _ := r.nol()
	r.os.offers() // nothing, ever (for now)

	pass := func(wantSearches int, why string) {
		t.Helper()
		if err := r.svc.Missing(bg()); err != nil {
			t.Fatal(err)
		}
		if got := r.os.searchCount(); got != wantSearches {
			t.Fatalf("%s: %d searches so far, want %d", why, got, wantSearches)
		}
	}
	pass(1, "the first look")
	r.advance(23 * time.Hour)
	pass(1, "23 hours after the first miss")
	r.advance(1 * time.Hour)
	pass(2, "a day after the first miss")
	r.advance(2*24*time.Hour + 23*time.Hour)
	pass(2, "2 days 23 hours after the second miss")
	r.advance(1 * time.Hour)
	pass(3, "3 days after the second miss")
	r.advance(6*24*time.Hour + 23*time.Hour)
	pass(3, "6 days 23 hours after the third miss")
	r.advance(1 * time.Hour)
	pass(4, "7 days after the third miss")
	r.advance(7 * 24 * time.Hour)
	pass(5, "7 days after the fourth miss: still 7")
	r.advance(7*24*time.Hour - time.Minute)
	pass(5, "just under 7 days")
	if got := r.results(id, "en"); len(got) != 5 || got[4] != ResultNone {
		t.Errorf("searches = %v", got)
	}

	// The subtitle turns up: found, and then the file is left alone.
	r.os.offers(goodOS(161, "en"))
	r.advance(time.Minute)
	pass(6, "the subtitle turns up")
	if rows := r.subtitleRows(id); len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	r.advance(30 * 24 * time.Hour)
	pass(6, "a file with its subtitle is not searched")

	// Removing the subtitle starts the file afresh: the miss streak ended with the find.
	if err := r.svc.Remove(bg(), r.subtitleRows(id)[0].ID); err != nil {
		t.Fatal(err)
	}
	r.os.offers()
	pass(7, "after the subtitle was removed, no backoff")
}

func TestMissingDoesNotCountAnErrorAsAMiss(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, _ := r.nol()
	r.os.status = http.StatusServiceUnavailable
	for i := 1; i <= 3; i++ {
		if err := r.svc.Missing(bg()); err != nil {
			t.Fatal(err)
		}
		if r.os.searchCount() != i {
			t.Fatalf("pass %d: %d searches; an error must not start a backoff", i, r.os.searchCount())
		}
	}
	if got := r.results(id, "en"); len(got) != 3 || got[0] != ResultError {
		t.Errorf("searches = %v", got)
	}
	// Recovered, and it finds nothing: that is a miss, and now the backoff starts.
	r.os.status = 0
	r.os.offers()
	_ = r.svc.Missing(bg())
	_ = r.svc.Missing(bg())
	if r.os.searchCount() != 4 {
		t.Errorf("%d searches, want 4 (one miss, then waiting)", r.os.searchCount())
	}
}

func TestBackoffIsPerLanguage(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, map[string]any{"languages": []any{"en", "fr"}})
	id, _ := r.nol()
	r.os.offers(goodOS(171, "en")) // English found, French never
	if err := r.svc.Missing(bg()); err != nil {
		t.Fatal(err)
	}
	if r.os.searchCount() != 1 || len(r.subtitleRows(id)) != 1 {
		t.Fatalf("%d searches, rows %+v", r.os.searchCount(), r.subtitleRows(id))
	}
	if got := r.results(id, "fr"); len(got) != 1 || got[0] != ResultNone {
		t.Fatalf("French searches = %v", got)
	}
	// French is waiting; English is done: nothing to ask for.
	r.advance(2 * time.Hour)
	_ = r.svc.Missing(bg())
	if r.os.searchCount() != 1 {
		t.Errorf("%d searches: French is still in its first day's wait", r.os.searchCount())
	}
	r.advance(24 * time.Hour)
	_ = r.svc.Missing(bg())
	if r.os.searchCount() != 2 || r.os.lastSearch().Get("languages") != "fr" {
		t.Errorf("%d searches, last languages %q; want a second one for fr only", r.os.searchCount(), r.os.lastSearch().Get("languages"))
	}
}

func TestMissingLeavesFilesAloneThatAreNotVideosOrNotThere(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, video := r.nol()
	music, err := r.lib.AddFolder(bg(), "music", mkdir(t, filepath.Join(r.root, "music")), "Music")
	if err != nil {
		t.Fatal(err)
	}
	r.videoFile(music, "A/B/01.flac", 100, 5, nil)
	r.os.offers()
	if err := os.Remove(video); err != nil { // the video is gone from disk; the row is still there
		t.Fatal(err)
	}
	if err := r.svc.Missing(bg()); err != nil {
		t.Fatal(err)
	}
	if r.os.searchCount() != 0 || len(r.results(id, "en")) != 0 {
		t.Errorf("%d searches for files that are not videos on disk", r.os.searchCount())
	}
}

func TestMissingJobRunsEveryRescanHours(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, map[string]any{"rescanEveryHours": 48})
	r.nol()
	// Everything found at once, so the file is not waiting out a backoff.
	prov := &fakeProvider{name: "fake"}
	r.svc.custom = func(Config) []Provider { return []Provider{prov} }
	for _, step := range []struct {
		after time.Duration
		want  int
	}{{0, 1}, {24 * time.Hour, 1}, {23 * time.Hour, 1}, {time.Hour, 2}, {47 * time.Hour, 2}, {time.Hour, 3}} {
		r.advance(step.after)
		if err := r.svc.MissingJob(bg()); err != nil {
			t.Fatal(err)
		}
		if prov.searches != step.want {
			t.Fatalf("after +%v: %d passes, want %d", step.after, prov.searches, step.want)
		}
		// a miss each time would start the backoff; keep the pass unconditional by making
		// the file due: forget the log.
		if _, err := r.db.Exec(`DELETE FROM subtitle_searches`); err != nil {
			t.Fatal(err)
		}
	}
}

// ---- quotas and limits ------------------------------------------------------------------

func TestQuotaOfOpenSubtitlesSendsTheNextFileToPodnapisi(t *testing.T) {
	r := newRig(t)
	r.configure(map[string]any{
		"languages": []any{"en"},
		"providers": map[string]any{
			"opensubtitles": map[string]any{"enabled": true, "apiKey": testKey},
			"podnapisi":     map[string]any{"enabled": true},
		},
	})
	id1, _ := r.movieFile("A (1968)/A (1968) Bluray-1080p.mkv", 200_000)
	id2, _ := r.movieFile("B (1968)/B (1968) Bluray-1080p.mkv", 200_000)
	r.os.offers(goodOS(181, "en"), goodOS(182, "en"))
	r.os.remaining = 1
	r.pod.entries = func(url.Values) []map[string]any {
		return []map[string]any{{"id": "pod1", "language": "en", "year": 1968, "releases": []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}}}
	}

	r.svc.AfterImport(bg(), []int64{id1})
	if got := r.os.downloaded(); len(got) != 1 {
		t.Fatalf("OpenSubtitles downloads = %v", got)
	}
	st, err := r.svc.Status(bg())
	if err != nil {
		t.Fatal(err)
	}
	os := st.Providers[0]
	if os.Name != "opensubtitles" || os.QuotaRemaining == nil || *os.QuotaRemaining != 0 || os.OK || os.BlockedUntil == nil {
		t.Errorf("OpenSubtitles status = %+v, want no quota left and blocked until the reset", os)
	}
	if !st.Providers[1].OK || st.Providers[1].Name != "podnapisi" {
		t.Errorf("Podnapisi status = %+v", st.Providers[1])
	}

	// The next file never reaches OpenSubtitles: it is asked nothing until the quota resets.
	searchesBefore := r.os.searchCount()
	r.svc.AfterImport(bg(), []int64{id2})
	if r.os.searchCount() != searchesBefore || len(r.os.downloaded()) != 1 {
		t.Errorf("OpenSubtitles was called while its quota was used up: %d searches, %v", r.os.searchCount(), r.os.downloaded())
	}
	if got := r.pod.downloaded(); len(got) != 1 || got[0] != "pod1" {
		t.Errorf("Podnapisi downloads = %v", got)
	}
	if rows := r.subtitleRows(id2); len(rows) != 1 || rows[0].Provider != "podnapisi" {
		t.Errorf("rows = %+v", rows)
	}

	// After the reset time it is asked again (and says 406: the fake has none left).
	r.advance(13 * time.Hour) // past 2026-11-22T00:00Z
	st, _ = r.svc.Status(bg())
	if st.Providers[0].BlockedUntil != nil {
		t.Errorf("still blocked: %+v", st.Providers[0])
	}
	id3, _ := r.movieFile("C (1968)/C (1968) Bluray-1080p.mkv", 200_000)
	r.svc.AfterImport(bg(), []int64{id3})
	if r.os.searchCount() == searchesBefore {
		t.Error("OpenSubtitles was not asked again after its quota reset")
	}
}

func TestRateLimitBlocksAProviderForRetryAfter(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, _ := r.nol()
	r.os.status, r.os.retryAfter = http.StatusTooManyRequests, "20"
	r.svc.AfterImport(bg(), []int64{id})
	if r.os.searchCount() != 1 {
		t.Fatalf("%d searches", r.os.searchCount())
	}
	r.svc.AfterImport(bg(), []int64{id}) // a moment later: not asked
	if r.os.searchCount() != 1 {
		t.Errorf("%d searches; the provider asked to be left alone for 20 seconds", r.os.searchCount())
	}
	st, _ := r.svc.Status(bg())
	if st.Providers[0].OK || st.Providers[0].BlockedUntil == nil || !strings.Contains(st.Providers[0].Error, "slow down") {
		t.Errorf("status = %+v", st.Providers[0])
	}
	r.advance(21 * time.Second)
	r.os.status = 0
	r.os.offers(goodOS(191, "en"))
	r.svc.AfterImport(bg(), []int64{id})
	if r.os.searchCount() != 2 || len(r.subtitleRows(id)) != 1 {
		t.Errorf("%d searches, rows %+v: after the wait it should work", r.os.searchCount(), r.subtitleRows(id))
	}
	st, _ = r.svc.Status(bg())
	if !st.Providers[0].OK {
		t.Errorf("status = %+v, want ok again", st.Providers[0])
	}
}

func TestQuotaErrorFromTheProviderBlocksItToo(t *testing.T) {
	r := newRig(t)
	prov := &fakeProvider{name: "fake", cands: []Candidate{{Provider: "fake", ProviderID: "1", Language: "en", IDMatch: true, Year: 1968,
		Releases: []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}}},
		dlErr: &QuotaError{Provider: "fake", ResetAt: r.now().Add(5 * time.Hour)}}
	r.configure(map[string]any{"languages": []any{"en"}})
	r.svc.custom = func(Config) []Provider { return []Provider{prov} }
	id, _ := r.nol()
	r.svc.AfterImport(bg(), []int64{id})
	if prov.downloads != 1 {
		t.Fatalf("downloads = %d", prov.downloads)
	}
	if got := r.results(id, "en"); len(got) != 1 || got[0] != ResultError {
		t.Errorf("searches = %v: a quota is not a miss", got)
	}
	r.svc.AfterImport(bg(), []int64{id})
	if prov.searches != 1 {
		t.Errorf("the blocked provider was searched again (%d)", prov.searches)
	}
	if r.svc.anyUsable([]Provider{prov}) {
		t.Error("the provider should be blocked until its quota resets")
	}
	r.advance(6 * time.Hour)
	if !r.svc.anyUsable([]Provider{prov}) {
		t.Error("the provider should be usable after the reset time")
	}
}

// ---- episodes ------------------------------------------------------------------------------

func TestEpisodeSearchUsesTheShowIdAndNumbers(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, _ := r.episodeFile("Pioneer One/Season 01/Pioneer One - S01E02 - Anomalies WEBDL-720p.mkv", 12)
	r.os.offers(
		osItem{FileID: 201, Release: "Pioneer.One.S01E03.720p.WEB-DL.x264-GRP", Language: "en", Downloads: 9999, Season: 1, Episode: 3},
		osItem{FileID: 202, Release: "Pioneer.One.S01E02.720p.WEB-DL.x264-GRP", Language: "en", Downloads: 10, Season: 1, Episode: 2},
	)
	res, err := r.svc.Search(bg(), id, "en")
	if err != nil {
		t.Fatal(err)
	}
	q := r.os.lastSearch()
	if q.Get("parent_imdb_id") != "1621045" || q.Get("season_number") != "1" || q.Get("episode_number") != "2" || q.Get("type") != "episode" {
		t.Errorf("query = %v", q)
	}
	if len(res.Results) != 2 || res.Results[0].ProviderID != "202" || res.Results[1].Score != 0 {
		t.Errorf("results = %+v, want the right episode first and the wrong one rejected", res.Results)
	}
	r.svc.AfterImport(bg(), []int64{id})
	if got := r.os.downloaded(); len(got) != 1 || got[0] != 202 {
		t.Errorf("downloads = %v, want the right episode's subtitle", got)
	}
}

// ---- the pages -------------------------------------------------------------------------------

func TestForFileListsOursAndTheOthers(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, map[string]any{"languages": []any{"en", "fr", "de"}})
	id, video := r.nol()
	r.os.offers(goodOS(211, "en"))
	if _, err := r.svc.Download(bg(), id, "opensubtitles", "211", "en"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SubtitlePath(video, "fr", false), []byte(srtOne), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strings.TrimSuffix(SubtitlePath(video, "de", false), ".srt")+".forced.srt", []byte(srtOne), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := r.svc.ForFile(bg(), id)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]FileSubtitle{}
	for _, s := range got.Subtitles {
		byName[s.Name] = s
		if strings.ContainsAny(s.Name, `/\`) {
			t.Errorf("a path leaked: %q", s.Name)
		}
	}
	en := byName["Night of the Living Dead (1968) Bluray-1080p.en.srt"]
	if en.ID == 0 || en.Source != "marquee" || en.Provider != "opensubtitles" || en.Language != "en" {
		t.Errorf("ours = %+v", en)
	}
	fr := byName["Night of the Living Dead (1968) Bluray-1080p.fr.srt"]
	if fr.ID != 0 || fr.Source != "file" || fr.Language != "fr" {
		t.Errorf("theirs = %+v", fr)
	}
	if len(got.Subtitles) != 3 {
		t.Errorf("subtitles = %+v", got.Subtitles)
	}
	if strings.Join(got.Missing, ",") != "de" || strings.Join(got.Languages, ",") != "en,fr,de" || !got.CanSearch {
		t.Errorf("missing %v languages %v canSearch %v (a forced-only file does not cover German)", got.Missing, got.Languages, got.CanSearch)
	}

	if _, err := r.svc.ForFile(bg(), 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown file: %v", err)
	}
	r.configure(map[string]any{"providers": map[string]any{"podnapisi": map[string]any{"enabled": false}}})
	if got, _ := r.svc.ForFile(bg(), id); got.CanSearch {
		t.Error("canSearch should be false with no provider turned on")
	}
}

func TestStatusCountsVideosMissingASubtitle(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id1, video1 := r.movieFile("A (1968)/A (1968) Bluray-1080p.mkv", 100_000)
	r.movieFile("B (1968)/B (1968) Bluray-1080p.mkv", 100_000)
	r.episodeFile("Pioneer One/Season 01/Pioneer One - S01E01 HDTV-720p.mkv", 11)
	st, err := r.svc.Status(bg())
	if err != nil {
		t.Fatal(err)
	}
	if st.MissingCount != 3 || len(st.Languages) != 1 || len(st.Providers) != 2 {
		t.Errorf("status = %+v", st)
	}
	if st.Providers[0].Name != "opensubtitles" || !st.Providers[0].Enabled || !st.Providers[0].OK ||
		st.Providers[1].Name != "podnapisi" || st.Providers[1].Enabled || st.Providers[1].OK {
		t.Errorf("providers = %+v", st.Providers)
	}

	// A subtitle beside one video, from anyone: two left. The count is kept for a
	// minute, but a download resets it at once.
	if err := os.WriteFile(SubtitlePath(video1, "en", false), []byte(srtOne), 0o644); err != nil {
		t.Fatal(err)
	}
	if st, _ = r.svc.Status(bg()); st.MissingCount != 3 {
		t.Errorf("the count should be cached for a minute: %d", st.MissingCount)
	}
	r.advance(2 * time.Minute)
	if st, _ = r.svc.Status(bg()); st.MissingCount != 2 {
		t.Errorf("after a minute: %d, want 2", st.MissingCount)
	}
	r.os.offers(goodOS(221, "en"))
	if _, err := r.svc.Download(bg(), id1, "opensubtitles", "221", "fr"); err != nil { // French is not wanted yet
		t.Fatal(err)
	}
	if st, _ = r.svc.Status(bg()); st.MissingCount != 2 {
		t.Errorf("a download resets the cached count at once: %d, want 2", st.MissingCount)
	}
	// Wanting French too: the first video has both (one written by hand, one by Marquee).
	r.useOpenSubtitles(false, map[string]any{"languages": []any{"en", "fr"}})
	r.advance(2 * time.Minute)
	if st, _ = r.svc.Status(bg()); st.MissingCount != 2 || len(st.Languages) != 2 {
		t.Errorf("two languages wanted: %+v, want 2 videos still missing something", st)
	}
}

func TestStatusForAProviderThatNeedsAKey(t *testing.T) {
	r := newRig(t)
	r.configure(map[string]any{"providers": map[string]any{"opensubtitles": map[string]any{"enabled": true}}})
	st, err := r.svc.Status(bg())
	if err != nil {
		t.Fatal(err)
	}
	if p := st.Providers[0]; p.OK || !p.Enabled || p.Error == "" {
		t.Errorf("OpenSubtitles with no key = %+v", p)
	}
	if p := st.Providers[1]; !p.OK || p.Error != "" {
		t.Errorf("Podnapisi = %+v", p)
	}
}

// ---- Podnapisi and the settings ------------------------------------------------------------------

func TestPodnapisiEndToEnd(t *testing.T) {
	r := newRig(t)
	r.usePodnapisi(nil)
	id, video := r.nol()
	r.pod.entries = func(q url.Values) []map[string]any {
		return []map[string]any{
			{"id": "p1", "language": "en", "year": 1968, "releases": []string{"Night.of.the.Living.Dead.1968.1080p.BluRay.x264-GRP"}, "stats": map[string]any{"downloads": 12}},
			{"id": "p2", "language": "en", "year": 1978, "releases": []string{"Dawn.of.the.Dead.1978.1080p.BluRay-GRP"}},
		}
	}
	r.pod.zips["p1"] = zipOf(map[string]string{"Night.of.the.Living.Dead.srt": "1\n00:00:01,000 --> 00:00:02,000\nFrom Podnapisi\n\n", "a.nfo": "x"})
	r.svc.AfterImport(bg(), []int64{id})
	if got := r.pod.downloaded(); len(got) != 1 || got[0] != "p1" {
		t.Fatalf("Podnapisi downloads = %v", got)
	}
	if !strings.Contains(r.read(SubtitlePath(video, "en", false)), "From Podnapisi") {
		t.Error("the unzipped subtitle is not next to the video")
	}
	if rows := r.subtitleRows(id); len(rows) != 1 || rows[0].Provider != "podnapisi" || rows[0].ProviderID != "p1" {
		t.Errorf("rows = %+v", rows)
	}
	if r.os.searchCount() != 0 {
		t.Error("OpenSubtitles was asked although it is off")
	}
}

func TestSecretsAreSealedInTheSettings(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(true, nil)
	var raw string
	if err := r.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, Section).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testKey, testPassword} {
		if strings.Contains(raw, secret) {
			t.Errorf("a secret is stored in the clear: %s", raw)
		}
	}
	if !strings.Contains(raw, `"username":"`+testUser+`"`) {
		t.Errorf("the username should be stored as it is: %s", raw)
	}
	masked, err := r.cfg.GetMasked(bg(), Section)
	if err != nil {
		t.Fatal(err)
	}
	os := masked.Value["providers"].(map[string]any)["opensubtitles"].(map[string]any)
	if os["apiKey"] == testKey || os["password"] == testPassword || !strings.HasPrefix(os["apiKey"].(string), "••••") {
		t.Errorf("masked = %v", os)
	}
	cfg := r.svc.Config(bg())
	if cfg.Providers.OpenSubtitles.APIKey != testKey || cfg.Providers.OpenSubtitles.Password != testPassword || !cfg.openSubtitlesReady() {
		t.Errorf("the service did not read the secrets back: %+v", cfg.Providers.OpenSubtitles)
	}
	// Saving the form with the masked values keeps the secrets.
	if _, err := r.cfg.Put(bg(), Section, masked.Value); err != nil {
		t.Fatal(err)
	}
	if got := r.svc.Config(bg()).Providers.OpenSubtitles.APIKey; got != testKey {
		t.Errorf("the key after a save with the mask: %q", got)
	}
}

func TestConfigDefaultsWhenNothingIsSet(t *testing.T) {
	r := newRig(t)
	c := r.svc.Config(bg())
	if len(c.Languages) != 1 || !c.Providers.Podnapisi.Enabled || c.Providers.OpenSubtitles.Enabled || c.MinScore != 60 || c.RescanEveryHours != 24 {
		t.Errorf("defaults = %+v", c)
	}
	// A partly filled section keeps the defaults for the rest.
	r.configure(map[string]any{"languages": []any{"fr"}})
	c = r.svc.Config(bg())
	if c.Languages[0] != "fr" || !c.Providers.Podnapisi.Enabled || c.MinScore != 60 || !c.SearchAfterImport {
		t.Errorf("partial = %+v", c)
	}
}

func TestTestProvider(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(true, nil)
	var a *AuthError
	if err := r.svc.TestProvider(bg(), "opensubtitles", OpenSubtitlesConfig{}); err != nil {
		t.Errorf("the saved key and login: %v", err)
	}
	// The form's masked values are ignored: the saved ones are used.
	if err := r.svc.TestProvider(bg(), "opensubtitles", OpenSubtitlesConfig{APIKey: "••••cdef", Password: "••••"}); err != nil {
		t.Errorf("masked values: %v", err)
	}
	// A password typed into the form is tried, not the saved one.
	if err := r.svc.TestProvider(bg(), "opensubtitles", OpenSubtitlesConfig{Password: "not the password"}); !errors.As(err, &a) {
		t.Errorf("a wrong typed password: %v", err)
	}
	st, _ := r.svc.Status(bg())
	if st.Providers[0].OK {
		t.Errorf("a failed test should show in the status: %+v", st.Providers[0])
	}
	r.pod.entries = func(url.Values) []map[string]any { return nil }
	if err := r.svc.TestProvider(bg(), "podnapisi", OpenSubtitlesConfig{}); err != nil {
		t.Errorf("podnapisi: %v", err)
	}
	var inv *InvalidError
	if err := r.svc.TestProvider(bg(), "napster", OpenSubtitlesConfig{}); !errors.As(err, &inv) {
		t.Errorf("unknown provider: %v", err)
	}
	// With no key anywhere.
	r.configure(map[string]any{"providers": map[string]any{"opensubtitles": map[string]any{"enabled": true}}})
	if err := r.svc.TestProvider(bg(), "opensubtitles", OpenSubtitlesConfig{}); !errors.As(err, &a) {
		t.Errorf("no key: %v", err)
	}
	if err := r.svc.TestProvider(bg(), "opensubtitles", OpenSubtitlesConfig{APIKey: testKey}); err != nil {
		t.Errorf("a key typed into the form: %v", err)
	}
}

func TestEventsAreSentWhenSubtitlesChange(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, _ := r.nol()
	c, cancel := r.hub.Subscribe(EventTopic)
	defer cancel()
	r.os.offers(goodOS(231, "en"))
	sub, err := r.svc.Download(bg(), id, "opensubtitles", "231", "en")
	if err != nil {
		t.Fatal(err)
	}
	expect := func(want string) {
		t.Helper()
		select {
		case m := <-c.C():
			if m.Topic != EventTopic || !strings.Contains(string(m.Data), want) {
				t.Errorf("event = %s %s, want %s", m.Topic, m.Data, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no %q event", want)
		}
	}
	expect(`"event":"added"`)
	if err := r.svc.Remove(bg(), sub.ID); err != nil {
		t.Fatal(err)
	}
	expect(`"event":"removed"`)
}

func TestFilesForTheLibraryFolderGoingAwayKeepTheSubtitleFile(t *testing.T) {
	// The subtitle rows go with the library's file rows (ON DELETE CASCADE); the
	// subtitle file on disk stays.
	r := newRig(t)
	r.useOpenSubtitles(false, nil)
	id, video := r.nol()
	r.os.offers(goodOS(241, "en"))
	if _, err := r.svc.Download(bg(), id, "opensubtitles", "241", "en"); err != nil {
		t.Fatal(err)
	}
	if err := r.lib.RemoveFolder(bg(), r.movie.ID); err != nil {
		t.Fatal(err)
	}
	if n := r.count(`SELECT COUNT(*) FROM subtitles`); n != 0 {
		t.Errorf("%d subtitle rows left", n)
	}
	if !exists(SubtitlePath(video, "en", false)) {
		t.Error("the subtitle file was removed from disk")
	}
}

func TestStoreSearchStateAndPruning(t *testing.T) {
	r := newRig(t)
	id, _ := r.nol()
	st := r.svc.store
	at := func(h int) time.Time { return r.now().Add(time.Duration(h) * time.Hour) }
	add := func(h int, result string) {
		t.Helper()
		if err := st.AddSearch(bg(), id, "en", at(h), result); err != nil {
			t.Fatal(err)
		}
	}
	state := func() (int, time.Time) {
		t.Helper()
		n, last, err := st.SearchState(bg(), id, "en")
		if err != nil {
			t.Fatal(err)
		}
		return n, last
	}
	if n, _ := state(); n != 0 {
		t.Errorf("no searches: %d misses", n)
	}
	add(0, ResultNone)
	add(1, ResultNone)
	add(2, ResultNone)
	if n, last := state(); n != 3 || !last.Equal(at(2)) {
		t.Errorf("three misses: %d, %v", n, last)
	}
	add(3, ResultError)
	if n, _ := state(); n != 0 {
		t.Errorf("an error on top: %d misses", n)
	}
	add(4, ResultNone)
	if n, last := state(); n != 1 || !last.Equal(at(4)) {
		t.Errorf("a miss after an error: %d, %v", n, last)
	}
	add(5, ResultFound)
	add(6, ResultNone)
	add(7, ResultNone)
	if n, _ := state(); n != 2 {
		t.Errorf("two misses after a find: %d", n)
	}

	// Old rows go, but the newest 30 of a file and language stay however old.
	for i := 0; i < 40; i++ {
		add(100+i, ResultNone)
	}
	if err := st.PruneSearches(bg(), at(1000)); err != nil {
		t.Fatal(err)
	}
	if n := r.count(`SELECT COUNT(*) FROM subtitle_searches WHERE file_id = ?`, id); n != 30 {
		t.Errorf("%d rows after pruning, want the newest 30", n)
	}
	if n, _ := state(); n != 30 {
		t.Errorf("misses = %d, want 30 (the cap of what is kept)", n)
	}
}

func TestForTitleListsEveryVideoOfATitle(t *testing.T) {
	r := newRig(t)
	r.useOpenSubtitles(false, map[string]any{"languages": []any{"en", "fr"}})
	id1, v1 := r.episodeFile("Pioneer One/Season 01/Pioneer One - S01E01 HDTV-720p.mkv", 11)
	id2, _ := r.episodeFile("Pioneer One/Season 01/Pioneer One - S01E02 HDTV-720p.mkv", 12)
	r.nol() // another title's file is not in the answer
	if err := os.WriteFile(SubtitlePath(v1, "fr", false), []byte(srtOne), 0o644); err != nil {
		t.Fatal(err)
	}
	r.os.offers(goodOS(251, "en"))
	if _, err := r.svc.Download(bg(), id1, "opensubtitles", "251", "en"); err != nil {
		t.Fatal(err)
	}

	files, err := r.svc.ForTitle(bg(), showID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}
	byID := map[int64]TitleFile{}
	for _, f := range files {
		byID[f.FileID] = f
		if strings.ContainsAny(f.Name, `/\`) {
			t.Errorf("a path leaked: %q", f.Name)
		}
	}
	first, second := byID[id1], byID[id2]
	if first.Name != "Pioneer One - S01E01 HDTV-720p.mkv" || len(first.EpisodeIDs) != 1 || first.EpisodeIDs[0] != 11 ||
		len(first.Subtitles) != 2 || len(first.Missing) != 0 || !first.CanSearch {
		t.Errorf("first = %+v", first)
	}
	if len(second.Subtitles) != 0 || strings.Join(second.Missing, ",") != "en,fr" || second.EpisodeIDs[0] != 12 {
		t.Errorf("second = %+v", second)
	}
	if none, err := r.svc.ForTitle(bg(), 424242); err != nil || len(none) != 0 || none == nil {
		t.Errorf("a title with no files = %v, %v", none, err)
	}
}
