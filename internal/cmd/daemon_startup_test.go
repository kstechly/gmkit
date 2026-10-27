package cmd

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnlindquist/gmkit/internal/paths"
)

func TestApprovalAndMCPLiveClientsRequireConfiguredDaemon(t *testing.T) {
	previous := flags
	t.Cleanup(func() { flags = previous })
	for _, stale := range []bool{false, true} {
		dir := t.TempDir()
		flags = globalFlags{storeDir: dir, readOnly: true}
		if stale {
			if err := os.WriteFile(filepath.Join(dir, "gmcli.sock"), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		for _, dial := range []func() error{
			func() error {
				client, err := dialDaemon()
				if client != nil {
					client.Close()
				}
				return err
			},
			func() error {
				layout, _ := paths.Resolve(dir)
				client, err := dialServe(layout)
				if client != nil {
					client.Close()
				}
				return err
			},
		} {
			var socketErr *net.OpError
			if err := dial(); !errors.As(err, &socketErr) || socketErr.Net != "unix" {
				t.Fatalf("expected configured socket error: %v", err)
			}
		}
		if _, err := os.Stat(filepath.Join(dir, "daemon.log")); !os.IsNotExist(err) {
			t.Fatalf("a client created a daemon log: %v", err)
		}
	}
}
