# gmcli daemon RPC protocol

`gmcli serve` exposes a local control surface over a unix domain socket
(default `<store>/gmcli.sock`, mode 0600). The protocol is newline-delimited
JSON-RPC 2.0 — one JSON document per line — chosen so any language (and any
agent runtime) can speak it without codegen. gmtui and `gmcli mcp` are the
first-party clients; this document is the reference for writing your own.

## Framing

```
-> {"jsonrpc":"2.0","id":1,"method":"chats.list","params":{"limit":20}}
<- {"jsonrpc":"2.0","id":1,"result":[ ... ]}
<- {"jsonrpc":"2.0","id":2,"error":{"code":1002,"message":"conversation not found: x"}}
```

Requests may be pipelined; responses can arrive out of order (match on `id`).
A request without an `id` is treated as a notification and gets no reply.
Lines are capped at 4 MiB.

After a client calls `subscribe`, the server pushes events as JSON-RPC
notifications on the same connection:

```
<- {"jsonrpc":"2.0","method":"event","params":{"type":"message.new","data":{...}}}
```

## Error codes

| Code   | Meaning                                                        |
| ------ | -------------------------------------------------------------- |
| -32700 | parse error                                                    |
| -32600 | invalid request                                                |
| -32601 | method not found                                               |
| -32602 | invalid params                                                 |
| -32603 | internal error                                                 |
| 1001   | sends disabled (daemon started read-only)                      |
| 1002   | entity not found                                               |
| 1003   | approval already resolved                                      |
| 1004   | phone/relay unavailable (offline daemon, disconnected, or send failure) |

## Methods

Read methods (always available):

| Method             | Params                                                              | Result |
| ------------------ | ------------------------------------------------------------------- | ------ |
| `ping`             | —                                                                   | `{pong, version, schema_version}` |
| `status`           | —                                                                   | `{coverage, connected, offline, auth_expired, send_mode, pending_approvals, conversations, messages, last_event_ms, last_connect_ms, updated_at_ms}` — `offline` = deliberately no phone connection; `auth_expired` = phone reports the pairing logged out (`gmcli auth` required; daemon keeps serving the archive) |
| `daemon.shutdown`  | —                                                                   | `{shutting_down: true}`; the daemon exits. Used by `gmcli auth` to hand over a fresh session — on-demand clients spawn a replacement. |
| `auth.pair`        | —                                                                   | `{started}`; legacy QR pairing in the daemon. Emits `pair.qr {url}`, then `pair.success`/`pair.error`. Google removed the QR scanner from current Messages builds — prefer `auth.pair.google`. |
| `auth.pair.google` | `{cookies_input}` — raw Cookie header / Copy-as-cURL / fetch / JSON | `{started}`; Google-account pairing. Emits `pair.emoji {emoji}` (tap it on the phone), then `pair.success {phone_id}` / `pair.error {error}`. On success the daemon restarts to load the new session. |
| `media.download`   | `{message_id}`                                                      | `{path, bytes, mime_type}`; downloads+decrypts the attachment into `<store>/media/`. |
| `alias.set`        | `{target_type, target_id, alias}` — target_type: contact\|conversation | `{set: true}`; local rename, never sent to Google. |
| `alias.rm`         | `{target_type, target_id}`                                          | `{removed: true}` |
| `send.react`       | `{message_id, emoji, remove?, switch?}`                             | `{reacted: true}`; add, remove or switch a reaction. `remove` and `switch` are mutually exclusive. Respects send mode but skips the approval queue (low-risk, human-initiated). |
| `subscribe`        | —                                                                   | `{subscribed: true}`; events start flowing on this connection |
| `chats.list`       | `{limit?, unread_only?, pinned?}`                                   | array of conversations |
| `chats.find`       | `{query, limit?}` — person/group/number fragment                    | array of conversations, matched on name, alias, participants, and contacts |
| `chats.show`       | `{conversation_id, limit?}`                                         | `{conversation, messages}` (messages newest-first) |
| `messages.list`    | `{conversation_id?, sender_id?, since_ms?, until_ms?, limit?, order?}` | array of messages |
| `sync.refresh`     | —                                                                   | `{started}` schedules paginated inbox/archive scans (rate-limited, 30s). Completion broadcasts `sync.status {state: "refreshed" | "refresh_failed", coverage}`; no message history is requested. Scheduling/connection activity is not coverage. |
| `messages.search`  | `{query, conversation_id?, since_ms?, until_ms?, limit?}`           | array of `{message_id, conversation_id, conversation_name, sender_name, body, snippet, timestamp_ms, timestamp_iso, is_from_me}` |
| `messages.show`    | `{message_id}`                                                      | message |
| `messages.context` | `{message_id, before?, after?}` (default 5/5)                       | array of messages, oldest-first, anchor included |
| `contacts.search`  | `{query?, limit?}`                                                  | array of contacts |
| `contacts.show`    | `{id}` — participant_id or phone number                             | contact |

Phone-touching methods (need a non-`--offline` daemon; sends additionally
need `--read-only=false`):

| Method              | Params                                    | Result |
| ------------------- | ----------------------------------------- | ------ |
| `history.backfill`  | `{conversation_id, requests?, count?}`    | backfill report |
| `history.lookup`    | `{phone, requests?, count?}` (E.164)      | `{conversation_id, name, backfill}` |
| `send.text`         | `{conversation_id, body, reply_to_id?, requested_by?}` | approval row (see below) |
| `approvals.list`    | `{status?, limit?}`                       | array of approvals |
| `approvals.approve` | `{approval_id}` — **performs the send**   | resolved approval |
| `approvals.deny`    | `{approval_id, reason?}`                  | resolved approval |

