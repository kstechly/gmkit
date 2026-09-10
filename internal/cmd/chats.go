package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	gmsync "github.com/johnlindquist/gmkit/internal/sync"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/johnlindquist/gmkit/internal/output"
	"github.com/johnlindquist/gmkit/internal/store"
)

func chatsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "chats",
		Short: "List and inspect conversations",
	}
	// SPEC-AMBIGUITY: (2026-09-10 User asked for "okay do this cutover" but this could mean
	// 1. Keep cached chat reads as the default.
	// 2. Default chat reads to the phone and require --source cached for offline reads.
	// Choice defaulted to: 2 because an unqualified inbox query should reflect phone state.
	c.PersistentFlags().StringVar(&chatSource, "source", "live", "live or cached; cached reads never imply current phone state")
	c.AddCommand(chatsListCmd())
	c.AddCommand(chatsFindCmd())
	c.AddCommand(chatsShowCmd())
	c.AddCommand(chatsArchiveCmd())
	return c
}

func chatsFindCmd() *cobra.Command {
	var limit int
	c := &cobra.Command{
		Use:   "find <person-or-number>",
		Short: "Find conversations by contact name, alias, group name, or number",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			var liveIDs map[string]bool
			if chatSource == "live" {
				r, e := scanPhone(gmsync.ScanOptions{Folder: "all", PageSize: 25, MaxPages: 1000})
				readCoverage = &r.Coverage
				if e != nil {
					return e
				}
				liveIDs = map[string]bool{}
				for _, c := range r.Conversations {
					liveIDs[c.ID] = true
				}
			} else if chatSource != "cached" {
				return fmt.Errorf("source must be live or cached")
			}
			convs, err := st.FindConversations(context.Background(), joinArgs(args), 100000)
			if err != nil {
				return err
			}
			if liveIDs != nil {
				filtered := []store.Conversation{}
				for _, c := range convs {
					if liveIDs[c.ID] {
						filtered = append(filtered, c)
					}
				}
				convs = filtered
			}
			convs = boundRead(convs, limit)
			if flags.jsonOut {
				return readJSON(convs)
			}
			if e := reportRead(); e != nil {
				return e
			}
			if len(convs) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "(no matching conversations)")
				return nil
			}
			rows := make([][]string, 0, len(convs))
			for _, c := range convs {
				kind := "1:1"
				if c.IsGroup {
					kind = "grp"
				}
				rows = append(rows, []string{
					output.FormatTime(c.LastMessageTimeMS),
					kind,
					truncate(c.DisplayName(), 40),
					c.ID,
				})
			}
			return output.Table(os.Stdout, []string{"last_msg", "kind", "name", "conv_id"}, rows)
		},
	}
	c.Flags().IntVar(&limit, "limit", 25, "max rows")
	return c
}

func chatsListCmd() *cobra.Command {
	var limit, pageSize, maxPages int
	var folder string
	var unreadOnly, pinned bool
	c := &cobra.Command{Use: "list", Short: "List a live folder or explicitly query the cache", RunE: func(cmd *cobra.Command, args []string) error {
		if limit < 1 {
			return fmt.Errorf("limit must be positive")
		}
		if folder != "inbox" && folder != "archive" && folder != "all" {
			return fmt.Errorf("folder must be inbox, archive, or all")
		}
		var convs []store.Conversation
		switch chatSource {
		case "live":
			r, e := scanPhone(gmsync.ScanOptions{Folder: folder, PageSize: pageSize, MaxPages: maxPages})
			readCoverage = &r.Coverage
			if e != nil {
				if flags.jsonOut {
					_ = readJSON(r.Conversations)
				} else {
					_ = reportRead()
				}
				return e
			}
			for _, row := range r.Conversations {
				if unreadOnly && !row.Unread || pinned && !row.Pinned {
					continue
				}
				convs = append(convs, row)
			}
			sort.Slice(convs, func(i, j int) bool { return convs[i].LastMessageTimeMS > convs[j].LastMessageTimeMS })
		case "cached":
			st, e := openStore()
			if e != nil {
				return e
			}
			defer st.Close()
			convs, e = st.ListConversations(context.Background(), store.ListConversationOpts{Limit: limit + 1, UnreadOnly: unreadOnly, Pinned: pinned, Folder: folder})
			if e != nil {
				return e
			}
			info, e := st.CachedCoverage(context.Background())
			if e != nil {
				return e
			}
			info.Folder = folder
			readCoverage = &info
		default:
			return fmt.Errorf("source must be live or cached")
		}
		if convs == nil {
			convs = []store.Conversation{}
		}
		convs = boundRead(convs, limit)
		if flags.jsonOut {
			return readJSON(convs)
		}
		if e := reportRead(); e != nil {
			return e
		}
		rows := [][]string{}
		for _, c := range convs {
			rows = append(rows, []string{output.FormatTime(c.LastMessageTimeMS), boolMark(c.Archived, "A"), boolMark(c.Unread, "*"), truncate(c.DisplayName(), 40), participantSummary(c.ParticipantsJSON), c.ID})
		}
		return output.Table(cmd.OutOrStdout(), []string{"last_msg", "archived", "unread", "name", "participants", "conv_id"}, rows)
	}}
	c.Flags().IntVar(&limit, "limit", 50, "maximum output rows (does not limit the folder scan)")
	c.Flags().StringVar(&folder, "folder", "inbox", "inbox, archive, or all")
	c.Flags().IntVar(&pageSize, "page-size", 25, "phone page size, 1..100")
	c.Flags().IntVar(&maxPages, "max-pages", 1000, "request budget; exhausting it is an incomplete scan error")
	c.Flags().BoolVar(&unreadOnly, "unread-only", false, "only unread threads")
	c.Flags().BoolVar(&pinned, "pinned", false, "only pinned threads")
	return c
}

