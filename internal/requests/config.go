package requests

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/BaileyPillon/marquee/internal/library"
	"github.com/BaileyPillon/marquee/internal/settings"
)

// VideoFolders are the library folders (by id) that movies or shows go to. A
// null id means "pick one": the first folder of that kind, and for 4K one that
// looks like a 4K folder.
type VideoFolders struct {
	HD  *int64 `json:"hd"`
	UHD *int64 `json:"uhd"`
}

// MusicFolders is the folder music goes to. Music has one quality.
type MusicFolders struct {
	HD *int64 `json:"hd"`
}

// FolderChoices are the owner's folder choices per kind of title.
type FolderChoices struct {
	Movie  VideoFolders `json:"movie"`
	Show   VideoFolders `json:"show"`
	Artist MusicFolders `json:"artist"`
}

// Config is the "requests" settings section: the defaults every person's
// limits fall back to, and where copies go.
//
// A weekly limit of Unlimited (-1) means no limit. In JSON that is written as
// null with "hdUnlimited" (or "uhdUnlimited") true; reading also accepts the word
// "unlimited" for the number, so {"hdPerWeek": "unlimited"} works too.
type Config struct {
	HDPerWeek      int
	UHDPerWeek     int
	AutoApproveHD  bool
	AutoApproveUHD bool
	Folders        FolderChoices
}

// configWire is Config as JSON.
type configWire struct {
	HDPerWeek      *int          `json:"hdPerWeek"`
	HDUnlimited    bool          `json:"hdUnlimited"`
	UHDPerWeek     *int          `json:"uhdPerWeek"`
	UHDUnlimited   bool          `json:"uhdUnlimited"`
	AutoApproveHD  bool          `json:"autoApproveHD"`
	AutoApproveUHD bool          `json:"autoApproveUHD"`
	Folders        FolderChoices `json:"folders"`
}

func splitLimit(n int) (*int, bool) {
	if n == Unlimited {
		return nil, true
	}
	return &n, false
}

// MarshalJSON writes an unlimited default as null plus the matching flag.
func (c Config) MarshalJSON() ([]byte, error) {
	w := configWire{AutoApproveHD: c.AutoApproveHD, AutoApproveUHD: c.AutoApproveUHD, Folders: c.Folders}
	w.HDPerWeek, w.HDUnlimited = splitLimit(c.HDPerWeek)
	w.UHDPerWeek, w.UHDUnlimited = splitLimit(c.UHDPerWeek)
	return json.Marshal(w)
}

// UnmarshalJSON reads over what the Config already holds: a field that is left
// out, or that makes no sense, keeps its value (so the defaults show through).
func (c *Config) UnmarshalJSON(b []byte) error {
	var raw struct {
		HDPerWeek      json.RawMessage `json:"hdPerWeek"`
		HDUnlimited    *bool           `json:"hdUnlimited"`
		UHDPerWeek     json.RawMessage `json:"uhdPerWeek"`
		UHDUnlimited   *bool           `json:"uhdUnlimited"`
		AutoApproveHD  *bool           `json:"autoApproveHD"`
		AutoApproveUHD *bool           `json:"autoApproveUHD"`
		Folders        *FolderChoices  `json:"folders"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	var err error
	if c.HDPerWeek, err = readLimit(raw.HDPerWeek, raw.HDUnlimited, c.HDPerWeek); err != nil {
		return err
	}
	if c.UHDPerWeek, err = readLimit(raw.UHDPerWeek, raw.UHDUnlimited, c.UHDPerWeek); err != nil {
		return err
	}
	if raw.AutoApproveHD != nil {
		c.AutoApproveHD = *raw.AutoApproveHD
	}
	if raw.AutoApproveUHD != nil {
		c.AutoApproveUHD = *raw.AutoApproveUHD
	}
	if raw.Folders != nil {
		c.Folders = *raw.Folders
	}
	return nil
}

// readLimit reads one weekly limit over cur: the flag or the word "unlimited" or -1
// mean no limit, a whole number is that limit, and a value that is left out or null
// leaves cur. Text that is not "unlimited", or a fraction, is an error (the section
// does not read). A number below -1 is returned as it is; LoadConfig turns it back
// into the default.
func readLimit(raw json.RawMessage, flag *bool, cur int) (int, error) {
	if flag != nil && *flag {
		return Unlimited, nil
	}
	n, set, err := parseLimit(raw)
	if err != nil {
		return cur, err
	}
	if !set {
		return cur, nil
	}
	return n, nil
}

var errBadLimit = errors.New(`a limit is a whole number or "unlimited"`)

// parseLimit reads a limit value's syntax: a whole number, or the word "unlimited"
// (Unlimited). set is false for a value that was left out or is null.
func parseLimit(raw json.RawMessage) (n int, set bool, err error) {
	s := strings.TrimSpace(string(raw))
	switch {
	case s == "" || s == "null":
		return 0, false, nil
	case strings.HasPrefix(s, `"`):
		var word string
		if err := json.Unmarshal(raw, &word); err != nil || !strings.EqualFold(strings.TrimSpace(word), "unlimited") {
			return 0, false, errBadLimit
		}
		return Unlimited, true, nil
	}
	var num json.Number
	if err := json.Unmarshal(raw, &num); err != nil {
		return 0, false, errBadLimit
	}
	v, err := num.Int64()
	if err != nil || v > 1<<31 || v < -(1<<31) {
		return 0, false, errBadLimit
	}
	return int(v), true, nil
}

