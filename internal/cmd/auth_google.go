package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/johnlindquist/gmkit/internal/rpc"
)

func authGoogleCmd() *cobra.Command {
	var cookieFile string
	c := &cobra.Command{
		Use:   "google",
		Short: "Pair with Google Messages as a Chrome profile's Google account (emoji confirmation)",
		Long: "Ask the running daemon (`gmcli serve`) to pair as the Google account signed in to " +
			"the Chrome profile whose cookie database is --cookie-file (default: the current " +
			"pairing's profile). session.json records that file, never a cookie: every request " +
			"reads the profile's current cookies. Prints the emoji to tap in Google Messages on " +
			"the phone and waits for the pairing; the daemon then restarts to load it.\n\n" +
			"With --json, each pairing event is printed to stdout as one JSON line " +
			"({\"type\":\"pair.emoji\",\"data\":{\"emoji\":...}}, then pair.success or pair.error).",
		RunE: func(cmd *cobra.Command, args []string) error {
			layout, err := resolveLayout()
			if err != nil {
				return err
			}
			ctx, cancel := signalContext(cmd.Context())
			defer cancel()
			client, err := rpc.Dial(layout.Socket)
			if err != nil {
				return err
			}
			defer client.Close()
			if err := client.Subscribe(ctx); err != nil {
				return fmt.Errorf("subscribe to daemon events: %w", err)
			}
			var started struct {
				Started    bool   `json:"started"`
				Reason     string `json:"reason"`
				CookieFile string `json:"cookie_file"`
			}
			if err := client.Call(ctx, "auth.pair.google", map[string]any{"cookie_file": cookieFile}, &started); err != nil {
				return fmt.Errorf("auth.pair.google: %w", err)
			}
			if !started.Started {
				return fmt.Errorf("pairing not started: %s", started.Reason)
			}
			if !flags.jsonOut {
				fmt.Fprintf(os.Stderr, "Pairing as the Google account in %s...\n", started.CookieFile)
			}
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case ev, ok := <-client.Events():
					if !ok {
						return fmt.Errorf("daemon closed the connection before pairing finished")
					}
					switch ev.Type {
					case rpc.EventPairEmoji, rpc.EventPairSuccess, rpc.EventPairError:
					default:
						continue
					}
					data, _ := ev.Data.(map[string]any)
					if flags.jsonOut {
						line, _ := json.Marshal(ev)
						fmt.Println(string(line))
					}
					switch ev.Type {
					case rpc.EventPairEmoji:
						if !flags.jsonOut {
							fmt.Fprintf(os.Stderr, "On your phone, tap this emoji in Google Messages: %v\n", data["emoji"])
							fmt.Fprintln(os.Stderr, "Waiting for confirmation... (Ctrl-C to stop waiting)")
						}
					case rpc.EventPairSuccess:
						if !flags.jsonOut {
							fmt.Fprintf(os.Stderr, "Paired. phone_id=%v; the daemon restarts to load the new pairing.\n", data["phone_id"])
						}
						return nil
					case rpc.EventPairError:
						return fmt.Errorf("pairing failed: %v", data["error"])
					}
				}
			}
		},
	}
	c.Flags().StringVar(&cookieFile, "cookie-file", "", "Chrome profile Cookies database of the account to pair as (default: the current pairing's)")
	return c
}
