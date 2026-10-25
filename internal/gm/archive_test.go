package gm

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

type fakeArchiveAPI struct {
	before, after     *gmproto.Conversation
	readErr, writeErr error
	success           bool
	writes, reads     int
	request           *gmproto.UpdateConversationRequest
}

func (f *fakeArchiveAPI) GetConversation(context.Context, string) (*gmproto.Conversation, error) {
	f.reads++
	if f.readErr != nil {
		return nil, f.readErr
	}
	if f.reads == 1 {
		return f.before, nil
	}
	return f.after, nil
}
func (f *fakeArchiveAPI) UpdateConversation(_ context.Context, r *gmproto.UpdateConversationRequest) (*gmproto.UpdateConversationResponse, error) {
	f.writes++
	f.request = r
	return &gmproto.UpdateConversationResponse{Success: f.success}, f.writeErr
}
func TestArchiveRequiresRemoteConfirmation(t *testing.T) {
	active := func(id string) *gmproto.Conversation {
		return &gmproto.Conversation{ConversationID: id, Status: gmproto.ConversationStatus_ACTIVE}
	}
	archived := &gmproto.Conversation{ConversationID: "c", Status: gmproto.ConversationStatus_ARCHIVED}
	cases := []struct {
		name    string
		api     fakeArchiveAPI
		wantErr bool
		writes  int
	}{
		{"confirmed", fakeArchiveAPI{before: active("c"), after: archived, success: true}, false, 1},
		{"already archived", fakeArchiveAPI{before: archived}, false, 0},
		{"kept archived", fakeArchiveAPI{before: &gmproto.Conversation{ConversationID: "c", Status: gmproto.ConversationStatus_KEEP_ARCHIVED}}, false, 0},
		{"lying acknowledgment", fakeArchiveAPI{before: active("c"), after: active("c"), success: true}, true, 1},
		{"wrong readback", fakeArchiveAPI{before: active("c"), after: &gmproto.Conversation{ConversationID: "other", Status: gmproto.ConversationStatus_ARCHIVED}, success: true}, true, 1},
		{"not found", fakeArchiveAPI{}, true, 0},
		{"wrong target", fakeArchiveAPI{before: active("other")}, true, 0},
		{"blocked", fakeArchiveAPI{before: &gmproto.Conversation{ConversationID: "c", Status: gmproto.ConversationStatus_BLOCKED_FOLDER}}, true, 0},
		{"read error", fakeArchiveAPI{readErr: errors.New("offline")}, true, 0},
		{"rejected", fakeArchiveAPI{before: active("c")}, true, 1},
		{"write error", fakeArchiveAPI{before: active("c"), writeErr: errors.New("offline")}, true, 1},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := archiveConversation(context.Background(), &tt.api, "c")
			if (err != nil) != tt.wantErr {
				t.Fatalf("got %v, %v", got, err)
			}
			if tt.api.writes != tt.writes {
				t.Fatalf("writes=%d want %d", tt.api.writes, tt.writes)
			}
			if tt.api.request != nil && (tt.api.request.GetConversationID() != "c" || tt.api.request.GetAction() != 4 || tt.api.request.Data != nil) {
				t.Fatalf("wrong wire request: %v", tt.api.request)
			}
		})
	}
}
func TestArchiveCanceledBeforeWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api := &fakeArchiveAPI{}
	if _, err := archiveConversation(ctx, api, "c"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if api.reads != 0 || api.writes != 0 {
		t.Fatal("canceled operation touched phone")
	}
}
func TestReadinessUsesPhoneSettings(t *testing.T) {
	c := &Client{settings: &gmproto.Settings{}}
	if err := c.WaitForReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	c = &Client{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := c.WaitForReady(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

type stalledArchiveAPI struct {
	fakeArchiveAPI
	entered, release chan struct{}
}

func (f *stalledArchiveAPI) GetConversation(context.Context, string) (*gmproto.Conversation, error) {
	close(f.entered)
	<-f.release
	return &gmproto.Conversation{ConversationID: "c", Status: gmproto.ConversationStatus_ACTIVE}, nil
}
func TestArchiveTimeoutCannotStartLateWrite(t *testing.T) {
	api := &stalledArchiveAPI{entered: make(chan struct{}), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := archiveConversation(ctx, api, "c"); done <- err }()
	<-api.entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("archive ignored cancellation")
	}
	close(api.release)
}
func TestReadinessAcceptsSettingsAfterWaitStarts(t *testing.T) {
	c := &Client{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.WaitForReady(ctx) }()
	c.dispatch(&gmproto.Settings{})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type delayedArchiveAPI struct{ fakeArchiveAPI }

func (f *delayedArchiveAPI) GetConversation(_ context.Context, id string) (*gmproto.Conversation, error) {
	f.reads++
	status := gmproto.ConversationStatus_ACTIVE
	if f.reads >= 4 {
		status = gmproto.ConversationStatus_ARCHIVED
	}
	return &gmproto.Conversation{ConversationID: id, Status: status}, nil
}
func TestArchiveWaitsForPropagationWithoutResending(t *testing.T) {
	f := &delayedArchiveAPI{fakeArchiveAPI: fakeArchiveAPI{success: true}}
	r, e := archiveConversation(context.Background(), f, "c")
	if e != nil || r.GetStatus() != gmproto.ConversationStatus_ARCHIVED || f.writes != 1 {
		t.Fatalf("r=%v err=%v writes=%d", r, e, f.writes)
	}
}

func TestFolderAndIndividualArchiveMustAgree(t *testing.T) {
	for _, tc := range []struct {
		status       gmproto.ConversationStatus
		in, ar, want bool
	}{
		{gmproto.ConversationStatus_ARCHIVED, false, true, true},
		{gmproto.ConversationStatus_ACTIVE, false, true, false},
		{gmproto.ConversationStatus_ARCHIVED, true, true, false},
		{gmproto.ConversationStatus_ARCHIVED, false, false, false},
	} {
		r := checkArchive("c", tc.status, tc.in, tc.ar)
		if r.Verified != tc.want {
			t.Fatalf("%+v", r)
		}
	}
}
