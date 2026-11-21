package library

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// FileSummary is what the matched files of a title, an episode or a track add
// up to. Files that are gone from disk (missing) and files that are not matched
// to anything are not counted.
type FileSummary struct {
	// Count is how many files there are.
	Count int `json:"count"`
	// Bytes is their size together.
	Bytes int64 `json:"bytes"`
	// Quality is the best quality name among them ("Bluray-1080p", "FLAC"),
	// empty when there is no file.
	Quality string `json:"quality"`
}

// add folds one group of files into the summary.
func (f *FileSummary) add(g fileGroup) {
	f.Count += g.Count
	f.Bytes += g.Bytes
	if f.Quality == "" || QualityRank(g.Quality) > QualityRank(f.Quality) {
		f.Quality = g.Quality
	}
}

func summarize(groups []fileGroup) map[int64]FileSummary {
	out := map[int64]FileSummary{}
	for _, g := range groups {
		s := out[g.ID]
		s.add(g)
		out[g.ID] = s
	}
	return out
}

// TitleFiles summarises the files of each title (a movie's file, a show's
// episodes, an artist's tracks), in one query. A title with no file has no
// entry.
func (s *Service) TitleFiles(ctx context.Context, titleIDs []int64) (map[int64]FileSummary, error) {
	if len(titleIDs) == 0 {
		return map[int64]FileSummary{}, nil
	}
	groups, err := s.store.titleFileGroups(ctx, titleIDs)
	if err != nil {
		return nil, err
	}
	return summarize(groups), nil
}

// EpisodeFiles summarises the files of each episode of a show, keyed by episode
// id. A file that holds two episodes counts for both. An episode with no file
// has no entry.
func (s *Service) EpisodeFiles(ctx context.Context, titleID int64) (map[int64]FileSummary, error) {
	groups, err := s.store.episodeFileGroups(ctx, titleID)
	if err != nil {
		return nil, err
	}
	return summarize(groups), nil
}

// TrackFiles summarises the files of each track of an artist, keyed by track
// id. A track with no file has no entry.
func (s *Service) TrackFiles(ctx context.Context, titleID int64) (map[int64]FileSummary, error) {
	groups, err := s.store.trackFileGroups(ctx, titleID)
	if err != nil {
		return nil, err
	}
	return summarize(groups), nil
}

// ---- which quality is better -------------------------------------------------

var resolutionRe = regexp.MustCompile(`(\d{3,4})p`)

// QualityRank orders quality names (as parse.QualityName writes them) from
// worst to best: for video the resolution first, then the source (Remux above
// Bluray above WEB-DL above WEBRip above HDTV); for music lossless above lossy,
// and a higher bitrate or bit depth above a lower one. Names it does not know
// rank 0.
func QualityRank(name string) int {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || n == "unknown" {
		return 0
	}
	if m := resolutionRe.FindStringSubmatch(n); m != nil {
		res, _ := strconv.Atoi(m[1])
		return res*10 + sourceRank(n)
	}
	switch {
	case strings.HasPrefix(n, "raw-hd"):
		return 10800 + 5
	case strings.HasPrefix(n, "dvd") && !strings.HasPrefix(n, "dvdscr"):
		return 4801
	case n == "sdtv":
		return 4800
	case strings.HasPrefix(n, "dvdscr"):
		return 400
	case n == "telecine":
		return 300
	case n == "telesync":
		return 200
	case n == "cam":
		return 100
	}
	return musicRank(n)
}

func sourceRank(n string) int {
	switch {
	case strings.HasPrefix(n, "remux"):
		return 9
	case strings.HasPrefix(n, "bluray"):
		return 8
	case strings.HasPrefix(n, "webdl"):
		return 6
	case strings.HasPrefix(n, "webrip"):
		return 5
	case strings.HasPrefix(n, "hdtv"):
		return 4
	}
	return 1 // a source that is not stated
}

var numberRe = regexp.MustCompile(`\d+`)

// musicRank ranks "FLAC", "FLAC-24bit", "MP3-320kbps": lossless sits above every
// lossy name.
func musicRank(n string) int {
	format, _, _ := strings.Cut(n, "-")
	num := 0
	if m := numberRe.FindString(n[len(format):]); m != "" {
		num, _ = strconv.Atoi(m)
	}
	switch format {
	case "flac", "alac", "wav", "ape", "wavpack", "aiff":
		return 20000 + num
	}
	return 1000 + num
}
