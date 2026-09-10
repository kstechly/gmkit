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

func TestLiveReadsRejectOfflineDaemon(t *testing.T) {
	c, _, _ := startTestServer(t, SendOff)
	for _, method := range []string{"chats.scan", "chats.read", "chats.verifyArchive"} {
		e := c.Call(callCtx(t), method, map[string]any{}, nil)
		var re *Error
		if !errors.As(e, &re) || re.Code != CodeUnavailable {
			t.Fatalf("%s returned %v", method, e)
		}
	}
}
