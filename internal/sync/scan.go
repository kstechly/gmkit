package sync

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/johnlindquist/gmkit/internal/coverage"
	"github.com/johnlindquist/gmkit/internal/gm"
	"github.com/johnlindquist/gmkit/internal/store"
)

// ScanOptions keeps enumeration limits separate from recent-message sampling.
type ScanOptions struct {
	Contacts bool   `json:"contacts"`
	Folder   string `json:"folder"`
	PageSize int    `json:"page_size"`
	MaxPages int    `json:"max_pages"`
	Messages int    `json:"messages_per_thread"`
}

// Validate rejects invalid budgets before opening a phone connection.
func (o ScanOptions) Validate() error {
	if o.Folder != "all" {
		if _, e := gm.FolderCode(o.Folder); e != nil {
			return e
		}
	}
	if o.PageSize < 1 || o.PageSize > 100 || o.MaxPages < 1 {
		return fmt.Errorf("page-size must be 1..100 and max-pages must be positive")
	}
	if o.Messages < 0 {
		return fmt.Errorf("messages-per-thread must not be negative")
	}
	return nil
}

// ScanResult contains only conversations observed by the live traversal.
type ScanResult struct {
	Coverage      coverage.Read        `json:"coverage"`
	Conversations []store.Conversation `json:"data"`
}

// Scan imports only rows actually observed during this traversal. Old cache rows
// are retained as history, never used to fill holes in a live response.
func Scan(ctx context.Context, c *gm.Client, st *store.Store, o ScanOptions) (out ScanResult, err error) {
	out = ScanResult{Coverage: coverage.Read{Source: "live", Folder: o.Folder, FolderScans: []coverage.Folder{}, MessageHistory: coverage.Messages{Scope: "scanned_threads", RequestedPerThread: o.Messages, StopReason: "not_requested"}}, Conversations: []store.Conversation{}}
	if e := o.Validate(); e != nil {
		return out, e
	}
	folders := []string{o.Folder}
	if o.Folder == "all" {
		folders = []string{"inbox", "archive"}
	}
	seen := map[string]bool{}
	for _, folder := range folders {
		scan, e := c.ScanFolder(ctx, folder, o.PageSize, o.MaxPages)
		ids := []string{}
		for _, conv := range scan.Conversations {
			row, _ := ConversationRow(conv, "gm")
			if save := st.UpsertConversation(ctx, row); save != nil {
				e = save
				scan.Complete = false
				scan.StopReason = "local_save_failed"
				break
			}
			ids = append(ids, row.ID)
			if !seen[row.ID] {
				out.Conversations = append(out.Conversations, row)
				seen[row.ID] = true
			}
		}
		out.Coverage.FolderScans = append(out.Coverage.FolderScans, scan.Folder)
		if save := st.SaveFolderScan(ctx, scan.Folder, ids); save != nil {
			return out, save
		}
		if e != nil {
			return out, e
		}
	}
	if o.Contacts {
		contacts, e := c.ReadContacts(ctx)
		if e != nil {
			return out, e
		}
		pump := New(st, zerolog.Nop())
		out.Coverage.ContactsImported, e = pump.ImportContacts(ctx, contacts)
		if e != nil {
			return out, e
		}
	}
	if o.Messages > 0 {
		out.Coverage.MessageHistory.StopReason = "recent_sample_only"
		for _, row := range out.Conversations {
			msgs, e := c.RecentMessages(ctx, row.ID, o.Messages)
			if e != nil {
				out.Coverage.MessageHistory.StopReason = "fetch_failed"
				return out, fmt.Errorf("messages for %s: %w", row.ID, e)
			}
			for _, m := range msgs {
				mr, ok := MessageRow(m, "gm")
				if !ok {
					return out, fmt.Errorf("invalid message record")
				}
				if e = st.UpsertMessage(ctx, mr); e != nil {
					return out, e
				}
				out.Coverage.MessageHistory.RecordsImported++
			}
			out.Coverage.MessageHistory.ThreadsFetched++
		}
	}
	return out, nil
}

// ChatRead contains fresh response records for a single thread.
type ChatRead struct {
	Coverage     coverage.Read      `json:"coverage"`
	Conversation store.Conversation `json:"conversation"`
	Messages     []store.Message    `json:"messages"`
}

// ReadChat imports a recent sample without filling missing records from the cache.
func ReadChat(ctx context.Context, c *gm.Client, st *store.Store, id string, limit int) (r ChatRead, err error) {
	r = ChatRead{Coverage: coverage.Read{Source: "live", Folder: "single_thread", FolderScans: []coverage.Folder{}, MessageHistory: coverage.Messages{Scope: id, RequestedPerThread: limit, StopReason: "recent_sample_only"}}, Messages: []store.Message{}}
	if limit < 1 {
		return r, fmt.Errorf("limit must be positive")
	}
	conv, e := c.ReadConversation(ctx, id)
	if e != nil {
		return r, e
	}
	r.Conversation, _ = ConversationRow(conv, "gm")
	if e = st.UpsertConversation(ctx, r.Conversation); e != nil {
		return r, e
	}
	msgs, e := c.RecentMessages(ctx, id, limit)
	if e != nil {
		return r, e
	}
	for _, m := range msgs {
		row, ok := MessageRow(m, "gm")
		if !ok {
			return r, fmt.Errorf("invalid message record")
		}
		if e = st.UpsertMessage(ctx, row); e != nil {
			return r, e
		}
		r.Coverage.MessageHistory.RecordsImported++
		if row.ConversationID == id {
			r.Messages = append(r.Messages, row)
		}
	}
	r.Coverage.MessageHistory.ThreadsFetched = 1
	return r, nil
}
