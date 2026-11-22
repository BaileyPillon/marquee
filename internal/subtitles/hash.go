package subtitles

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// hashChunk is how much of each end of the file the OpenSubtitles hash reads.
const hashChunk = 64 * 1024

// ErrTooSmall: the file is under 64 KB, which the hash cannot read.
var ErrTooSmall = errors.New("subtitles: the file is too small to hash")

// HashFile computes the OpenSubtitles movie hash of a file: the file's size
// plus every 64-bit little-endian word of its first 64 KB and its last 64 KB,
// added up modulo 2^64, written as 16 lower-case hex digits. It also returns the
// size. Two copies of the same video have the same hash, which is how the
// providers match a subtitle to the exact file.
func HashFile(path string) (hash string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	size = st.Size()
	if size < hashChunk {
		return "", size, ErrTooSmall
	}
	sum := uint64(size)
	buf := make([]byte, hashChunk)
	for _, off := range []int64{0, size - hashChunk} {
		if _, err := f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
			return "", size, err
		}
		for i := 0; i+8 <= len(buf); i += 8 {
			sum += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return fmt.Sprintf("%016x", sum), size, nil
}
