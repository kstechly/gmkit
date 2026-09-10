package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/johnlindquist/gmkit/internal/gm"
	"github.com/johnlindquist/gmkit/internal/output"
	"github.com/johnlindquist/gmkit/internal/rpc"
	"github.com/johnlindquist/gmkit/internal/store"
)

func chatsVerifyCmd() *cobra.Command {
	return &cobra.Command{Use: "verify-archive <conversation-id>...", Short: "Read-only check of individual status against paginated inbox and archive", Args: cobra.MinimumNArgs(1), RunE: func(cmd *cobra.Command, ids []string) error {
		r, e := verifyArchives(ids)
		if flags.jsonOut {
			if out := output.JSON(os.Stdout, r); out != nil {
				return out
			}
		} else {
			for _, c := range r.Checks {
				fmt.Printf("%s: status=%s in_inbox=%t in_archive=%t verified=%t\n", c.ID, c.Status, c.InInbox, c.InArchive, c.Verified)
			}
			fmt.Printf("source=live checked_at=%s verified=%t\n", r.CheckedAt.Format(time.RFC3339), r.Verified)
		}
		return e
	}}
}
func verifyArchives(ids []string) (r gm.ArchiveVerification, err error) {
	l, e := resolveLayout()
	if e != nil {
		return r, e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, e = os.Stat(l.Socket); e == nil {
		c, e := rpc.Dial(l.Socket)
		if e != nil {
			return r, e
		}
		defer c.Close()
		err = c.Call(ctx, "chats.verifyArchive", map[string]any{"conversation_ids": ids}, &r)
		var re *rpc.Error
		if errors.As(err, &re) && re.Data != nil {
			b, _ := json.Marshal(re.Data)
			_ = json.Unmarshal(b, &r)
		}
		return
	} else if !os.IsNotExist(e) {
		return r, e
	}
	err = runWithConnectedContext(ctx, func(ctx context.Context, c *gm.Client, _ *store.Store) error {
		var e error
		r, e = c.VerifyArchives(ctx, ids)
		return e
	})
	return
}
