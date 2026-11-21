package web

import (
	"context"
	"net/http"
	"strconv"
	"testing"
)

// addFileRow puts a file row straight into the library's table.
func (a *catalogApp) addFileRow(folder int64, rel string, size int64, quality string, title any, episodes, track any, state string, missing any) {
	a.t.Helper()
	_, err := a.mainDB.Exec(`INSERT INTO files (folder_id, rel_path, size, mtime, kind, quality, title_id, episode_ids, track_id,
		match_state, added_at, missing_since) VALUES (?, ?, ?, 0, 'movie', ?, ?, ?, ?, ?, '2026-01-01T00:00:00.000Z', ?)`,
		folder, rel, size, quality, title, episodes, track, state, missing)
	if err != nil {
		a.t.Fatal(err)
	}
}

func asSlice(t *testing.T, v any) []map[string]any {
	t.Helper()
	list, _ := v.([]any)
	out := make([]map[string]any, len(list))
	for i, x := range list {
		out[i], _ = x.(map[string]any)
	}
	return out
}

func TestTitleListAndDetailCarryWhatIsOnDisk(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()
	a.setTMDBKey(owner)
	member := a.member(owner, "Priya")

	folder, err := a.lib.AddFolder(context.Background(), "movie", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}

	movie := owner.do("POST", "/api/v1/titles", map[string]any{"kind": "movie", "provider": "tmdb", "providerId": 19})
	show := owner.do("POST", "/api/v1/titles", map[string]any{"kind": "show", "provider": "tvmaze", "providerId": 99001})
	if movie.Status != http.StatusCreated || show.Status != http.StatusCreated {
		t.Fatalf("add = %d %d", movie.Status, show.Status)
	}
	movieID, showID := idOf(t, movie), idOf(t, show)

	// A title nothing is on disk for says so.
	for _, c := range []*client{owner, member} {
		r := c.send("GET", "/api/v1/titles", nil)
		for _, it := range asSlice(t, r.JSON(t)["items"]) {
			if it["fileCount"] != float64(0) || it["inLibrary"] != false || it["quality"] != "" || it["sizeBytes"] != float64(0) {
				t.Errorf("a title with no file: %v", it)
			}
		}
	}

	// The movie has two files (the better one is 4K) and one that is gone; the
	// show has a two-episode file.
	a.addFileRow(folder.ID, "m1.mkv", 1000, "Bluray-1080p", movieID, nil, nil, "matched", nil)
	a.addFileRow(folder.ID, "m2.mkv", 3000, "WEBDL-2160p", movieID, nil, nil, "matched", nil)
	a.addFileRow(folder.ID, "m3.mkv", 500, "Remux-2160p", movieID, nil, nil, "matched", "2026-03-01T00:00:00.000Z")
	r := owner.send("GET", titlePath(showID), nil)
	var ep1, ep2, ep3 float64
	for _, e := range asSlice(t, r.JSON(t)["episodes"]) {
		switch {
		case e["season"] == float64(1) && e["number"] == float64(1):
			ep1 = e["id"].(float64)
		case e["season"] == float64(1) && e["number"] == float64(2):
			ep2 = e["id"].(float64)
		case e["season"] == float64(2):
			ep3 = e["id"].(float64)
		}
	}
	a.addFileRow(folder.ID, "s1.mkv", 700, "HDTV-720p", showID, "["+fid(ep1)+","+fid(ep2)+"]", nil, "matched", nil)
	a.addFileRow(folder.ID, "s1b.mkv", 400, "WEBDL-1080p", showID, "["+fid(ep1)+"]", nil, "matched", nil)

	for _, c := range []*client{owner, member} { // every signed-in person sees them
		r = c.send("GET", "/api/v1/titles", nil)
		byID := map[float64]map[string]any{}
		for _, it := range asSlice(t, r.JSON(t)["items"]) {
			byID[it["id"].(float64)] = it
		}
		mv, sh := byID[float64(movieID)], byID[float64(showID)]
		if mv["fileCount"] != float64(2) || mv["quality"] != "WEBDL-2160p" || mv["inLibrary"] != true || mv["sizeBytes"] != float64(4000) {
			t.Errorf("movie in the list = %v", mv)
		}
		if sh["fileCount"] != float64(2) || sh["quality"] != "WEBDL-1080p" || sh["inLibrary"] != true || sh["sizeBytes"] != float64(1100) {
			t.Errorf("show in the list = %v", sh)
		}
		if _, has := mv["episodes"]; has {
			t.Error("list items must not carry children")
		}
	}

	r = member.send("GET", titlePath(movieID), nil)
	if d := r.JSON(t); d["fileCount"] != float64(2) || d["quality"] != "WEBDL-2160p" || d["inLibrary"] != true || d["sizeBytes"] != float64(4000) {
		t.Errorf("movie detail = %s", r.Body)
	}

	r = member.send("GET", titlePath(showID), nil)
	byEp := map[float64]map[string]any{}
	for _, e := range asSlice(t, r.JSON(t)["episodes"]) {
		byEp[e["id"].(float64)] = e
	}
	if e := byEp[ep1]; e["hasFile"] != true || e["quality"] != "WEBDL-1080p" || e["size"] != float64(1100) || e["name"] != "Earthfall" {
		t.Errorf("episode 1 = %v", e)
	}
	if e := byEp[ep2]; e["hasFile"] != true || e["quality"] != "HDTV-720p" || e["size"] != float64(700) {
		t.Errorf("episode 2 = %v (a two-episode file counts for both)", e)
	}
	if e := byEp[ep3]; e["hasFile"] != false || e["quality"] != "" || e["size"] != float64(0) {
		t.Errorf("episode 3 = %v", e)
	}

	// A refresh and a PATCH answer the same shape.
	if r = owner.do("PATCH", titlePath(movieID), map[string]any{"monitored": false}); r.JSON(t)["fileCount"] != float64(2) {
		t.Errorf("patch = %s", r.Body)
	}
	if r = owner.do("POST", titlePath(movieID)+"/refresh", nil); r.JSON(t)["quality"] != "WEBDL-2160p" {
		t.Errorf("refresh = %s", r.Body)
	}
}

