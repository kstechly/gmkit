package rpc

import (
	"errors"
	"testing"
)

func TestArchiveRPCRejectsUnavailableAndReadOnly(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mode   SendMode
		params any
		code   int
	}{
		{"read only", SendOff, map[string]string{"conversation_id": "c"}, CodeSendsDisabled},
		{"missing id", SendApprove, map[string]string{}, CodeInvalidParams},
		{"offline", SendApprove, map[string]string{"conversation_id": "c"}, CodeUnavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _, _ := startTestServer(t, tt.mode)
			err := c.Call(callCtx(t), "chats.archive", tt.params, nil)
			var rpcErr *Error
			if !errors.As(err, &rpcErr) || rpcErr.Code != tt.code {
				t.Fatalf("got %v, want code %d", err, tt.code)
			}
		})
	}
}
