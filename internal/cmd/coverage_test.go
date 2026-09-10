package cmd_test

import (
	"encoding/json"
	"testing"
)

func TestCachedReadReportsUnknownCoverageAndOutputLimit(t *testing.T) {
	dir := seedStore(t)
	out := runCmd(t, dir, "--json", "chats", "list", "--source", "cached", "--limit", "1")
	var r struct {
		Coverage struct {
			Source      string
			Truncated   bool
			FolderScans []any `json:"folder_scans"`
		}
		Data []any
	}
	if e := json.Unmarshal([]byte(out), &r); e != nil {
		t.Fatal(e)
	}
	if r.Coverage.Source != "cached" || !r.Coverage.Truncated || len(r.Coverage.FolderScans) != 0 || len(r.Data) != 1 {
		t.Fatalf("untruthful cache metadata: %s", out)
	}
}
func TestLiveReadDoesNotFallBackToCachedRows(t *testing.T) {
	dir := seedStore(t)
	if _, e := runCmdAllowError(t, dir, "--json", "chats", "list"); e == nil {
		t.Fatal("unpaired live read fell back to cache")
	}
}
