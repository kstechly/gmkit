package rpc

import (
	"context"
	"encoding/json"
	"time"

	gmsync "github.com/johnlindquist/gmkit/internal/sync"
)

func (s *Server) handleChatsScan(ctx context.Context, params json.RawMessage) (any, *Error) {
	o, e := decode[gmsync.ScanOptions](params)
	if e != nil {
		return nil, e
	}
	if s.deps.Client == nil || s.authExpired.Load() {
		return nil, &Error{Code: CodeUnavailable, Message: "paired phone unavailable"}
	}
	if o.Folder == "" {
		o.Folder = "all"
	}
	if o.PageSize == 0 {
		o.PageSize = 25
	}
	if o.MaxPages == 0 {
		o.MaxPages = 1000
	}
	if e := o.Validate(); e != nil {
		return nil, &Error{Code: CodeInvalidParams, Message: e.Error()}
	}
	r, err := s.Scan(ctx, o)
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Message: err.Error(), Data: r}
	}
	return r, nil
}
func (s *Server) handleChatsRead(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, e := decode[struct {
		ID    string `json:"conversation_id"`
		Limit int    `json:"limit"`
	}](params)
	if e != nil {
		return nil, e
	}
	if s.deps.Client == nil || s.authExpired.Load() {
		return nil, &Error{Code: CodeUnavailable, Message: "paired phone unavailable"}
	}
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	r, err := gmsync.ReadChat(ctx, s.deps.Client, s.deps.Store, p.ID, p.Limit)
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Message: err.Error()}
	}
	return r, nil
}

func (s *Server) handleVerifyArchive(ctx context.Context, params json.RawMessage) (any, *Error) {
	p, e := decode[struct {
		IDs []string `json:"conversation_ids"`
	}](params)
	if e != nil {
		return nil, e
	}
	if s.deps.Client == nil || s.authExpired.Load() {
		return nil, &Error{Code: CodeUnavailable, Message: "paired phone unavailable"}
	}
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	r, err := s.deps.Client.VerifyArchives(ctx, p.IDs)
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Message: err.Error(), Data: r}
	}
	return r, nil
}

// Scan serializes startup, explicit reads, and background refresh on one phone connection.
func (s *Server) Scan(ctx context.Context, o gmsync.ScanOptions) (gmsync.ScanResult, error) {
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return gmsync.Scan(ctx, s.deps.Client, s.deps.Store, o)
}
