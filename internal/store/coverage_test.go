package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/johnlindquist/gmkit/internal/coverage"
	"github.com/johnlindquist/gmkit/internal/store"
)

func TestFolderCoverageSurvivesReopenAndFailedRescan(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	st, e := store.Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	c, e := st.CachedCoverage(ctx)
	if e != nil || len(c.FolderScans) != 0 || c.MessageHistory.Complete {
		t.Fatalf("fresh cache coverage: %+v %v", c, e)
	}
	if e = st.SaveFolderScan(ctx, coverage.Folder{Folder: "inbox", Complete: true, Count: 2}, []string{"a", "b"}); e != nil {
		t.Fatal(e)
	}
	st.Close()
	st, e = store.Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	if e = st.SaveFolderScan(ctx, coverage.Folder{Folder: "inbox", Complete: false, StopReason: "offline", Count: 1}, []string{"a"}); e != nil {
		t.Fatal(e)
	}
	c, e = st.CachedCoverage(ctx)
	if e != nil || len(c.FolderScans) != 1 || c.FolderScans[0].Complete || c.FolderScans[0].StopReason != "offline" {
		t.Fatalf("failed scan hid behind old success: %+v %v", c, e)
	}
}
