package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/johnlindquist/gmkit/internal/gm"
	"github.com/johnlindquist/gmkit/internal/output"
	"github.com/johnlindquist/gmkit/internal/rpc"
	"github.com/johnlindquist/gmkit/internal/store"
	gmsync "github.com/johnlindquist/gmkit/internal/sync"
)

const syncHeartbeatInterval = 5 * time.Minute

func syncCmd() *cobra.Command {
	var follow bool
	o := gmsync.ScanOptions{}
	c := &cobra.Command{Use: "sync", Short: "Scan phone folders with explicit coverage; optionally import recent messages", RunE: func(cmd *cobra.Command, args []string) error {
		r, e := scanPhone(o)
		readCoverage = &r.Coverage
		if flags.jsonOut {
			if out := output.JSON(os.Stdout, r.Coverage); out != nil {
				return out
			}
		} else {
			if out := reportRead(); out != nil {
				return out
			}
		}
		if e != nil {
			return e
		}
		if !follow {
			return nil
		}
		layout, e := resolveLayout()
		if e != nil {
			return e
		}
		if _, e = os.Stat(layout.Socket); e == nil {
			return fmt.Errorf("daemon owns this session; use its event subscription instead of sync --follow")
		}
		ctx, cancel := signalContext(cmd.Context())
		defer cancel()
		st, e := store.Open(ctx, layout.Database)
		if e != nil {
			return e
		}
		defer st.Close()
		client, e := gm.Open(layout, newLogger())
		if e != nil {
			return e
		}
		pump := gmsync.New(st, newLogger())
		client.Subscribe(pump.Handle)
		if e = client.Connect(); e != nil {
			return e
		}
		defer client.Disconnect()
		fmt.Fprintln(os.Stderr, "Streaming phone events; connection activity does not establish folder coverage.")
		heartbeat := time.NewTicker(syncHeartbeatInterval)
		defer heartbeat.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case e := <-pump.Fatal():
				return e
			case <-heartbeat.C:
				if e = st.TouchSync(ctx); e != nil {
					return e
				}
			}
		}
	}}
	// SPEC-AMBIGUITY: (2026-09-10 User asked for "okay do this cutover" but this could mean
	// 1. Sync always downloads recent messages for every thread.
	// 2. Sync enumerates folders by default, with message sampling explicitly requested.
	// Choice defaulted to: 2 because complete folder coverage should be fast and independent of history depth.
	c.Flags().BoolVar(&o.Contacts, "contacts", true, "also refresh contacts")
	c.Flags().StringVar(&o.Folder, "folder", "all", "inbox, archive, or all")
	c.Flags().IntVar(&o.PageSize, "page-size", 25, "phone page size, 1..100")
	c.Flags().IntVar(&o.MaxPages, "max-pages", 1000, "maximum pages per folder; exhaustion is an error")
	c.Flags().IntVar(&o.Messages, "messages-per-thread", 0, "recent records to request per thread; 0 scans folders only")
	c.Flags().BoolVar(&follow, "follow", false, "stream phone events after the scan")
	return c
}

// Startup and explicit refresh use the same scanner and propagate failures.
func runInitialImport(ctx context.Context, server *rpc.Server, logger zerolog.Logger) (gmsync.ScanResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	r, e := server.Scan(ctx, gmsync.ScanOptions{Folder: "all", PageSize: 25, MaxPages: 1000, Contacts: true})
	if e != nil {
		logger.Error().Err(e).Msg("Folder scan incomplete")
	} else {
		logger.Info().Int("conversations", len(r.Conversations)).Msg("Folder cursors exhausted; message history not requested")
	}
	return r, e
}