// NormalizeConfig checks what the owner sent for the "requests" section and
// returns it in the form it is stored and shown in: unlimited limits are null with
// the flag set, fields left out take their defaults. A limit that is not a whole
// number from 0 to 10000 or the word "unlimited" is an *InvalidError.
func NormalizeConfig(in map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, invalid("invalid_request", "That is not a valid settings section.")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, invalid("invalid_request", "That is not a valid settings section.")
	}
	for _, key := range []string{"hdPerWeek", "uhdPerWeek"} {
		n, set, err := parseLimit(probe[key])
		if err == nil && set && n != Unlimited && (n < 0 || n > maxPerWeek) {
			err = errBadLimit
		}
		if err != nil {
			return nil, invalid("invalid_limit", key+": "+err.Error()+".")
		}
	}
	c := DefaultConfig()
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, invalid("invalid_request", "That is not a valid settings section.")
	}
	out, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	return m, json.Unmarshal(out, &m)
}

// DefaultConfig is what the section holds until the owner changes it.
func DefaultConfig() Config {
	return Config{HDPerWeek: 10, UHDPerWeek: 2, AutoApproveHD: true, AutoApproveUHD: false}
}

// LoadConfig reads the "requests" section over the defaults: a field that was
// never set keeps its default. It returns the defaults, with the error, when the
// stored section cannot be read.
func LoadConfig(ctx context.Context, s *settings.Service) (Config, error) {
	c := DefaultConfig()
	if s == nil {
		return c, nil
	}
	if _, err := s.Get(ctx, SettingsSection, &c); err != nil {
		return DefaultConfig(), err
	}
	def := DefaultConfig()
	if c.HDPerWeek < Unlimited {
		c.HDPerWeek = def.HDPerWeek
	}
	if c.UHDPerWeek < Unlimited {
		c.UHDPerWeek = def.UHDPerWeek
	}
	return c, nil
}

// SaveConfig stores c as the whole "requests" section.
func SaveConfig(ctx context.Context, s *settings.Service, c Config) error {
	return s.Set(ctx, SettingsSection, c)
}

// folderID is the folder the owner chose for a kind and quality, or nil.
func (c Config) folderID(kind, quality string) *int64 {
	switch kind {
	case KindMovie:
		if quality == QualityUHD {
			return c.Folders.Movie.UHD
		}
		return c.Folders.Movie.HD
	case KindShow:
		if quality == QualityUHD {
			return c.Folders.Show.UHD
		}
		return c.Folders.Show.HD
	case KindArtist:
		return c.Folders.Artist.HD
	}
	return nil
}

// names4K reports whether text names a 4K folder.
func names4K(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "4k") || strings.Contains(text, "uhd")
}

// own4K reports whether the folder itself is named for 4K: its label, or the
// last part of its path.
func own4K(f library.Folder) bool {
	return names4K(f.Label) || names4K(filepath.Base(f.Path))
}

// any4K reports whether the label or any part of the path names a 4K folder
// (a "4K" parent folder, say).
func any4K(f library.Folder) bool { return names4K(f.Label) || names4K(f.Path) }

// pickFolder chooses the library folder a copy goes to.
//
//   - The folder the owner chose for this kind and quality wins, if it is still
//     a folder of the right kind.
//   - For 4K, otherwise: the first folder whose label or path contains "4k" or
//     "uhd", a folder named that way before one that merely sits under such a
//     folder; if there is none, the folder the HD copy would use.
//   - For HD, otherwise: the first folder of the kind that does not look like a
//     4K folder (so the 4K folder is not taken for the main one); if every
//     folder looks like one, the first that is not named for 4K itself, else the
//     first.
//
// folders must be in library order (oldest first).
func pickFolder(c Config, kind, quality string, folders []library.Folder) (library.Folder, bool) {
	want := libraryKind(kind)
	var ofKind []library.Folder
	for _, f := range folders {
		if f.Kind == want {
			ofKind = append(ofKind, f)
		}
	}
	if len(ofKind) == 0 {
		return library.Folder{}, false
	}
	chosen := func(q string) (library.Folder, bool) {
		id := c.folderID(kind, q)
		if id == nil {
			return library.Folder{}, false
		}
		for _, f := range ofKind {
			if f.ID == *id {
				return f, true
			}
		}
		return library.Folder{}, false
	}

	if f, ok := chosen(quality); ok {
		return f, true
	}
	if quality == QualityUHD {
		// A folder named for 4K, else one that sits under such a folder.
		for _, f := range ofKind {
			if own4K(f) {
				return f, true
			}
		}
		for _, f := range ofKind {
			if any4K(f) {
				return f, true
			}
		}
	}
	// The HD choice (also the answer for 4K when there is no 4K folder).
	if f, ok := chosen(QualityHD); ok {
		return f, true
	}
	for _, f := range ofKind {
		if !any4K(f) {
			return f, true
		}
	}
	for _, f := range ofKind {
		if !own4K(f) {
			return f, true
		}
	}
	return ofKind[0], true
}
