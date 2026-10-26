package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/johnlindquist/gmkit/internal/history"
	"github.com/johnlindquist/gmkit/internal/output"
)

func historyCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "history",
		Short: "Best-effort message history backfill",
		Long: "Fetch older messages for a conversation through the paired phone. " +
			"Like wacli, this is best-effort: Google may return partial history, " +
			"and the phone must be online.",
	}
	c.AddCommand(historyBackfillCmd())
	c.AddCommand(historyLookupCmd())
	return c
}

func historyBackfillCmd() *cobra.Command {
	var chat string
	var requests int
	var count int64
	c := &cobra.Command{
		Use:   "backfill",
		Short: "Fetch older messages for one conversation",
		Long: "Fetch older messages for one conversation. --requests limits how many " +
			"FetchMessages calls gmcli makes, and --count limits how many message " +
			"records each call asks the phone for. JSON output separates protocol " +
			"records processed from messages added to the target conversation.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if chat == "" {
				return fmt.Errorf("--chat is required")
			}
			res, err := runHistoryBackfill(chat, requests, count)
			if err != nil {
				return err
			}
			return printBackfillResult(res)
		},
	}
	c.Flags().StringVar(&chat, "chat", "", "conversation_id to backfill")
	c.Flags().IntVar(&requests, "requests", 10, "max FetchMessages calls to make for the target conversation")
	c.Flags().Int64Var(&count, "count", 50, "max message records to request per FetchMessages call")
	return c
}

func historyLookupCmd() *cobra.Command {
	var phone string
	var requests int
	var count int64
	c := &cobra.Command{
		Use:   "lookup",
		Short: "Find a conversation by phone number and backfill its messages",
		RunE: func(cmd *cobra.Command, args []string) error {
			if phone == "" {
				return fmt.Errorf("--phone is required")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			var result struct {
				ConversationID string                 `json:"conversation_id"`
				Name           string                 `json:"name"`
				Backfill       history.BackfillResult `json:"backfill"`
			}
			if err := callDaemon(ctx, "history.lookup", map[string]any{"phone": phone, "requests": requests, "count": count}, &result); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Found conversation: %s (ID: %s)\n", result.Name, result.ConversationID)
			return printBackfillResult(result.Backfill)
		},
	}
	c.Flags().StringVar(&phone, "phone", "", "phone number in E.164 format (e.g. +13855551234)")
	c.Flags().IntVar(&requests, "requests", 20, "max FetchMessages calls")
	c.Flags().Int64Var(&count, "count", 50, "max message records per call")
	return c
}

func printBackfillResult(res history.BackfillResult) error {
	if flags.jsonOut {
		return output.JSON(os.Stdout, res)
	}
	fmt.Fprintf(os.Stderr, "Backfill for %s: fetched %d message record(s), chat messages %d -> %d (+%d), using %d request(s)\n",
		res.ConversationID, res.FetchedMessages, res.MessagesBefore, res.MessagesAfter, res.MessagesAddedForChat, res.Requests)
	fmt.Fprintf(os.Stderr, "source=%s traversal_complete=%t history_complete=%t stop=%s (older-prefix traversal does not prove gap-free whole history)\n", res.Source, res.TraversalComplete, res.HistoryComplete, res.StopReason)
	return nil
}

func runHistoryBackfill(chat string, requests int, count int64) (history.BackfillResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var result history.BackfillResult
	err := callDaemon(ctx, "history.backfill", map[string]any{"conversation_id": chat, "requests": requests, "count": count}, &result)
	return result, err
}
