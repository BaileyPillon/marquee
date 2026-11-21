package library

import (
	"context"
	"testing"
)

func TestListFoldersIsPlainAndOldestFirst(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if got, err := e.svc.ListFolders(ctx); err != nil || len(got) != 0 {
		t.Fatalf("ListFolders on an empty library = %v, %v", got, err)
	}
	a, _ := e.addFolder(KindMovie, "Metropolis (1927)/Metropolis (1927).mkv")
	b, _ := e.addFolder(KindShow)
	c, _ := e.addFolder(KindMusic)

	got, err := e.svc.ListFolders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != a.ID || got[1].ID != b.ID || got[2].ID != c.ID {
		t.Fatalf("ListFolders = %+v, want %d, %d, %d in that order", got, a.ID, b.ID, c.ID)
	}
	if got[0].Kind != KindMovie || got[0].Path != a.Path || got[0].Label != a.Label || got[1].Kind != KindShow || got[2].Kind != KindMusic {
		t.Errorf("ListFolders = %+v", got)
	}
}