func TestArtistTracksCarryWhatIsOnDisk(t *testing.T) {
	a := newCatalogApp(t)
	owner := a.owner()
	folder, err := a.lib.AddFolder(context.Background(), "music", t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	hydrated := a.waitTitlesEvent(t, "hydrated")
	r := owner.do("POST", "/api/v1/titles", map[string]any{"kind": "artist", "provider": "musicbrainz", "providerId": chopinMBID})
	if r.Status != http.StatusAccepted {
		t.Fatalf("add artist = %d %s", r.Status, r.Body)
	}
	if r.JSON(t)["hydrating"] != true || r.JSON(t)["title"].(map[string]any)["hydrating"] != true {
		t.Errorf("the answer does not say the artist is still loading: %s", r.Body)
	}
	id := int64(hydrated())
	r = owner.send("GET", titlePath(id), nil)
	var firstTrack, secondTrack float64
	for _, al := range asSlice(t, r.JSON(t)["albums"]) {
		if al["name"] == "Nocturnes" {
			tracks := asSlice(t, al["tracks"])
			firstTrack, secondTrack = tracks[0]["id"].(float64), tracks[1]["id"].(float64)
		}
	}
	a.addFileRow(folder.ID, "01.flac", 30, "FLAC", id, nil, int64(firstTrack), "matched", nil)
	a.addFileRow(folder.ID, "01.mp3", 8, "MP3-320kbps", id, nil, int64(firstTrack), "matched", nil)

	r = owner.send("GET", titlePath(id), nil)
	if d := r.JSON(t); d["fileCount"] != float64(2) || d["quality"] != "FLAC" || d["inLibrary"] != true || d["sizeBytes"] != float64(38) {
		t.Errorf("artist = %v", d["fileCount"])
	}
	for _, al := range asSlice(t, r.JSON(t)["albums"]) {
		for _, tr := range asSlice(t, al["tracks"]) {
			switch tr["id"].(float64) {
			case firstTrack:
				if tr["hasFile"] != true || tr["quality"] != "FLAC" || tr["size"] != float64(38) {
					t.Errorf("track 1 = %v", tr)
				}
			case secondTrack:
				if tr["hasFile"] != false || tr["quality"] != "" || tr["size"] != float64(0) {
					t.Errorf("track 2 = %v", tr)
				}
			}
		}
	}
}

func fid(f float64) string { return strconv.FormatInt(int64(f), 10) }
