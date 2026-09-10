package gm

import (
	"context"
	"errors"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"testing"
)

type scanFake struct {
	pages []*gmproto.ListConversationsResponse
	calls int
	err   error
}

func (f *scanFake) ListConversationsWithCursor(n int, folder gmproto.ListConversationsRequest_Folder, c *gmproto.Cursor) (*gmproto.ListConversationsResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	r := f.pages[f.calls]
	f.calls++
	return r, nil
}
func TestFolderScanCoverage(t *testing.T) {
	row := func(id string) *gmproto.Conversation {
		return &gmproto.Conversation{ConversationID: id, Status: gmproto.ConversationStatus_ACTIVE}
	}
	cursor := &gmproto.Cursor{LastItemID: "a", LastItemTimestamp: 123}
	for _, tt := range []struct {
		name     string
		pages    []*gmproto.ListConversationsResponse
		complete bool
		count    int
	}{
		{"multiple pages", []*gmproto.ListConversationsResponse{{Conversations: []*gmproto.Conversation{row("a")}, Cursor: cursor}, {Conversations: []*gmproto.Conversation{row("b")}}}, true, 2},
		{"empty folder", []*gmproto.ListConversationsResponse{{}}, true, 0},
		{"repeated cursor", []*gmproto.ListConversationsResponse{{Conversations: []*gmproto.Conversation{row("a")}, Cursor: cursor}, {Conversations: []*gmproto.Conversation{row("a")}, Cursor: cursor}}, false, 1},
		{"opaque cursor", []*gmproto.ListConversationsResponse{{CursorBytes: []byte{1}}}, false, 0},
		{"wrong folder", []*gmproto.ListConversationsResponse{{Conversations: []*gmproto.Conversation{{ConversationID: "a", Status: gmproto.ConversationStatus_ARCHIVED}}}}, false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, e := scanFolder(context.Background(), &scanFake{pages: tt.pages}, "inbox", 25, 100)
			if r.Complete != tt.complete || len(r.Conversations) != tt.count || (e == nil) != tt.complete {
				t.Fatalf("result=%+v error=%v", r, e)
			}
		})
	}
	r, e := scanFolder(context.Background(), &scanFake{err: errors.New("offline")}, "inbox", 25, 100)
	if e == nil || r.Complete {
		t.Fatal("failed scan claimed completeness")
	}
	r, e = scanFolder(context.Background(), &scanFake{pages: []*gmproto.ListConversationsResponse{{Cursor: cursor}}}, "inbox", 25, 1)
	if e == nil || r.Complete {
		t.Fatal("budget exhaustion claimed completeness")
	}
}
