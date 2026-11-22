package subtitles

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// referenceHash is the OpenSubtitles algorithm written the long way, from the
// published description: the file's size, plus the first 64 KB and the last 64 KB
// read as little-endian 64-bit numbers, added modulo 2^64, as 16 hex digits. It
// shares no code with HashFile.
func referenceHash(data []byte) string {
	const chunk = 65536
	h := uint64(len(data))
	words := func(b []byte) {
		for i := 0; i+8 <= len(b); i += 8 {
			var w uint64
			for k := 7; k >= 0; k-- {
				w = w<<8 | uint64(b[i+k])
			}
			h += w
		}
	}
	words(data[:chunk])
	words(data[len(data)-chunk:])
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		out[i] = hexdigits[h&0xf]
		h >>= 4
	}
	return string(out)
}

func writeTemp(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHashFileMatchesTheReferenceAlgorithm(t *testing.T) {
	for _, size := range []int{65536, 65537, 70000, 131072, 300001, 1 << 20} {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i*31 + i>>8)
		}
		got, n, err := HashFile(writeTemp(t, data))
		if err != nil || n != int64(size) {
			t.Fatalf("size %d: %v, %d", size, err, n)
		}
		if want := referenceHash(data); got != want {
			t.Errorf("size %d: hash = %s, want %s", size, got, want)
		}
	}
}

func TestHashFileKnownValues(t *testing.T) {
	// A file of zeros hashes to its own size: 131072 is 0x20000.
	got, _, err := HashFile(writeTemp(t, make([]byte, 131072)))
	if err != nil || got != "0000000000020000" {
		t.Errorf("zeros: %q, %v", got, err)
	}
	// Every byte 0x01: each word is 0x0101010101010101, and there are 8192 of them in
	// each 64 KB end. 16384 * 0x0101010101010101 + 200000, modulo 2^64.
	ones := make([]byte, 200000)
	for i := range ones {
		ones[i] = 1
	}
	got, _, err = HashFile(writeTemp(t, ones))
	word, count, size := uint64(0x0101010101010101), uint64(16384), uint64(200000) // variables: the sum wraps at run time
	want := count*word + size
	if err != nil || got != hex16(want) {
		t.Errorf("ones: %q, %v; want %s", got, err, hex16(want))
	}
	// The sum wraps: all-0xFF words add 2^64-1 each.
	ff := make([]byte, 131072)
	for i := range ff {
		ff[i] = 0xff
	}
	got, _, err = HashFile(writeTemp(t, ff))
	allOnes, n131072 := ^uint64(0), uint64(131072)
	want = n131072 + count*allOnes
	if err != nil || got != hex16(want) {
		t.Errorf("wrapping: %q, %v; want %s", got, err, hex16(want))
	}
	// Changing a byte outside the two ends does not change the hash; inside, it does.
	data := make([]byte, 400000)
	base, _, _ := HashFile(writeTemp(t, data))
	data[200000] = 9
	if h, _, _ := HashFile(writeTemp(t, data)); h != base {
		t.Error("a byte in the middle changed the hash")
	}
	data[10] = 9
	if h, _, _ := HashFile(writeTemp(t, data)); h == base {
		t.Error("a byte at the start did not change the hash")
	}
}

func hex16(n uint64) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		out[i] = hexdigits[n&0xf]
		n >>= 4
	}
	return string(out)
}

func TestHashFileErrors(t *testing.T) {
	if _, size, err := HashFile(writeTemp(t, make([]byte, 65535))); !errors.Is(err, ErrTooSmall) || size != 65535 {
		t.Errorf("a 64 KB-less file: %v, %d", err, size)
	}
	if _, _, err := HashFile(filepath.Join(t.TempDir(), "nope.mkv")); err == nil {
		t.Error("a missing file should be an error")
	}
}
