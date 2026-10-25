package gm

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"

	"github.com/johnlindquist/gmkit/internal/coverage"
)

// FolderScan describes a bounded traversal, not an atomic snapshot of the phone.
// Complete means the protocol's cursor was exhausted without an error.
type FolderScan struct {
	coverage.Folder
	Conversations []*gmproto.Conversation `json:"-"`
}
type folderAPI interface {
	ListConversations(context.Context, *gmproto.ListConversationsRequest) (*gmproto.ListConversationsResponse, error)
}

// FolderCode validates the user-facing folder name before making a request.
func FolderCode(folder string) (gmproto.ListConversationsRequest_Folder, error) {
	switch folder {
	case "inbox":
		return gmproto.ListConversationsRequest_INBOX, nil
	case "archive":
		return gmproto.ListConversationsRequest_ARCHIVE, nil
	}
	return 0, fmt.Errorf("folder must be inbox or archive, got %q", folder)
}

// ScanFolder traverses one phone folder until its cursor ends or the budget fails.
func (c *Client) ScanFolder(ctx context.Context, folder string, size, pages int) (FolderScan, error) {
	if err := c.WaitForReady(ctx); err != nil {
		return FolderScan{Folder: coverage.Folder{Folder: folder, StopReason: err.Error()}}, err
	}
	return scanFolder(ctx, c.libgm, folder, size, pages)
}
func scanFolder(ctx context.Context, api folderAPI, folder string, size, maxPages int) (r FolderScan, err error) {
	r = FolderScan{Folder: coverage.Folder{Folder: folder, StartedAt: time.Now().UTC(), PageSize: size}, Conversations: []*gmproto.Conversation{}}
	defer func() {
		r.ScannedAt = time.Now().UTC()
		r.Count = len(r.Conversations)
		if err != nil {
			r.StopReason = err.Error()
		}
	}()
	code, err := FolderCode(folder)
	if err != nil {
		return r, err
	}
	if size < 1 || size > 100 || maxPages < 1 {
		return r, fmt.Errorf("page-size must be 1..100 and max-pages must be positive")
	}
	var cursor *gmproto.Cursor
	seenCursors := map[string]bool{}
	seenIDs := map[string]bool{}
	for r.Pages < maxPages {
		resp, e := phoneCall(ctx, func() (*gmproto.ListConversationsResponse, error) {
			return api.ListConversations(ctx, &gmproto.ListConversationsRequest{Count: int64(size), Folder: code, Cursor: cursor})
		})
		if e != nil {
			return r, e
		}
		r.Pages++
		for _, c := range resp.GetConversations() {
			if c.GetConversationID() == "" {
				return r, fmt.Errorf("folder page contains a conversation without an ID")
			}
			archived := c.GetStatus() == gmproto.ConversationStatus_ARCHIVED || c.GetStatus() == gmproto.ConversationStatus_KEEP_ARCHIVED
			if (folder == "archive" && !archived) || (folder == "inbox" && c.GetStatus() != gmproto.ConversationStatus_ACTIVE) {
				return r, fmt.Errorf("folder %s returned %s with status %s", folder, c.GetConversationID(), c.GetStatus())
			}
			if !seenIDs[c.GetConversationID()] {
				seenIDs[c.GetConversationID()] = true
				r.Conversations = append(r.Conversations, c)
			}
		}
		next := resp.GetCursor()
		if next == nil {
			if len(resp.GetCursorBytes()) != 0 {
				return r, fmt.Errorf("opaque continuation cursor without a decoded cursor; coverage unknown")
			}
			r.Complete = true
			r.StopReason = "end_of_cursor"
			return r, nil
		}
		key := next.String()
		if seenCursors[key] {
			return r, fmt.Errorf("repeated pagination cursor; scan incomplete")
		}
		seenCursors[key] = true
		cursor = next
	}
	return r, fmt.Errorf("max-pages reached before end of cursor")
}

// The transport has no context-aware request API. Bound the caller's wait;
// disconnecting the session releases any outstanding transport request.
func phoneCall[T any](ctx context.Context, f func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	type result struct {
		v T
		e error
	}
	ch := make(chan result, 1)
	go func() { v, e := f(); ch <- result{v, e} }()
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case r := <-ch:
		return r.v, r.e
	}
}

// RecentMessages requests a bounded recent sample; the protocol can include other threads.
func (c *Client) RecentMessages(ctx context.Context, id string, count int) ([]*libgm.WrappedMessage, error) {
	r, e := phoneCall(ctx, func() (*gmproto.ListMessagesResponse, error) {
		return c.libgm.FetchMessages(ctx, id, int64(count), nil)
	})
	if e != nil {
		return nil, e
	}
	out := make([]*libgm.WrappedMessage, 0, len(r.GetMessages()))
	for _, m := range r.GetMessages() {
		out = append(out, &libgm.WrappedMessage{Message: m, IsOld: true})
	}
	return out, nil
}

// ReadConversation checks that a fresh response belongs to the requested thread.
func (c *Client) ReadConversation(ctx context.Context, id string) (*gmproto.Conversation, error) {
	r, e := phoneCall(ctx, func() (*gmproto.Conversation, error) { return c.libgm.GetConversation(ctx, id) })
	if e == nil && r.GetConversationID() != id {
		return nil, fmt.Errorf("phone returned no matching conversation for %s", id)
	}
	return r, e
}

// ReadContacts fetches contacts with cancellation of the caller’s wait.
func (c *Client) ReadContacts(ctx context.Context) ([]*gmproto.Contact, error) {
	r, e := phoneCall(ctx, func() (*gmproto.ListContactsResponse, error) { return c.libgm.ListContacts(ctx) })
	if e != nil {
		return nil, e
	}
	return r.GetContacts(), nil
}
