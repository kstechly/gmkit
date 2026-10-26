package cmd_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This daemon is a local protocol fixture. It has no phone or Google credentials.
func commandDaemon(t *testing.T, dir, method string, params, result any) {
	t.Helper()
	listener, err := net.Listen("unix", filepath.Join(dir, "gmcli.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		var request struct {
			ID     json.RawMessage
			Method string
			Params json.RawMessage
		}
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var got, want any
		json.Unmarshal(request.Params, &got)
		encoded, _ := json.Marshal(params)
		json.Unmarshal(encoded, &want)
		if request.Method != method || !reflect.DeepEqual(got, want) {
			t.Errorf("RPC %s %v, want %s %v", request.Method, got, method, want)
		}
		json.NewEncoder(conn).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}()
}

func TestPhoneCommandsUseDaemon(t *testing.T) {
	cases := []struct {
		name           string
		args           []string
		method         string
		params, result any
	}{
		{"send", []string{"send", "text", "--to", "fixture", "--message", "synthetic fixture", "--reply-to", "reply"}, "send.text", map[string]any{"conversation_id": "fixture", "body": "synthetic fixture", "reply_to_id": "reply", "requested_by": "cli"}, map[string]any{"approval_id": "local", "status": "pending"}},
		{"react", []string{"send", "react", "--message", "fixture", "--emoji", "🦊"}, "send.react", map[string]any{"message_id": "fixture", "emoji": "🦊", "remove": false, "switch": false}, map[string]any{"reacted": true}},
		{"remove", []string{"send", "react", "--message", "fixture", "--emoji", "🦊", "--remove"}, "send.react", map[string]any{"message_id": "fixture", "emoji": "🦊", "remove": true, "switch": false}, map[string]any{"reacted": true}},
		{"switch", []string{"send", "react", "--message", "fixture", "--emoji", "🦊", "--switch"}, "send.react", map[string]any{"message_id": "fixture", "emoji": "🦊", "remove": false, "switch": true}, map[string]any{"reacted": true}},
		{"archive", []string{"chats", "archive", "fixture"}, "chats.archive", map[string]any{"conversation_id": "fixture"}, map[string]any{"conversation_id": "fixture", "archived": true}},
		{"backfill", []string{"history", "backfill", "--chat", "fixture", "--requests", "2", "--count", "3"}, "history.backfill", map[string]any{"conversation_id": "fixture", "requests": 2, "count": 3}, map[string]any{"conversation_id": "fixture", "source": "live"}},
		{"lookup", []string{"history", "lookup", "--phone", "+15555550100", "--requests", "2", "--count", "3"}, "history.lookup", map[string]any{"phone": "+15555550100", "requests": 2, "count": 3}, map[string]any{"conversation_id": "fixture", "backfill": map[string]any{"conversation_id": "fixture", "source": "live"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			commandDaemon(t, dir, tc.method, tc.params, tc.result)
			out := runCmd(t, dir, append([]string{"--json", "--read-only=false"}, tc.args...)...)
			if tc.name == "send" {
				var a struct{ Status string }
				if err := json.Unmarshal([]byte(out), &a); err != nil || a.Status != "pending" {
					t.Fatalf("queued message misreported: %s", out)
				}
			}
		})
	}
}

func TestMediaDaemonDownloadPreservesOutputPath(t *testing.T) {
	for _, kind := range []string{"default", "file", "directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "fixture.png")
			if err := os.WriteFile(source, []byte("synthetic media"), 0600); err != nil {
				t.Fatal(err)
			}
			commandDaemon(t, dir, "media.download", map[string]any{"message_id": "fixture"}, map[string]any{"path": source, "bytes": 15, "mime_type": "image/png"})
			args := []string{"--json", "media", "download", "--message", "fixture"}
			want := source
			if kind == "file" {
				want = filepath.Join(dir, "export.png")
				args = append(args, "--out", want)
			}
			if kind == "directory" {
				export := filepath.Join(dir, "export")
				if err := os.Mkdir(export, 0700); err != nil {
					t.Fatal(err)
				}
				want = filepath.Join(export, "fixture.png")
				args = append(args, "--out", export)
			}
			out := runCmd(t, dir, args...)
			var result struct{ Path string }
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(result.Path)
			if err != nil || result.Path != want || string(data) != "synthetic media" {
				t.Fatalf("output %s: %q %v", result.Path, data, err)
			}
		})
	}
}

func TestPhoneCommandsRejectMissingDaemon(t *testing.T) {
	for _, args := range [][]string{
		{"send", "text", "--to", "fixture", "--message", "synthetic fixture"},
		{"send", "react", "--message", "fixture", "--emoji", "🦊"},
		{"media", "download", "--message", "fixture"},
		{"chats", "archive", "fixture"},
		{"history", "backfill", "--chat", "fixture"},
		{"history", "lookup", "--phone", "+15555550100"},
		{"sync"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			_, err := runCmdAllowError(t, t.TempDir(), append([]string{"--json", "--read-only=false"}, args...)...)
			if err == nil || !strings.Contains(err.Error(), "connect to gmcli daemon") {
				t.Fatalf("missing daemon: %v", err)
			}
		})
	}
}

func TestSyncFollowSubscribesToDaemon(t *testing.T) {
	dir := t.TempDir()
	listener, err := net.Listen("unix", filepath.Join(dir, "gmcli.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for _, method := range []string{"chats.scan", "subscribe"} {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			var request struct {
				ID     json.RawMessage
				Method string
			}
			if err := json.NewDecoder(conn).Decode(&request); err != nil {
				t.Error(err)
				conn.Close()
				return
			}
			if request.Method != method {
				t.Errorf("method %s, want %s", request.Method, method)
			}
			json.NewEncoder(conn).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
			if method == "subscribe" {
				json.NewEncoder(conn).Encode(map[string]any{"jsonrpc": "2.0", "method": "event", "params": map[string]any{"type": "fixture_event", "data": map[string]any{"value": 1}}})
			}
			conn.Close()
		}
	}()
	out, err := runCmdAllowError(t, dir, "--json", "sync", "--follow")
	if err == nil || !strings.Contains(err.Error(), "daemon event connection closed") {
		t.Fatalf("stream termination: %v", err)
	}
	decoder := json.NewDecoder(strings.NewReader(out))
	var coverage, event map[string]any
	if err := decoder.Decode(&coverage); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "fixture_event" {
		t.Fatalf("missing daemon event: %v", event)
	}
}

func TestApprovalsUseConfiguredDaemon(t *testing.T) {
	for _, tc := range []struct {
		name           string
		args           []string
		method         string
		params, result any
	}{
		{"list", []string{"approvals", "list"}, "approvals.list", map[string]any{"status": "pending", "limit": 50}, []any{}},
		{"approve", []string{"approvals", "approve", "fixture"}, "approvals.approve", map[string]any{"approval_id": "fixture"}, map[string]any{"status": "sent"}},
		{"deny", []string{"approvals", "deny", "fixture", "--reason", "synthetic fixture"}, "approvals.deny", map[string]any{"approval_id": "fixture", "reason": "synthetic fixture"}, map[string]any{"status": "denied"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			commandDaemon(t, dir, tc.method, tc.params, tc.result)
			runCmd(t, dir, append([]string{"--json", "--read-only=false"}, tc.args...)...)
		})
	}
}
