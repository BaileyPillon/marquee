// Package subtitles finds subtitles for the movies and episodes in the library
// and puts them next to the video files.
//
// Two providers sit behind one small interface: OpenSubtitles.com (the owner's
// own API key) and Podnapisi (no key). A search asks every enabled provider for
// the languages the owner wants, scores each result against the video (the
// exact file by its hash first, then the release name: group, source and
// resolution) and downloads the best one that clears the owner's minimum score.
// The file is written as "<video name>.<language>.srt" ("<name>.<language>.hi.srt"
// for the hearing impaired). Marquee never overwrites a subtitle file it did not
// write; the subtitles table is how it knows which ones it did.
//
// Three things start a search: the importer saying it just added files
// (AfterImport), the scheduled job that looks for files still missing a wanted
// language (Missing, waiting 1, then 3, then 7 days between looks at one file),
// and a person pressing "Find subtitles" (Search, then Download).
//
// SQL lives in store.go. Every outbound call goes through core/outbound. Tests
// use httptest fakes of both providers; nothing here calls the real services
// unless it is built and started that way.
package subtitles

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/titles"
)

// Names of the providers, as the settings and the API write them.
const (
	ProviderOpenSubtitles = "opensubtitles"
	ProviderPodnapisi     = "podnapisi"
)

// Section is the settings section that holds the Config.
const Section = "subtitles"

// Scheduler job and timings.
const (
	// JobMissing is the scheduled job. It wakes up every JobInterval and runs a
	// pass when Config.RescanEveryHours have gone by since the last one.
	JobMissing  = "subtitles.missing"
	JobInterval = time.Hour

	// EventTopic is the SSE topic that says a file got or lost a subtitle:
	// {"fileId": 12, "event": "added"|"removed"}.
	EventTopic = "subtitles"

	// maxPerPass bounds how many files one pass of the job looks at.
	maxPerPass = 500
	// maxLanguages bounds the wanted languages.
	maxLanguages = 12
	// downloadAttempts is how many of the best results are tried for one
	// language when a download fails.
	downloadAttempts = 3
	// searchKeep is how long a manual search's results can still be downloaded.
	searchKeep = 15 * time.Minute
)

// backoff is how long to leave a file alone after a look that found nothing:
// the first miss 1 day, the second 3, the third and every one after 7.
var backoff = []time.Duration{24 * time.Hour, 3 * 24 * time.Hour, 7 * 24 * time.Hour}

// Hearing-impaired preferences.
const (
	HIPreferNot = "prefer-not"
	HIPrefer    = "prefer"
	HIOnly      = "only"
)

// Results of one look for one language, as subtitle_searches records them.
const (
	ResultFound = "found"
	ResultNone  = "none"
	ResultError = "error"
)

// Errors the web layer maps to HTTP statuses.
var (
	// ErrNotFound: no such file, or no such subtitle.
	ErrNotFound = errors.New("subtitles: not found")
	// ErrNotVideo: the file is not a movie or an episode matched to a title.
	ErrNotVideo = errors.New("subtitles: only a movie or an episode in the library has subtitles")
	// ErrFileMissing: the video is not on disk (its library folder is gone, or the file moved).
	ErrFileMissing = errors.New("subtitles: the video file is not on disk")
	// ErrNoProvider: no provider is turned on and set up.
	ErrNoProvider = errors.New("subtitles: no subtitle source is set up")
	// ErrForeignFile: a subtitle file by that name is already there and Marquee
	// did not write it, so it was left alone.
	ErrForeignFile = errors.New("subtitles: a subtitle file with that name is already there and Marquee did not write it")
	// ErrResultGone: a download was asked for a result of a search that has been forgotten.
	ErrResultGone = errors.New("subtitles: that subtitle is no longer in the list")
	// ErrNotSubtitle: a provider sent something that is not a subtitle file.
	ErrNotSubtitle = errors.New("subtitles: the provider sent something that is not a subtitle")
)

// InvalidError is a request the caller can fix; Message is plain words.
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &InvalidError{Message: fmt.Sprintf(format, args...)}
}

// ---- what the module needs from the others ---------------------------------------

// Library is the part of the library service the subtitles need.
type Library interface {
	File(ctx context.Context, id int64) (library.File, error)
	Files(ctx context.Context, f library.FileFilter) ([]library.File, int, error)
	Folder(ctx context.Context, id int64) (library.Folder, error)
}

// Catalog is the part of the titles service the subtitles need.
type Catalog interface {
	Get(ctx context.Context, id int64) (*titles.Detail, error)
}

// Settings is the part of the settings service the subtitles need: it reads the
// "subtitles" section with its secrets opened.
type Settings interface {
	Get(ctx context.Context, section string, dst any) (bool, error)
}
