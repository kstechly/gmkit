package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/johnlindquist/gmkit/internal/output"
	"github.com/johnlindquist/gmkit/internal/store"
)

// daemonCallTimeout bounds one CLI request to the configured phone connection.
const daemonCallTimeout = time.Minute

func sendCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "send",
		Short: "Send messages and reactions (requires --read-only=false)",
	}
	c.AddCommand(sendTextCmd())
	c.AddCommand(sendReactCmd())
	return c
}

func sendTextCmd() *cobra.Command {
	var to, message, replyTo string
	c := &cobra.Command{
		Use:   "text",
		Short: "Send a text message into a conversation",
		Long: "Sends `--message` to the conversation identified by `--to` " +
			"(a conversation_id; find one with `gmcli chats list`). " +
			"Optionally `--reply-to <message_id>` to render the message as a " +
			"quoted reply. Requires `--read-only=false` to be passed at the " +
			"root since gmcli is read-only by default.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" || message == "" {
				return fmt.Errorf("--to and --message are required")
			}
			if err := requireWritable(); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), daemonCallTimeout)
			defer cancel()
			var approval store.Approval
			if err := callDaemon(ctx, "send.text", map[string]any{
				"conversation_id": to, "body": message, "reply_to_id": replyTo, "requested_by": "cli",
			}, &approval); err != nil {
				return err
			}
			if flags.jsonOut {
				return output.JSON(os.Stdout, approval)
			}
			if approval.Status == store.ApprovalPending {
				fmt.Fprintf(os.Stderr, "Queued for approval (approval_id %s); no message sent.\n", approval.ID)
			} else {
				fmt.Fprintf(os.Stderr, "Message status %s for %s (approval_id %s)\n", approval.Status, approval.ConversationID, approval.ID)
			}
			return nil
		},
	}
	c.Flags().StringVar(&to, "to", "", "conversation_id (find one via `gmcli chats list`)")
	c.Flags().StringVar(&message, "message", "", "message body")
	c.Flags().StringVar(&replyTo, "reply-to", "", "optional message_id to quote-reply to")
	return c
}

func sendReactCmd() *cobra.Command {
	var msgID, emoji string
	var remove, switchAct bool
	c := &cobra.Command{
		Use:   "react",
		Short: "Add, remove, or switch a reaction on a message",
		RunE: func(cmd *cobra.Command, args []string) error {
			if msgID == "" || emoji == "" {
				return fmt.Errorf("--message and --emoji are required")
			}
			if remove && switchAct {
				return fmt.Errorf("--remove and --switch are mutually exclusive")
			}
			if err := requireWritable(); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), daemonCallTimeout)
			defer cancel()
			var result map[string]any
			if err := callDaemon(ctx, "send.react", map[string]any{
				"message_id": msgID, "emoji": emoji, "remove": remove, "switch": switchAct,
			}, &result); err != nil {
				return err
			}
			if flags.jsonOut {
				return output.JSON(os.Stdout, result)
			}
			fmt.Fprintf(os.Stderr, "Reacted %s on %s\n", emoji, msgID)
			return nil
		},
	}
	c.Flags().StringVar(&msgID, "message", "", "target message_id")
	c.Flags().StringVar(&emoji, "emoji", "", "unicode emoji to react with")
	c.Flags().BoolVar(&remove, "remove", false, "remove the reaction instead of adding it")
	c.Flags().BoolVar(&switchAct, "switch", false, "switch an existing reaction to a new emoji")
	return c
}
