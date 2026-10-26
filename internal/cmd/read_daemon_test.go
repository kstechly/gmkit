package cmd

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	gmsync "github.com/johnlindquist/gmkit/internal/sync"
)

func TestLiveReadsRequireDaemon(t *testing.T) {
	previous := flags
	t.Cleanup(func() { flags = previous })
	for _, state := range []string{"absent", "stale"} {
		for name, read := range map[string]func() error{
			"thread": func() error { _, err := refreshChat("369", 1); return err },
			"folders": func() error {
				_, err := scanPhone(gmsync.ScanOptions{Folder: "all", PageSize: 25, MaxPages: 1000})
				return err
			},
			"verification": func() error { _, err := verifyArchives([]string{"369"}); return err },
		} {
			t.Run(name+" "+state, func(t *testing.T) {
				flags = globalFlags{storeDir: t.TempDir(), readOnly: true}
				if state == "stale" {
					if err := os.WriteFile(filepath.Join(flags.storeDir, "gmcli.sock"), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				var socketError *net.OpError
				if err := read(); !errors.As(err, &socketError) || socketError.Net != "unix" {
					t.Fatalf("expected unavailable daemon socket, got %v", err)
				}
			})
		}
	}
}