### Archive conversations

`chats.archive` takes `{conversation_id}` and returns the updated conversation
row, including `archived: true`, only after a fresh phone readback confirms it.
It respects daemon read-only mode (code 1001), runs immediately in writable
modes, and does not use the outgoing-text approval queue. Missing IDs return
-32602; unavailable pairing or failed verification returns 1004. Already-archived
threads succeed without mutation. Success also emits `conversation.updated`
with the existing `{conversation: ...}` event shape. Timeout errors leave the
remote outcome unconfirmed; they never imply the archive did not happen.

### Search semantics

`messages.search` degrades gracefully so natural-language queries always
work: the query runs verbatim first (full FTS5 syntax — quoted phrases,
AND/OR/NOT), and if FTS5 rejects it, terms are cleaned (edge punctuation
trimmed, sub-3-character terms and stopwords dropped) and retried quoted
with AND semantics. When requiring every term finds nothing — and the query
contains no explicit FTS5 syntax — a final OR pass surfaces per-term
matches. Terms need 3+ characters to match (trigram index). Results are
newest-first.

Messages returned by `chats.show`, `messages.list`, and `messages.context`
are enriched with `sender_name` (alias > contact name > number) and
`timestamp_iso`; search hits additionally carry `conversation_name`.

## Send policy and the approval queue

The daemon's send mode is fixed at startup:

- **off** (default; daemon started without `--read-only=false`): `send.text`
  and `approvals.approve` return code 1001.
- **approve** (`--read-only=false serve`): `send.text` inserts a `pending`
  approval row and returns it — *nothing is sent*. A human resolves it via
  `approvals.approve` (gmtui `y`, or `gmcli approvals approve`). Approving
  performs the send and resolves the row to `sent` or `failed`; a failed send
  leaves useful detail in `error`.
- **direct** (`--read-only=false serve --sends direct`): `send.text` sends
  immediately; an audit row is still written to the approvals table.

Approval rows: `{approval_id, conversation_id, body, reply_to_id?,
requested_by, status: pending|sent|failed|denied|canceled, error?,
message_id?, created_at_ms, updated_at_ms}`. Double-approve races lose
cleanly with code 1003.

## Events

| Type                   | Data                                             |
| ---------------------- | ------------------------------------------------ |
| `message.new`          | `{message, is_old}` — after the row is persisted |
| `conversation.updated` | `{conversation}`                                 |
| `approval.requested`   | approval row                                     |
| `approval.resolved`    | approval row                                     |
| `sync.status`          | `{state}`: `ready`, `refreshed` (bulk import finished — refetch), `refresh_failed`, `phone_not_responding`, `phone_responding`, `listen_temporary_error`, `listen_recovered`, `logged_out` |

Slow consumers may have events dropped rather than stall the daemon; treat
the stream as advisory and re-query when reconnecting.

## Daemon lifecycle

- The socket accepts connections immediately on startup; the phone
  connection and initial import proceed in the background. Query the archive
  right away and watch `sync.status` / `status` for connection state.
- Start `gmcli serve` with the intended credential source, or manage it with
  a service. CLI, MCP and TUI clients never spawn a replacement daemon.
- Live operations fail promptly when the configured socket is unavailable.
  Cached MCP read tools still work directly against the local archive.
- `--idle-exit <duration>` allows an explicitly started daemon to exit when
  unused; `0` means run forever. Explicit `serve --auto` defaults to ten
  minutes and the approval queue unless a read-only policy is supplied.

## Concurrency notes

- Only one daemon owns the paired connection per store. All regular live
  CLI operations use its socket; no client falls back to another connection.
- `gmcli serve --offline` serves archive queries and the approval queue without
  a phone connection. Clients do not start it automatically.
- A TUI whose daemon disappears reconnects to the same socket after its
  configured service returns. It never starts an unconfigured replacement.

## Coverage cutover

CLI query output uses `{coverage,data}`. Existing cached RPC query methods remain
explicit local-store operations; `status.coverage` supplies their folder-scan
provenance. MCP read results include `coverage` alongside their existing data.

- `chats.scan {folder: "inbox"|"archive"|"all", page_size?:25, max_pages?:1000,
  messages_per_thread?:0, contacts?:false}` returns `{coverage,data:[Conversation]}`.
  It is synchronous and shares the daemon’s phone-operation lock. Failure code
  1004 includes the partial result in `error.data`.
- `chats.read {conversation_id,limit}` returns `{coverage,conversation,messages}`.
  The messages are the requested thread’s fresh response records, not cache filler.
- `chats.verifyArchive {conversation_ids:[...]}` returns
  `{source:"live",checked_at,folder_scans,checks,verified}`. Each check contains
  `{conversation_id,individual_status,in_inbox,in_archive,verified}`. Code 1004
  carries the report in `error.data` when verification cannot establish agreement.

`coverage` contains `source`, `folder`, `folder_scans`, `message_history`,
`truncated`, and optionally `contacts_imported`. A folder scan contains
`folder,started_at,scanned_at,complete,stop_reason,pages,page_size,count`.
No recorded scan means unknown coverage. Scan completion means cursor exhaustion
at traversal time, not atomic or durable folder state.

Message coverage contains `scope,complete,stop_reason,requested_per_thread,
threads_fetched,records_imported`. Recent samples always have `complete:false`.
History backfill separately reports `source,stop_reason,traversal_complete,
history_complete`; traversing an older prefix cannot prove the entire cache has
no gaps. Archive readback waits for status propagation for up to five seconds;
folder agreement is provided by the separate read-only verifier.
