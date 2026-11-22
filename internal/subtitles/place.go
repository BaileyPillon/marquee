package subtitles

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// maxSubtitleBytes is the biggest subtitle file Marquee accepts (a real one is
// well under 500 KB), and so the biggest thing read out of a zip.
const maxSubtitleBytes = 5 << 20

// SubtitlePath is where a subtitle of a video goes: next to it, named like it:
//
//	/media/Movie (1999)/Movie (1999).mkv  ->  /media/Movie (1999)/Movie (1999).en.srt
//	                                          /media/Movie (1999)/Movie (1999).en.hi.srt
//
// language is the code as the settings write it ("en", "pt-br").
func SubtitlePath(videoPath, language string, hi bool) string {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	name := base + "." + strings.ToLower(language)
	if hi {
		name += ".hi"
	}
	return name + ".srt"
}

// WriteSubtitle writes data to path and never replaces a file Marquee did not
// write. ours says the file already at path (if there is one) was written by
// Marquee: then it is replaced, through a temporary file so a failure leaves the
// old one. A file that is there and not ours gives ErrForeignFile and is not
// touched; so does one that appears while the new file is being made.
func WriteSubtitle(path string, data []byte, ours bool) error {
	if st, err := os.Lstat(path); err == nil {
		if !ours || !st.Mode().IsRegular() {
			return ErrForeignFile
		}
		return replaceFile(path, data)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrForeignFile
		}
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func replaceFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".marquee-subtitle-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// ---- reading what a provider sent --------------------------------------------------

// ExtractSRT returns the subtitle inside a zip archive: the biggest .srt file in
// it. Data that is not a zip is returned as it is. An archive with no .srt, or
// one that would unpack to something too big, is an error.
func ExtractSRT(data []byte) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return data, nil
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("the archive could not be opened: %w", err)
	}
	if len(zr.File) > 200 {
		return nil, errors.New("the archive holds too many files")
	}
	var best *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !strings.EqualFold(filepath.Ext(f.Name), ".srt") {
			continue
		}
		if f.UncompressedSize64 > maxSubtitleBytes {
			return nil, errors.New("a file in the archive is too big to be a subtitle")
		}
		if best == nil || f.UncompressedSize64 > best.UncompressedSize64 {
			best = f
		}
	}
	if best == nil {
		return nil, errors.New("the archive holds no .srt file")
	}
	rc, err := best.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	out, err := io.ReadAll(io.LimitReader(rc, maxSubtitleBytes+1))
	if err != nil {
		return nil, err
	}
	if len(out) > maxSubtitleBytes {
		return nil, errors.New("a file in the archive is too big to be a subtitle")
	}
	return out, nil
}

// cp1252 maps bytes 0x80 to 0x9F of Windows-1252 to their characters; the rest
// of that code page is Latin-1.
var cp1252 = [32]rune{
	0x20AC, 0x81, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x8D, 0x017D, 0x8F,
	0x90, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x9D, 0x017E, 0x0178,
}

// ToUTF8 turns the bytes of a subtitle file into UTF-8 text: a byte-order mark
// is dropped, UTF-16 (with a mark) is converted, text that is already valid
// UTF-8 is kept, and anything else is read as Windows-1252, the old default of
// subtitle files in western languages. It does not guess other code pages.
func ToUTF8(b []byte) []byte {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return b[3:]
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return decodeUTF16(b[2:], false)
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return decodeUTF16(b[2:], true)
	case utf8.Valid(b):
		return b
	}
	var sb strings.Builder
	sb.Grow(len(b) + len(b)/8)
	for _, c := range b {
		switch {
		case c >= 0x80 && c <= 0x9F:
			sb.WriteRune(cp1252[c-0x80])
		default:
			sb.WriteRune(rune(c))
		}
	}
	return []byte(sb.String())
}

func decodeUTF16(b []byte, bigEndian bool) []byte {
	units := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if bigEndian {
			units = append(units, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			units = append(units, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	return []byte(string(utf16.Decode(units)))
}

// CheckSubtitle reports whether text looks like a subtitle: SubRip has lines
// with "-->" between two times. It stops a provider's error page, or a zip of
// something else, from being saved as a subtitle.
func CheckSubtitle(text []byte) error {
	if len(bytes.TrimSpace(text)) == 0 || !bytes.Contains(text, []byte("-->")) {
		return ErrNotSubtitle
	}
	if len(text) > maxSubtitleBytes {
		return ErrNotSubtitle
	}
	return nil
}
