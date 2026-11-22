package library

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFileByIdAndRenameFile(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	f, root := e.addFolder("movie")
	mkFiles(t, root, "a.mkv", "b.mkv")
	for _, rel := range []string{"a.mkv", "b.mkv"} {
		if err := e.svc.ImportFiles(ctx, f.ID, []ImportedFile{{RelPath: rel, TitleID: 7, Quality: "Bluray-1080p"}}); err != nil {
			t.Fatal(err)
		}
	}
	rows := e.files(f)
	a, b := rows["a.mkv"], rows["b.mkv"]

	got, err := e.svc.File(ctx, a.ID)
	if err != nil || got.ID != a.ID || got.RelPath != "a.mkv" || got.FolderID != f.ID || got.TitleID == nil || *got.TitleID != 7 {
		t.Fatalf("File = %+v, %v", got, err)
	}
	if _, err := e.svc.File(ctx, 99999); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such file: %v", err)
	}

	// The row takes the new path and keeps everything else, including its id.
	later := time.Date(2021, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := e.svc.RenameFile(ctx, a.ID, `Film (1999)\Film (1999) Bluray-1080p.mkv`, later); err != nil {
		t.Fatal(err)
	}
	renamed, _ := e.svc.File(ctx, a.ID)
	if renamed.RelPath != "Film (1999)/Film (1999) Bluray-1080p.mkv" || renamed.TitleID == nil || *renamed.TitleID != 7 ||
		renamed.Quality != "Bluray-1080p" || renamed.State != StateMatched || !renamed.MTime.Equal(later) || renamed.MissingSince != nil {
		t.Errorf("renamed = %+v", renamed)
	}
	if _, old := e.files(f)["a.mkv"]; old {
		t.Error("the old path should be gone")
	}

	// Another row's path is refused, and so is a path that leaves the folder.
	if err := e.svc.RenameFile(ctx, a.ID, "b.mkv", later); !errors.Is(err, ErrNameInUse) {
		t.Errorf("a name in use: %v", err)
	}
	for _, bad := range []string{"", "../x.mkv", "/x.mkv", "C:\\x.mkv", "."} {
		if err := e.svc.RenameFile(ctx, b.ID, bad, later); !errors.Is(err, ErrBadRelPath) {
			t.Errorf("RenameFile(%q) = %v, want ErrBadRelPath", bad, err)
		}
	}
	if err := e.svc.RenameFile(ctx, 99999, "z.mkv", later); !errors.Is(err, ErrNotFound) {
		t.Errorf("no such file: %v", err)
	}
	if still, _ := e.svc.File(ctx, b.ID); still.RelPath != "b.mkv" {
		t.Errorf("a refused rename changed the row: %+v", still)
	}
}
