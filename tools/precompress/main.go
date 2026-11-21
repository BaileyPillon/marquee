// Command precompress brotli-compresses a built web directory in place.
//
// For every .js .css .html .svg .json .txt .webmanifest file of 512 bytes or
// more it writes <file>.br at quality 11, then deletes the original, except
// index.html, which keeps both (the server uses the raw copy for browsers
// that do not accept brotli). Fonts and images are already compressed and
// are left alone.
//
// Usage: go run ./tools/precompress web/dist
package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
)

const minSize = 512

var compressible = map[string]bool{
	".js": true, ".css": true, ".html": true, ".svg": true,
	".json": true, ".txt": true, ".webmanifest": true,
}

type result struct {
	path       string
	before     int64
	after      int64
	skipReason string
	err        error
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: precompress <dir>")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "precompress:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	dirBefore, err := dirSize(dir)
	if err != nil {
		return err
	}

	var targets []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !compressible[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() >= minSize {
			targets = append(targets, p)
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Quality 11 is slow, so spread the files over the CPUs.
	jobs := make(chan string)
	results := make(chan result)
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				results <- compress(p)
			}
		}()
	}
	go func() {
		for _, p := range targets {
			jobs <- p
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var (
		files         int
		before, after int64
		failed        bool
	)
	for r := range results {
		rel, _ := filepath.Rel(dir, r.path)
		switch {
		case r.err != nil:
			failed = true
			fmt.Fprintf(os.Stderr, "  FAILED %s: %v\n", rel, r.err)
		case r.skipReason != "":
			fmt.Printf("  skipped %s (%s)\n", rel, r.skipReason)
		default:
			files++
			before += r.before
			after += r.after
		}
	}
	if failed {
		return fmt.Errorf("some files could not be compressed")
	}

	dirAfter, err := dirSize(dir)
	if err != nil {
		return err
	}
	fmt.Printf("precompress %s\n", dir)
	fmt.Printf("  %d files compressed: %s -> %s (%s)\n", files, human(before), human(after), pct(after, before))
	fmt.Printf("  directory total:     %s -> %s (%s)\n", human(dirBefore), human(dirAfter), pct(dirAfter, dirBefore))
	return nil
}

func compress(p string) result {
	raw, err := os.ReadFile(p)
	if err != nil {
		return result{path: p, err: err}
	}
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, brotli.BestCompression) // quality 11
	if _, err := w.Write(raw); err != nil {
		return result{path: p, err: err}
	}
	if err := w.Close(); err != nil {
		return result{path: p, err: err}
	}
	// Not worth shipping a .br that is no smaller than the file.
	if buf.Len() >= len(raw) {
		return result{path: p, skipReason: "does not compress"}
	}
	if err := os.WriteFile(p+".br", buf.Bytes(), 0o644); err != nil {
		return result{path: p, err: err}
	}
	if filepath.Base(p) != "index.html" {
		if err := os.Remove(p); err != nil {
			return result{path: p, err: err}
		}
	}
	return result{path: p, before: int64(len(raw)), after: int64(buf.Len())}
}

func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func human(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.2f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func pct(after, before int64) string {
	if before == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%% of original", float64(after)*100/float64(before))
}
