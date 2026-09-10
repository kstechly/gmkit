package cmd

import (
	"context"
	"fmt"
	"github.com/johnlindquist/gmkit/internal/coverage"
	"github.com/johnlindquist/gmkit/internal/gm"
	"github.com/johnlindquist/gmkit/internal/output"
	"github.com/johnlindquist/gmkit/internal/rpc"
	"github.com/johnlindquist/gmkit/internal/store"
	gmsync "github.com/johnlindquist/gmkit/internal/sync"
	"os"
	"time"
)

var readCoverage *coverage.Read
var readReported bool
var chatSource string

func cachedReadCoverage() (coverage.Read, error) {
	st, e := openStore()
	if e != nil {
		return coverage.Read{}, e
	}
	defer st.Close()
	return st.CachedCoverage(context.Background())
}
func readInfo() (coverage.Read, error) {
	if readCoverage != nil {
		return *readCoverage, nil
	}
	return cachedReadCoverage()
}
func readJSON(data any) error {
	c, e := readInfo()
	if e != nil {
		return e
	}
	return output.JSON(os.Stdout, coverage.Result[any]{Coverage: c, Data: data})
}
func reportRead() error {
	if readReported {
		return nil
	}
	readReported = true
	c, e := readInfo()
	if e != nil {
		return e
	}
	fmt.Fprintf(os.Stderr, "source=%s folder=%s query_truncated=%t message_history=%s\n", c.Source, c.Folder, c.Truncated, c.MessageHistory.StopReason)
	if len(c.FolderScans) == 0 {
		fmt.Fprintln(os.Stderr, "folder coverage=unknown (no recorded completed scan)")
	}
	for _, s := range c.FolderScans {
		fmt.Fprintf(os.Stderr, "%s: scanned_at=%s complete=%t count=%d pages=%d stop=%s\n", s.Folder, s.ScannedAt.Format(time.RFC3339), s.Complete, s.Count, s.Pages, s.StopReason)
	}
	return nil
}
func boundRead[T any](rows []T, limit int) []T {
	c, e := readInfo()
	if e == nil {
		c.Truncated = limit > 0 && len(rows) > limit
		readCoverage = &c
	}
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}
func scanPhone(o gmsync.ScanOptions) (r gmsync.ScanResult, err error) {
	r.Coverage = coverage.Read{Source: "live", Folder: o.Folder, FolderScans: []coverage.Folder{}, MessageHistory: coverage.Messages{Scope: "scanned_threads", StopReason: "not_requested"}}
	layout, e := resolveLayout()
	if e != nil {
		return r, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, e = os.Stat(layout.Socket); e == nil {
		c, e := rpc.Dial(layout.Socket)
		if e != nil {
			return r, e
		}
		defer c.Close()
		err = c.Call(ctx, "chats.scan", o, &r)
		return
	} else if !os.IsNotExist(e) {
		return r, e
	}
	err = runWithConnectedClient(func(_ context.Context, c *gm.Client, st *store.Store) error {
		var e error
		r, e = gmsync.Scan(ctx, c, st, o)
		return e
	})
	return
}
func refreshChat(id string, limit int) error {
	if chatSource == "cached" {
		return nil
	}
	if chatSource != "live" {
		return fmt.Errorf("source must be live or cached")
	}
	// SPEC-AMBIGUITY: (2026-09-10 User asked for "okay do this cutover" but this could mean
	// 1. Live reads fetch complete message history too.
	// 2. Live chat reads fetch the requested recent sample and report that scope.
	// Choice defaulted to: 2 because folder enumeration and historical message coverage are separate contracts.
	layout, e := resolveLayout()
	if e != nil {
		return e
	}
	var info coverage.Read
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, e = os.Stat(layout.Socket); e == nil {
		c, e := rpc.Dial(layout.Socket)
		if e != nil {
			return e
		}
		defer c.Close()
		if e = c.Call(ctx, "chats.read", map[string]any{"conversation_id": id, "limit": limit}, &info); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	} else {
		e = runWithConnectedClient(func(ctx context.Context, c *gm.Client, st *store.Store) error {
			var e error
			info, e = gmsync.ReadChat(ctx, c, st, id, limit)
			return e
		})
		if e != nil {
			return e
		}
	}
	readCoverage = &info
	return nil
}
