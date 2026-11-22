package subtitles

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/BaileyPillon/marquee/internal/library"
)

// iso3 maps the three-letter language codes subtitle files are sometimes named
// with to the two-letter ones the settings use.
var iso3 = map[string]string{
	"eng": "en", "fra": "fr", "fre": "fr", "deu": "de", "ger": "de", "spa": "es", "ita": "it", "por": "pt",
	"rus": "ru", "jpn": "ja", "kor": "ko", "zho": "zh", "chi": "zh", "nld": "nl", "dut": "nl", "swe": "sv",
	"nor": "no", "dan": "da", "fin": "fi", "pol": "pl", "tur": "tr", "ara": "ar", "heb": "he", "hin": "hi",
	"ell": "el", "gre": "el", "ces": "cs", "cze": "cs", "hun": "hu", "ron": "ro", "rum": "ro", "tha": "th",
	"ind": "id", "vie": "vi", "ukr": "uk", "bul": "bg", "hrv": "hr", "srp": "sr", "slk": "sk", "slv": "sl",
	"fas": "fa", "per": "fa",
}

// normalizeLanguage writes a language code the way the settings do: lower case,
// "-" between the language and the region, two letters when there is a two-letter
// code ("eng" is "en").
func normalizeLanguage(code string) string {
	code = strings.ToLower(strings.TrimSpace(strings.ReplaceAll(code, "_", "-")))
	if two, ok := iso3[code]; ok {
		return two
	}
	return code
}

// subtitleExts are the extensions of subtitle files other tools write.
var subtitleExts = map[string]bool{".srt": true, ".ass": true, ".ssa": true, ".vtt": true, ".sub": true}

// External is a subtitle file next to a video, whoever wrote it.
type External struct {
	// Name is the file's name (never its folder).
	Name string
	// Language is the code in the name ("en", "pt-br"), "" when it has none.
	Language string
	HI       bool
	Forced   bool
}

// dirCache lists a folder once however many videos sit in it.
type dirCache struct {
	names map[string][]string
}

func newDirCache() *dirCache { return &dirCache{names: map[string][]string{}} }

func (c *dirCache) list(dir string) []string {
	if names, ok := c.names[dir]; ok {
		return names
	}
	var names []string
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				names = append(names, e.Name())
			}
		}
	}
	c.names[dir] = names
	return names
}

// externalSubtitles finds the subtitle files that sit beside a video and are
// named after it: "<video name>.<language>[.hi|.forced].<ext>". The video's own
// name must be followed by a dot, so "Movie (1999) Extra.en.srt" is not Movie
// (1999)'s.
func externalSubtitles(videoPath string, c *dirCache) []External {
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	prefix := strings.ToLower(base) + "."
	var out []External
	for _, name := range c.list(dir) {
		ext := strings.ToLower(filepath.Ext(name))
		if !subtitleExts[ext] {
			continue
		}
		if strings.EqualFold(strings.TrimSuffix(name, filepath.Ext(name)), base) {
			out = append(out, External{Name: name}) // "<video name>.srt": a subtitle that names no language
			continue
		}
		if len(name) <= len(prefix)+len(ext) || !strings.HasPrefix(strings.ToLower(name), prefix) {
			continue
		}
		middle := name[len(prefix) : len(name)-len(ext)]
		x := External{Name: name}
		for i, tok := range strings.Split(strings.ToLower(middle), ".") {
			switch {
			case tok == "forced":
				x.Forced = true
			case i > 0 && (tok == "hi" || tok == "sdh" || tok == "cc"):
				x.HI = true
			case x.Language == "" && i == 0:
				if l := normalizeLanguage(tok); reLanguage.MatchString(l) {
					x.Language = l
				}
			}
		}
		out = append(out, x)
	}
	return out
}

// missingLanguages are the wanted languages a video has no subtitle for: none
// written by Marquee, and no file of that language beside the video (a forced-only
// file does not count).
func (s *Service) missingLanguages(ctx context.Context, f library.File, abs string, cfg Config, cache *dirCache) ([]string, error) {
	if len(cfg.Languages) == 0 {
		return nil, nil
	}
	tracked, err := s.store.List(ctx, f.ID)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, t := range tracked {
		have[t.Language] = true
	}
	for _, x := range externalSubtitles(abs, cache) {
		if x.Language != "" && !x.Forced {
			have[x.Language] = true
		}
	}
	var missing []string
	for _, l := range cfg.Languages {
		if !have[l] {
			missing = append(missing, l)
		}
	}
	return missing, nil
}
