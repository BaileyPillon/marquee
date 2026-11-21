package web

import (
	"bytes"
	"io"
	"testing"

	"github.com/andybalholm/brotli"
)

func compressBR(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, 5)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decompressBR(t *testing.T, b []byte) []byte {
	t.Helper()
	out, err := io.ReadAll(brotli.NewReader(bytes.NewReader(b)))
	if err != nil {
		t.Fatalf("not valid brotli: %v", err)
	}
	return out
}
