package requests

import (
	"context"
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
type Config struct {
	HDPerWeek      int           `json:"hdPerWeek"`
	UHDPerWeek     int           `json:"uhdPerWeek"`
	AutoApproveHD  bool          `json:"autoApproveHD"`
	AutoApproveUHD bool          `json:"autoApproveUHD"`
	Folders        FolderChoices `json:"folders"`
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
	if c.HDPerWeek < 0 {
		c.HDPerWeek = def.HDPerWeek
	}
	if c.UHDPerWeek < 0 {
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
