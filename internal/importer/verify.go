package importer

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/parse"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// srcFile is one media file found in a finished download.
type srcFile struct {
	Path  string // full path on this machine
	Rel   string // under the download's own folder, with "/"
	Size  int64
	Owned bool // unpacked by the importer: safe to move even for a torrent
}

// plan is a file that passed the check and knows where it is going.
type plan struct {
	src      srcFile
	dest     string // under the library folder, with "/"
	titleID  int64
	episodes []int64 // episode ids (shows)
	epNums   []int   // episode numbers (shows), for the name
	trackID  int64   // the catalog track (music), 0 when the match was not sure
	albumID  int64   // the catalog album (music)
	season   int
	quality  string // "Bluray-1080p"; for music "FLAC"
	parsed   parse.Release
	label    string // what to call it in a note: "S01E02" or the film's name
}

// skip is a file the importer left alone, and why.
type skip struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
	// Existing marks a file left out because the library already has it (the same
	// file, or one that ranks the same or better), which is no failure.
	Existing bool `json:"-"`
}

var (
	reSeasonFolder = regexp.MustCompile(`(?i)^(?:(?:season|series|saison|staffel|temporada)[ ._-]*\d{1,4}(?:[^0-9].*)?|s\d{1,4}|specials?)$`)
	reHashName     = regexp.MustCompile(`^[a-f0-9]{8,}$`)
)

// genericTitles are names a file gets when its release name was stripped: a
// title worth nothing, so the folder or the release name is read instead.
var genericTitles = map[string]bool{
	"movie": true, "video": true, "film": true, "main": true, "feature": true, "untitled": true,
	"video ts": true, "title": true, "episode": true,
}

// usefulTitle reports whether a parsed title says anything about what the file
// is (not empty, not a hash, not "movie").
func usefulTitle(t string) bool {
	n := parse.Normalize(t)
	if n == "" || genericTitles[n] {
		return false
	}
	compact := strings.ReplaceAll(n, " ", "")
	if reHashName.MatchString(compact) {
		return false
	}
	letters := 0
	for _, r := range n {
		if r >= 'a' && r <= 'z' {
			letters++
		}
	}
	return letters > 0 || len([]rune(n)) > 3
}

