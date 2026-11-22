package subtitles

import (
	"context"
	"fmt"
	"time"

	"github.com/BaileyPillon/marquee/internal/parse"
)

// VideoInfo is what a provider search needs to know about a video file.
type VideoInfo struct {
	FileID   int64
	Path     string // full path of the video on disk
	FileName string // its base name
	Kind     string // "movie" or "show"
	Title    string // the film's name, or the show's
	Year     int
	IMDb     string // "tt0063350": the film's, or the show's
	TMDb     int64
	Season   int // episodes
	Episode  int // episodes: the first one in the file
	// Hash is the OpenSubtitles movie hash of the file, "" when it could not be
	// read (the file is under 64 KB or unreadable).
	Hash string
	Size int64
	// Parsed is the video's file name read as a release name (group, source,
	// resolution).
	Parsed parse.Release
}

// IsEpisode reports whether the video is an episode.
func (v VideoInfo) IsEpisode() bool { return v.Kind == "show" }

// Query is one search of one provider: a video and the languages wanted.
type Query struct {
	Video     VideoInfo
	Languages []string
}

// Candidate is one subtitle a provider offers.
type Candidate struct {
	Provider string `json:"provider"`
	// ProviderID is what Download needs: the provider's own id for the file.
	ProviderID string `json:"providerId"`
	Language   string `json:"language"`
	// Releases are the release names the uploader gave it (OpenSubtitles one,
	// Podnapisi several).
	Releases  []string `json:"releases"`
	HI        bool     `json:"hi"`
	Downloads int      `json:"downloads"`
	// HashMatch says the provider matched the subtitle to the exact video file by
	// its hash.
	HashMatch bool `json:"hashMatch"`
	// IDMatch says the provider matched it by IMDb or TMDb id (not by a text search).
	IDMatch bool `json:"idMatch"`
	// Machine marks a machine-made or machine-translated subtitle.
	Machine bool `json:"machine"`
	// Year, Season and Episode are what the provider says the subtitle is for
	// (0 when it does not say).
	Year    int `json:"year"`
	Season  int `json:"season"`
	Episode int `json:"episode"`

	// Score and Reasons are filled in by Score.
	Score   int      `json:"score"`
	Reasons []string `json:"reasons"`
}

// DownloadResult is a downloaded subtitle and what the provider said about the
// quota.
type DownloadResult struct {
	Data []byte
	// Remaining is how many downloads the provider says are left today, nil when
	// it does not say.
	Remaining *int
	// ResetAt is when the quota resets, zero when unknown.
	ResetAt time.Time
}

// Provider is one place to get subtitles from.
type Provider interface {
	// Name is "opensubtitles" or "podnapisi".
	Name() string
	// Search lists what the provider has for the video in the languages. It may
	// return results of other languages (they are dropped).
	Search(ctx context.Context, q Query) ([]Candidate, error)
	// Download fetches one subtitle. The bytes are as the provider sent them
	// (a Podnapisi zip is already unpacked); the caller fixes the text encoding.
	Download(ctx context.Context, c Candidate) (DownloadResult, error)
	// Test checks the provider answers and accepts the owner's key or login.
	Test(ctx context.Context) error
}

// ---- provider errors ---------------------------------------------------------------

// QuotaError says the daily download quota is used up.
type QuotaError struct {
	Provider string
	ResetAt  time.Time // zero when unknown
}

func (e *QuotaError) Error() string {
	if e.ResetAt.IsZero() {
		return fmt.Sprintf("%s: the daily download quota is used up", e.Provider)
	}
	return fmt.Sprintf("%s: the daily download quota is used up until %s", e.Provider, e.ResetAt.UTC().Format("15:04 UTC"))
}

// RateLimitError says the provider asked Marquee to slow down.
type RateLimitError struct {
	Provider   string
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("%s asked Marquee to slow down", e.Provider)
}

// AuthError says the provider did not accept the owner's key or login.
type AuthError struct {
	Provider string
	Message  string
}

func (e *AuthError) Error() string { return e.Message }

// UnavailableError says the provider did not answer, or answered with an error.
type UnavailableError struct {
	Provider string
	Message  string
}

func (e *UnavailableError) Error() string { return e.Message }
