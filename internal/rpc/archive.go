package rpc

import (
	"context"
	"encoding/json"
	"fmt"

	gmsync "github.com/johnlindquist/gmkit/internal/sync"
)

type archiveParams struct {
	ConversationID string `json:"conversation_id"`
}

func (s *Server) handleChatsArchive(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, err := decode[archiveParams](params)
	if err != nil {
		return nil, err
	}
	if p.ConversationID == "" {
		return nil, &Error{Code: CodeInvalidParams, Message: "conversation_id is required"}
	}
	if s.deps.SendMode == SendOff {
		return nil, &Error{Code: CodeSendsDisabled, Message: "daemon is read-only; restart with --read-only=false"}
	}
	if e := s.phoneRefusal(); e != nil {
		return nil, e
	}
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, s.deps.LiveTimeout)
	defer cancel()
	conv, archiveErr := s.deps.Client.ArchiveConversation(ctx, p.ConversationID)
	if archiveErr != nil {
		return nil, &Error{Code: CodeUnavailable, Message: archiveErr.Error()}
	}
	row, _ := gmsync.ConversationRow(conv, "gm")
	if saveErr := s.deps.Store.UpsertConversation(ctx, row); saveErr != nil {
		return nil, internalErr(fmt.Errorf("archived on phone but local save failed: %w", saveErr))
	}
	row, saveErr := s.deps.Store.GetConversation(ctx, p.ConversationID)
	if saveErr != nil {
		return nil, internalErr(saveErr)
	}
	s.Broadcast(EventConversationUpdated, map[string]any{"conversation": row})
	return row, nil
}
