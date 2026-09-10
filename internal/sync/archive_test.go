package sync

import (
	"context"
	"github.com/johnlindquist/gmkit/internal/store"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
	"path/filepath"
	"testing"
)

func TestArchiveStatusSurvivesSync(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	pump := New(st, zerolog.Nop())
	for _, status := range []gmproto.ConversationStatus{gmproto.ConversationStatus_ARCHIVED, gmproto.ConversationStatus_KEEP_ARCHIVED, gmproto.ConversationStatus_ACTIVE} {
		pump.Handle(&gmproto.Conversation{ConversationID: "test", Status: status})
		got, err := st.GetConversation(ctx, "test")
		if err != nil {
			t.Fatal(err)
		}
		want := status != gmproto.ConversationStatus_ACTIVE
		if got.Archived != want {
			t.Fatalf("status %s: archived=%v, want %v", status, got.Archived, want)
		}
	}
}