func chatsShowCmd() *cobra.Command {
	var limit int
	c := &cobra.Command{
		Use:   "show <conversation-id>",
		Short: "Show a conversation header and its most recent messages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			if err := refreshChat(args[0], limit); err != nil {
				return err
			}
			ctx := context.Background()
			conv, err := st.GetConversation(ctx, args[0])
			if err != nil {
				if errors.Is(err, store.ErrNotFound) || err.Error() == "sql: no rows in result set" {
					return fmt.Errorf("no conversation with id %s", args[0])
				}
				return err
			}
			msgs, err := st.ListMessages(ctx, store.ListMessageOpts{
				ConversationID: args[0],
				Limit:          limit,
				Order:          "desc",
			})
			if err != nil {
				return err
			}
			reverseMessages(msgs)
			if flags.jsonOut {
				return readJSON(struct {
					Conversation store.Conversation `json:"conversation"`
					Messages     []store.Message    `json:"messages"`
				}{conv, msgs})
			}
			if err := reportRead(); err != nil {
				return err
			}
			renderChatDetail(conv, msgs)
			return nil
		},
	}
	c.Flags().IntVar(&limit, "limit", 50, "max recent messages to display")
	return c
}

func reverseMessages(msgs []store.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}

func renderChatDetail(c store.Conversation, msgs []store.Message) {
	fmt.Printf("conversation_id: %s\n", c.ID)
	fmt.Printf("name:            %s\n", c.Name)
	fmt.Printf("group:           %v\n", c.IsGroup)
	fmt.Printf("unread:          %v\n", c.Unread)
	fmt.Printf("pinned:          %v\n", c.Pinned)
	fmt.Printf("archived:        %v\n", c.Archived)
	fmt.Printf("last_message:    %s\n", output.FormatTime(c.LastMessageTimeMS))
	fmt.Printf("participants:    %s\n", participantSummary(c.ParticipantsJSON))
	fmt.Println()
	if len(msgs) == 0 {
		fmt.Println("(no messages)")
		return
	}
	_ = renderMessages(msgs)
}

// boolMark returns mark when b is true, else empty.
func boolMark(b bool, mark string) string {
	if b {
		return mark
	}
	return ""
}

// participantSummary parses the participants_json blob and returns a comma
// separated list of names (preferring formatted_number when name is empty).
// Best-effort — malformed JSON returns "?".
func participantSummary(js string) string {
	if js == "" || js == "[]" {
		return ""
	}
	var parts []map[string]any
	if err := json.Unmarshal([]byte(js), &parts); err != nil {
		return "?"
	}
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		if v, _ := p["is_me"].(bool); v {
			continue
		}
		if name, _ := p["name"].(string); name != "" {
			names = append(names, name)
			continue
		}
		if n, _ := p["formatted_number"].(string); n != "" {
			names = append(names, n)
			continue
		}
		if n, _ := p["e164"].(string); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return ""
	}
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return truncate(out, 40)
}
