package rpc

import (
	"encoding/json"
	"testing"
)

func TestReactionRejectsConflictingActions(t *testing.T) {
	client, _, _ := startTestServer(t, SendOff)
	err := client.Call(callCtx(t), "send.react", map[string]any{"message_id": "fixture", "emoji": "🦊", "remove": true, "switch": true}, nil)
	rpcErr, ok := err.(*Error)
	if !ok || rpcErr.Code != CodeInvalidParams {
		t.Fatalf("conflicting reaction actions: %v", err)
	}
}

func TestReactionSwitchRespectsReadOnlyDaemon(t *testing.T) {
	_, _, server := startTestServer(t, SendOff)
	_, err := server.handleSendReact(json.RawMessage(`{"message_id":"fixture","emoji":"🦊","switch":true}`))
	if err == nil || err.Code != CodeSendsDisabled {
		t.Fatalf("read only switch: %v", err)
	}
}