// nameMatches reports whether a title read from a file name is the catalog
// title: the normalized names are equal (the title's own or an alternative).
func nameMatches(d *titles.Detail, name string) bool {
	n := parse.Normalize(name)
	if n == "" {
		return false
	}
	if parse.Normalize(d.Name) == n {
		return true
	}
	for _, alt := range d.AltNames {
		if parse.Normalize(alt) == n {
			return true
		}
	}
	return false
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// names lists what can say what a file is, most specific first: the file's own
// name, then each folder above it up to the download's folder, then the
// release name the download was grabbed as. Season folders carry no title.
func names(root string, f srcFile, release string) []string {
	out := []string{filepath.Base(f.Path)}
	dir := filepath.Dir(f.Path)
	// root is "" when the download is a single file: the folder it sits in is
	// not the download's own, so it says nothing.
	for i := 0; root != "" && i < 8; i++ {
		base := filepath.Base(dir)
		if base != "." && base != string(filepath.Separator) && !reSeasonFolder.MatchString(base) &&
			!strings.HasPrefix(base, ".marquee-extract") && !strings.HasPrefix(base, "marquee-extract") && !isArchiveStaging(base) {
			out = append(out, base)
		}
		if sameDir(dir, root) || filepath.Dir(dir) == dir {
			break
		}
		dir = filepath.Dir(dir)
	}
	if release != "" {
		out = append(out, release)
	}
	return out
}

// isArchiveStaging reports the folder names the importer itself makes while
// unpacking ("a00", "a01"...).
func isArchiveStaging(name string) bool {
	return len(name) == 3 && name[0] == 'a' && name[1] >= '0' && name[1] <= '9' && name[2] >= '0' && name[2] <= '9'
}

func sameDir(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	return strings.EqualFold(ca, cb)
}

// bestQuality picks, from the readings of a file's names, the first that says
// anything about quality (a source or a resolution), file name first.
func bestQuality(rs []parse.Release) parse.Release {
	for _, r := range rs {
		if r.Source != parse.SourceUnknown || r.Resolution > 0 {
			return r
		}
	}
	if len(rs) > 0 {
		return rs[0]
	}
	return parse.Release{}
}

func parseAll(list []string) []parse.Release {
	out := make([]parse.Release, len(list))
	for i, n := range list {
		out[i] = parse.ParseRelease(n)
	}
	return out
}

// verifier checks the files of one download against one title.
type verifier struct {
	d       *titles.Detail
	root    string
	release string         // what the download was grabbed as
	wanted  map[int64]bool // episode ids (shows) or album ids (artists) the download is for

	folderHint, releaseHint *parse.MusicRelease // music: what the download's own names say
	hintsDone               bool
}

func newVerifier(d *titles.Detail, root, release string, episodeIDs []int64) *verifier {
	v := &verifier{d: d, root: root, release: release, wanted: map[int64]bool{}}
	for _, id := range episodeIDs {
		v.wanted[id] = true
	}
	return v
}

// verify reads every file and returns the ones that are the title (with their
// destinations) and the ones that are not (with the reason).
func (v *verifier) verify(files []srcFile) ([]plan, []skip) {
	var (
		plans []plan
		skips []skip
	)
	for _, f := range files {
		var (
			p  plan
			ok bool
			sk string
		)
		switch v.d.Kind {
		case titles.KindShow:
			p, ok, sk = v.show(f)
		case titles.KindArtist:
			p, ok, sk = v.music(f)
		default:
			p, ok, sk = v.movie(f)
		}
		if !ok {
			skips = append(skips, skip{File: f.Rel, Reason: sk})
			continue
		}
		plans = append(plans, p)
	}
	plans, skips = dedupePlans(plans, skips)
	sort.SliceStable(plans, func(i, j int) bool { return plans[i].dest < plans[j].dest })
	return plans, skips
}

// dedupePlans keeps one file for each destination: when two files of a download
// are the same film or episode, the bigger one wins (the other is a sample or a
// worse copy), and the loser is noted.
func dedupePlans(plans []plan, skips []skip) ([]plan, []skip) {
	best := map[string]int{}
	var keep []plan
	for _, p := range plans {
		key := strings.ToLower(p.dest)
		i, seen := best[key]
		if !seen {
			best[key] = len(keep)
			keep = append(keep, p)
			continue
		}
		if p.src.Size > keep[i].src.Size {
			skips = append(skips, skip{File: keep[i].src.Rel, Reason: "Another file in the download is the same " + kindWord(p) + " and bigger"})
			keep[i] = p
		} else {
			skips = append(skips, skip{File: p.src.Rel, Reason: "Another file in the download is the same " + kindWord(p) + " and bigger"})
		}
	}
	return keep, skips
}

func kindWord(p plan) string {
	if len(p.epNums) > 0 {
		return "episode"
	}
	return "file"
}

// ---- movies --------------------------------------------------------------------

func (v *verifier) movie(f srcFile) (plan, bool, string) {
	list := names(v.root, f, v.release)
	reads := parseAll(list)
	if reads[0].IsTV() {
		return plan{}, false, "This looks like a TV episode, not a film"
	}
	idx := -1
	for i, r := range reads {
		if usefulTitle(r.Title) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return plan{}, false, "The name does not say what this file is"
	}
	read := reads[idx]
	year, yearInTitle := read.Year, false
	switch {
	case nameMatches(v.d, read.Title):
	case read.Year >= 1900 && nameMatches(v.d, read.Title+" "+strconv.Itoa(read.Year)):
		year, yearInTitle = 0, true // the number was part of the title ("Blade Runner 2049")
	default:
		return plan{}, false, fmt.Sprintf("This is %q, not %q", read.Title, v.d.Name)
	}
	if year == 0 {
		// The file's own name has no year; a folder or the release name may.
		for i, r := range reads {
			if r.Year > 0 && !(yearInTitle && i == idx) {
				year = r.Year
				break
			}
		}
	}
	if v.d.Year > 0 && year > 0 && absInt(year-v.d.Year) > 1 {
		return plan{}, false, fmt.Sprintf("Wrong year: this is from %d, you want the %d one", year, v.d.Year)
	}
	q := bestQuality(reads)
	quality := parse.QualityName(q)
	return plan{
		src: f, titleID: v.d.ID, quality: quality, parsed: q, label: v.d.Name,
		dest: MoviePath(MovieName{Name: v.d.Name, Year: v.d.Year, Quality: quality, Ext: filepath.Ext(f.Path)}),
	}, true, ""
}

// ---- shows ---------------------------------------------------------------------

func (v *verifier) show(f srcFile) (plan, bool, string) {
	list := names(v.root, f, v.release)
	reads := parseAll(list)
	idx := -1
	for i, r := range reads {
		if usefulTitle(r.Title) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return plan{}, false, "The name does not say which show this is"
	}
	if t := reads[idx].Title; !nameMatches(v.d, t) {
		return plan{}, false, fmt.Sprintf("This is %q, not %q", t, v.d.Name)
	}

	// The episode numbers: the file's own name first, then its folders. The
	// release name only helps a file that is the whole download.
	var ep parse.Release
	found := false
	for i, r := range reads {
		last := i == len(reads)-1 && v.release != ""
		if last && r.FullSeason {
			break
		}
		if len(r.Episodes) > 0 || r.AirDate != "" {
			ep, found = r, true
			break
		}
	}
	if !found {
		return plan{}, false, "Cannot tell which episode this is: the name has no season and episode numbers"
	}
	season, nums := ep.Season, ep.Episodes
	if len(nums) == 0 && ep.AirDate != "" {
		for _, e := range v.d.Episodes {
			if e.AirDate == ep.AirDate {
				season, nums = e.Season, []int{e.Number}
				break
			}
		}
		if len(nums) == 0 {
			return plan{}, false, "No episode aired on " + ep.AirDate
		}
	}
	index := map[[2]int]titles.Episode{}
	for _, e := range v.d.Episodes {
		index[[2]int{e.Season, e.Number}] = e
	}
	var (
		ids     []int64
		epNames []string
	)
	for _, n := range nums {
		e, ok := index[[2]int{season, n}]
		if !ok {
			return plan{}, false, fmt.Sprintf("S%02dE%02d is not in the catalog for this show", season, n)
		}
		ids = append(ids, e.ID)
		epNames = append(epNames, e.Name)
	}
	if len(v.wanted) > 0 {
		hit := false
		for _, id := range ids {
			if v.wanted[id] {
				hit = true
			}
		}
		if !hit {
			return plan{}, false, fmt.Sprintf("S%02dE%02d is not one of the episodes asked for", season, nums[0])
		}
	}
	q := bestQuality(reads)
	quality := parse.QualityName(q)
	return plan{
		src: f, titleID: v.d.ID, episodes: ids, epNums: nums, season: season, quality: quality, parsed: q,
		label: episodeCode(season, nums),
		dest: EpisodePath(EpisodeName{Show: v.d.Name, Year: v.d.Year, Season: season, Episodes: nums,
			EpisodeNames: epNames, Quality: quality, Ext: filepath.Ext(f.Path)}),
	}, true, ""
}

// ---- music ---------------------------------------------------------------------

// musicHints reads the download's own names the way a release is named: its
// folder (nil when the download is a single file) and the release it was
// grabbed as. A scene-style folder says the artist and album when the track
// paths inside it do not.
func (v *verifier) musicHints() (folder, release *parse.MusicRelease) {
	if !v.hintsDone {
		v.hintsDone = true
		if v.root != "" {
			if base := filepath.Base(v.root); base != "" && base != "." && !strings.HasPrefix(base, ".marquee-extract") && !strings.HasPrefix(base, "marquee-extract") {
				h := parse.ParseMusicRelease(base)
				v.folderHint = &h
			}
		}
		if v.release != "" {
			h := parse.ParseMusicRelease(v.release)
			v.releaseHint = &h
		}
	}
	return v.folderHint, v.releaseHint
}

// albumOf finds the catalog album a track file belongs to: by the album its
// path names, then the album its folder names (a scene name the path reader
// could not split). Only when neither names an album at all does the release
// the download was grabbed as have a say: a folder that names another album is
// not overruled by the release's label.
func (v *verifier) albumOf(pathAlbum string) *titles.Album {
	folder, release := v.musicHints()
	names := []string{pathAlbum}
	if folder != nil && !folder.Discography {
		names = append(names, folder.Album)
	}
	named := false
	for _, n := range names {
		if strings.TrimSpace(n) != "" {
			named = true
		}
	}
	if !named && release != nil && !release.Discography {
		names = append(names, release.Album)
	}
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			continue
		}
		// The album the owner asked for comes first: two editions of an artist's
		// album can share a name.
		for pass := 0; pass < 2; pass++ {
			for i := range v.d.Albums {
				al := &v.d.Albums[i]
				if (pass == 0) == v.wanted[al.ID] && parse.AlbumNameMatches(al.Name, n) {
					return al
				}
			}
		}
	}
	return nil
}
func (v *verifier) music(f srcFile) (plan, bool, string) {
	// Read the path from the download's own folder (so the album folder counts),
	// or the bare file name when the download is one file.
	rel := f.Rel
	if v.root != "" {
		if base := filepath.Base(v.root); base != "" && base != "." {
			rel = base + "/" + rel
		}
	}
	mr := parse.ParseMusic(rel)
	if mr.Artist != "" && !nameMatches(v.d, mr.Artist) && !v.hintsNameArtist() {
		return plan{}, false, fmt.Sprintf("This is by %q, not %q", mr.Artist, v.d.Name)
	}
	if mr.Track <= 0 {
		return plan{}, false, "Cannot tell the track number from the name"
	}
	al := v.albumOf(mr.Album)
	if al == nil {
		return plan{}, false, "Cannot tell which album this track is from"
	}

	// The track: by its number and title among the album's tracks.
	tracks := make([]parse.CatalogTrack, len(al.Tracks))
	multi := mr.Disc > 1
	for i, t := range al.Tracks {
		tracks[i] = parse.CatalogTrack{ID: t.ID, Disc: t.Disc, Position: t.Position, Name: t.Name}
		if t.Disc > 1 {
			multi = true
		}
	}
	number, disc, title := mr.Track, mr.Disc, mr.Title
	var trackID int64
	switch tr, how := parse.MatchTrack(tracks, mr.Disc, mr.Track, mr.Title); how {
	case parse.SureTrack:
		// The catalog's number and title name the file (the title may have won over
		// a shifted number).
		trackID, number, title = tr.ID, tr.Position, tr.Name
		if multi || tr.Disc > 1 {
			disc = max(tr.Disc, 1)
		}
	case parse.WeakTrack:
		// Probably this track, but the title disagrees: keep the file's own title
		// and do not claim the track.
	}
	if title == "" {
		title = mr.Title
	}

	quality := mr.QualityName()
	if mr.Bitrate == "" {
		folder, release := v.musicHints()
		for _, h := range []*parse.MusicRelease{folder, release} {
			if h != nil && h.Bitrate != "" && (h.Format == "" || h.Format == mr.Format) {
				quality = mr.Format + "-" + h.Bitrate
				break
			}
		}
	}
	return plan{
		src: f, titleID: v.d.ID, albumID: al.ID, trackID: trackID, quality: quality, label: fmt.Sprintf("%02d %s", number, title),
		dest: TrackPath(TrackName{Artist: v.d.Name, Album: al.Name, Year: al.Year, Disc: disc, MultiDisc: multi,
			Track: number, Title: title, Ext: filepath.Ext(f.Path)}),
	}, true, ""
}

// hintsNameArtist reports whether the download's own names say the artist is
// the title: a folder the path reader could not split (a scene name) is not
// evidence against the artist.
func (v *verifier) hintsNameArtist() bool {
	folder, release := v.musicHints()
	for _, h := range []*parse.MusicRelease{folder, release} {
		if h != nil && h.Artist != "" && nameMatches(v.d, h.Artist) {
			return true
		}
	}
	return false
}

// libraryKind is the library folder kind that holds a title kind.
func libraryKind(titleKind string) string {
	if titleKind == titles.KindArtist {
		return library.KindMusic
	}
	return titleKind
}
