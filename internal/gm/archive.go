package gm

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/mautrix-gmessages/pkg/libgm/gmproto"
)

// actionArchive is field 2 = 4 in Google's web client's archiveConversation
// request (observed 2026-09-10); the conversation ID is field 3. libgm v0.6.4
// omits this action from its enum. Setting UpdateConversationData.Status
// instead is acknowledged as successful but does not archive the thread.
const actionArchive gmproto.ConversationActionStatus = 4

type archiveAPI interface {
	GetConversation(context.Context, string) (*gmproto.Conversation, error)
	UpdateConversation(context.Context, *gmproto.UpdateConversationRequest) (*gmproto.UpdateConversationResponse, error)
}

// ArchiveConversation archives a phone conversation and verifies its status
// by reading it back. A successful write acknowledgment alone is insufficient.
func (c *Client) ArchiveConversation(ctx context.Context, id string) (*gmproto.Conversation, error) {
	if err := c.WaitForReady(ctx); err != nil {
		return nil, err
	}
	return archiveConversation(ctx, c.libgm, id)
}

func archiveConversation(ctx context.Context, api archiveAPI, id string) (*gmproto.Conversation, error) {
	if id == "" {
		return nil, fmt.Errorf("conversation id is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return archiveAndReadBack(ctx, api, id)
}

func archiveAndReadBack(ctx context.Context, api archiveAPI, id string) (*gmproto.Conversation, error) {
	conv, err := api.GetConversation(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("read conversation: %w", err)
	}
	if conv.GetConversationID() != id {
		return nil, fmt.Errorf("conversation %s not found in phone response", id)
	}
	switch conv.GetStatus() {
	case gmproto.ConversationStatus_ARCHIVED, gmproto.ConversationStatus_KEEP_ARCHIVED:
		return conv, nil
	case gmproto.ConversationStatus_ACTIVE:
	default:
		return nil, fmt.Errorf("cannot archive conversation %s with status %s", id, conv.GetStatus())
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resp, err := api.UpdateConversation(ctx, &gmproto.UpdateConversationRequest{ConversationID: id, Action: actionArchive})
	if err != nil {
		return nil, fmt.Errorf("archive request: %w", err)
	}
	if !resp.GetSuccess() {
		return nil, fmt.Errorf("phone rejected archive of %s", id)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Acknowledgment and visible state can arrive separately. Poll reads only;
	// never resend a mutation merely because its result is not visible yet.
	verifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		conv, err = api.GetConversation(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("archive acknowledged but readback failed: %w", err)
		}
		if conv.GetConversationID() != id {
			return nil, fmt.Errorf("archive acknowledged but readback returned wrong id %q", conv.GetConversationID())
		}
		if conv.GetStatus() == gmproto.ConversationStatus_ARCHIVED || conv.GetStatus() == gmproto.ConversationStatus_KEEP_ARCHIVED {
			return conv, nil
		}
		select {
		case <-verifyCtx.Done():
			return nil, fmt.Errorf("archive acknowledged but not verified for %s: status=%s: %w", id, conv.GetStatus(), verifyCtx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}
