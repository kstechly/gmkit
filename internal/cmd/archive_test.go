package cmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveCLIRejectsReadOnlyAndMissingSession(t *testing.T) {
	dir := seedStore(t)
	if _, err := runCmdAllowError(t, dir, "chats", "archive", "c_alice"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("expected read-only rejection, got %v", err)
	}
	if _, err := runCmdAllowError(t, dir, "--read-only=false", "chats", "archive", "c_alice"); err == nil {
		t.Fatal("missing session must fail")
	}
	// A stale daemon socket must not trigger a competing direct connection.
	if err := os.WriteFile(filepath.Join(dir, "gmcli.sock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmdAllowError(t, dir, "--read-only=false", "chats", "archive", "c_alice"); err == nil || !strings.Contains(err.Error(), "daemon") {
		t.Fatalf("expected daemon connection failure, got %v", err)
	}
}
