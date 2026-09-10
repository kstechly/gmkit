package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/johnlindquist/gmkit/internal/gm"
	"github.com/johnlindquist/gmkit/internal/output"
	"github.com/johnlindquist/gmkit/internal/rpc"
	"github.com/johnlindquist/gmkit/internal/store"
	gmsync "github.com/johnlindquist/gmkit/internal/sync"
)

func chatsArchiveCmd() *cobra.Command {
	var verifyFolders bool
	c := &cobra.Command{
		Use:   "archive <conversation-id>",
		Short: "Archive a phone conversation and verify its status (requires --read-only=false)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireWritable(); err != nil {
				return err
			}
			if chatSource != "live" {
				return fmt.Errorf("archive operates on the phone; --source must be live")
			}
			layout, err := resolveLayout()
			if err != nil {
				return err
			}
			var row store.Conversation
			if _, err = os.Stat(layout.Socket); err == nil {
				// A present socket belongs to the session owner. Failure to connect or an
				// old daemon must surface, never fall back to a competing phone session.
				client, err := rpc.Dial(layout.Socket)
				if err != nil {
					return err
				}
				defer client.Close()
				ctx, cancel := context.WithTimeout(cmd.Context(), 2*readyTimeout)
				defer cancel()
				err = client.Call(ctx, "chats.archive", map[string]string{"conversation_id": args[0]}, &row)
				if err != nil {
					return fmt.Errorf("archive via daemon: %w", err)
				}
			} else if !os.IsNotExist(err) {
				return err
			} else {
				err = runWithConnectedClient(func(ctx context.Context, c *gm.Client, st *store.Store) error {
					conv, err := c.ArchiveConversation(ctx, args[0])
					if err != nil {
						return err
					}
					row, _ = gmsync.ConversationRow(conv, "gm")
					if err = st.UpsertConversation(ctx, row); err != nil {
						return fmt.Errorf("archived on phone but local save failed: %w", err)
					}
					row, err = st.GetConversation(ctx, args[0])
					return err
				})
				if err != nil {
					return err
				}
			}
			var verification *gm.ArchiveVerification
			var checkErr error
			if verifyFolders {
				r, e := verifyArchives([]string{row.ID})
				verification = &r
				checkErr = e
			}
			if flags.jsonOut {
				if e := output.JSON(os.Stdout, struct {
					Conversation store.Conversation      `json:"conversation"`
					Scope        string                  `json:"verification_scope"`
					Folders      *gm.ArchiveVerification `json:"folder_verification,omitempty"`
				}{row, "individual_remote_readback", verification}); e != nil {
					return e
				}
				return checkErr
			}
			if checkErr != nil {
				return checkErr
			}
			fmt.Fprintf(os.Stdout, "Archived %s (conversation_id %s; individual remote readback; use verify-archive for folder agreement).\n", row.DisplayName(), row.ID)
			return nil
		},
	}
	c.Flags().BoolVar(&verifyFolders, "verify-folders", false, "also compare status against complete inbox and archive scans")
	return c
}
