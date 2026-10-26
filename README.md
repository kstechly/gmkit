# gmkit

Google Messages for your terminal and your AI agents — local-first, with a
human approval queue for anything an agent wants to send.

gmkit ships one standalone Go binary, `gmcli` (CLI, sync daemon, and MCP
server), that pairs with Google Messages and archives conversations into a
local SQLite + FTS5 database, plus `gmtui` ([`gmtui/`](gmtui/)), a Rust
terminal UI. gmkit builds on
[fdsouvenir/gmcli](https://github.com/fdsouvenir/gmcli) and extends it with
the daemon, agent, and TUI layers.

```
                 ┌───────────────────────────────┐
                 │ gmcli serve (Go daemon)       │
                 │ libgm session · sync · SQLite │
                 └──────────────┬────────────────┘
                                │ JSON-RPC over unix socket
             ┌──────────────────┼─────────────────────┐
             │                  │                     │
     gmtui (Rust TUI)      gmcli mcp             gmcli CLI + --json
     human cockpit +       MCP tools for         scripts and ad-hoc
     send approvals        Claude & agents       agent use
```

> **Status:** beta. Pairing, session persistence, sync loop, query CLI
> (`messages`, `contacts`, `chats`), best-effort history backfill, send
> commands, media download, and an LLM skill (`skills/google-messages`) are
> wired up and covered by the automated test suite. Live-device behavior still
> depends on the unofficial Google Messages web protocol, so validate auth,
> sync, history, media, and send flows on your own account before relying on
> unattended operation. See
> [`docs/research/phase-1-libgmessages.md`](docs/research/phase-1-libgmessages.md)
> for the design notes that motivated this layout, and
> [`skills/README.md`](skills/README.md) for the skill installation guide.

## What it is

- **Standalone.** No Matrix server, no Docker, no bridge daemon. Just a Go
  binary, a SQLite file, and a phone running Google Messages.
- **Read-first.** Phone-mutating operations (sending texts and reactions) are
  gated behind explicit flags. The default is to observe, not to send.
- **Local.** Messages live in a single SQLite database under your data
  directory (XDG-compliant). Nothing is uploaded anywhere.
- **AGPL-3.0.** gmcli imports `pkg/libgm` from
  [mautrix/gmessages](https://github.com/mautrix/gmessages), which is licensed
  AGPL-3.0. That makes gmcli a derivative work and obligates the same license
  for the whole program. See `LICENSE` and `NOTICE`.

## How it works

`pkg/libgm` reverse-engineers the Google Messages web client protocol. After a
one-time QR pairing handshake, it maintains an authenticated session with
your paired phone — all messages flow through the phone, which proxies them
to Google's relay infrastructure. gmcli wraps that session with an event loop
that writes incoming messages, conversation updates, and contact data to a
local SQLite database, and exposes the database through a CLI.

The phone must be online and have Google Messages installed for the relay to
work. Pairing tokens are refreshed automatically; full re-pairing is required
roughly every 14 days of inactivity (Google's policy, not ours).

## Install this fork

Build the archive-capable CLI from this fork with Go 1.26 or newer:

```sh
git clone https://github.com/kstechly/gmkit.git
cd gmkit
mkdir -p ~/.local/bin
go build -ldflags "-X github.com/johnlindquist/gmkit/internal/cmd.Version=$(git describe --tags --always --dirty)" -o ~/.local/bin/gmcli ./cmd/gmcli
```

The Go module path remains `github.com/johnlindquist/gmkit`; build from the
checkout above to get this fork's changes. An upstream `go install ...@latest`
will install upstream instead. The optional `./install.sh --source` also builds
the Rust TUI when Cargo is installed.

## Current limits

- Live-device coverage is still limited. Before relying on gmcli unattended,
  test `auth`, `sync`, query commands, `history backfill`, `media download`,
  and a deliberate send with your own Google Messages account.
- History backfill is best-effort and depends on what Google Messages returns
  through the paired phone.
- The phone must be online for sync, backfill, sends, and media downloads.
- The SQLite database is local but unencrypted. Use filesystem encryption if
  you need at-rest protection.
- The protocol depends on the unofficial `libgm` reverse-engineered Google
  Messages web protocol and can break if Google changes that protocol.

## Quick start

```sh
# 1. One-time pairing (renders a QR code in the terminal — scan with the
#    Google Messages app on your phone, Settings → Device pairing → QR code).
gmcli auth

# 2. Scan both folders and refresh contacts; explicitly sample recent messages.
gmcli sync --messages-per-thread 10
# Add --follow to stream new events after the scan.

# 3. Live chat reads; message search/list query the explicitly labeled cache.
gmcli chats list --folder inbox                # complete live scan, bounded output
gmcli chats show <conversation-id>            # live header + recent message sample
gmcli chats list --source cached --folder all # offline, all known cached rows
gmcli messages search "dinner"                # FTS5 across all conversations
gmcli messages list --conv <conv-id>          # message list with filters
gmcli messages show <message-id>              # single message detail
gmcli messages context <message-id>           # surrounding messages
gmcli contacts search alice                   # name/number/alias substring match
gmcli contacts show <participant-id-or-num>   # contact detail

# 4. Local-only labels.
gmcli contacts alias set --id <pid> --alias "Mom"
gmcli contacts alias list                     # list all set aliases
gmcli contacts alias rm --id <pid>

# 5. Best-effort history backfill, modeled after wacli.
gmcli history backfill --chat <conv-id> --requests 10 --count 50
# JSON output reports protocol records separately from the chat message delta:
# fetched_messages, sync_records_processed, messages_before, messages_after,
# messages_added_for_chat.

# 6. Write to the phone (always requires --read-only=false).
gmcli --read-only=false send text --to <conv-id> --message "on my way"
gmcli --read-only=false send react --message <msg-id> --emoji "👍"
gmcli media download --message <msg-id>
# `send text` only reports success after Google Messages echoes the outgoing
# message back with its canonical message_id.

# Every command supports --json for machine-readable output and --full to
# disable truncation in tables.
gmcli --json chats list | jq '.data[0].name'
```

## Daemon, TUI, and agents

The daemon keeps the session alive, streams events into the archive, and
exposes a JSON-RPC surface over a unix socket (`<store>/gmcli.sock`,
documented in [`docs/rpc-protocol.md`](docs/rpc-protocol.md)). **You
normally never start it yourself**: gmtui, `gmcli mcp`, and `gmcli
approvals` auto-start it on demand (`serve --auto`) and it exits on its own
after ten minutes without clients. Run it manually only when you want
different behavior:

```sh
gmcli serve                      # always-on, read-only: query surface only
gmcli --read-only=false serve    # always-on with sends (approval queue)
gmcli serve --offline            # archive queries without a phone connection
```

Auto-started daemons use the approval queue for sends — safe by design,
since nothing reaches the phone without a human approving — and log to
`<store>/daemon.log`.

**gmtui** ([`gmtui/`](gmtui/)) is a Rust/ratatui terminal client:
conversation browser, live message stream, full-text search, compose, and
the approval queue UI. After pairing once with `gmcli auth`, running `gmtui`
is all a new user needs — it brings the daemon up itself.
`cd gmtui && cargo install --path .`

**AI agents** connect through `gmcli mcp`, an MCP (Model Context Protocol)
stdio server. Read tools (`find_chats`, `search_messages`, `list_chats`,
`list_messages`, `get_message_context`, `search_contacts`, `get_status`)
work standalone against the local archive; `send_text` and
`backfill_history` go through the daemon (auto-started as needed). Search is
agent-friendly: natural-language queries fall back gracefully from FTS5
syntax to literal AND then OR term matching, results carry sender and
conversation names plus ISO timestamps, and `since`/`until` accept ISO dates
or relative durations like `7d`. Register with your agent runtime, e.g. for
Claude Code:

```sh
claude mcp add google-messages -- gmcli mcp
```

**The approval queue** is the piece that makes agent sends safe: under the
default daemon policy (`--sends approve`), an agent's `send_text` only
*proposes* a message. It lands in a local queue, gmtui pops a warning, and a
human approves (`y` in gmtui, or `gmcli approvals approve <id>`) or denies
it. Nothing touches your phone until then. `--sends direct` skips the queue
for people who trust their agents; every send still leaves an audit row.

```sh
gmcli approvals list                       # review the queue headlessly
gmcli --read-only=false approvals approve <id>   # approve = send
gmcli approvals deny <id> --reason "nope"
```

While `serve` is running, prefer the socket/MCP surface over `gmcli
sync`/`send`/`history` CLI commands on the same store — the daemon owns the
one allowed session to your phone.

## Global flags

| Flag             | Default                            | Purpose                                                  |
| ---------------- | ---------------------------------- | -------------------------------------------------------- |
| `--store DIR`    | `$XDG_STATE_HOME/gmcli`            | Where session, SQLite, and downloaded media live.        |
| `--read-only`    | `true`                             | Block commands that send texts or reactions through the phone. |
| `--json`         | `false`                            | Emit machine-readable output.                            |
| `--full`         | `false`                            | Disable truncation in tabular output.                    |
| `--log-level`    | `info`                             | Verbosity (`trace`/`debug`/`info`/`warn`).               |

## Layout

```
cmd/                  Cobra command tree (auth, sync, serve, mcp, approvals,
                      version, doctor, messages, contacts, chats, send, media)
internal/
  gm/                 libgm wrapper — pairing, session, events, send/react,
                      WaitForReady, DownloadMedia
  store/              SQLite + FTS5 store (schema v3: aliases + approvals)
  sync/               Event-to-store pump
  history/            Best-effort backfill engine (shared by CLI and daemon)
  rpc/                Daemon: JSON-RPC over unix socket, event stream,
                      approval queue (docs/rpc-protocol.md)
  output/             Shared JSON / tab-aligned table renderers
  paths/              XDG path resolution (XDG_STATE_HOME)
  logging/            zerolog setup
gmtui/                Rust ratatui terminal client for the daemon
skills/
  google-messages/    LLM skill bundle - archive playbook for assistants
docs/research/        Phase 1 research notes
docs/rpc-protocol.md  Daemon socket protocol reference
```

## LLM integration

The first-class integration is `gmcli mcp` (see “Daemon, TUI, and agents”
above). The bundled OpenClaw skill lives in `skills/google-messages`. It is
published on ClawHub as
[Google Messages Local Archive](https://clawhub.ai/fdsouvenir/google-messages-local-archive)
(`google-messages-local-archive`) for searching, summarizing, and answering
questions from a local Google Messages SMS/RCS archive with read-only commands
by default.

## Privacy

- All data is local. gmcli does not phone home.
- Session tokens are stored in `$XDG_STATE_HOME/gmcli/session.json` with mode
  0600.
- Media attachments are referenced by ID in the database; bytes are not
  downloaded by default. Use `gmcli media download --message <message-id>`
  for explicit downloads.
- The SQLite file is unencrypted. If you need at-rest encryption, layer your
  own filesystem encryption (FileVault, LUKS, etc.).

## Attribution

- **libgm** — the Google Messages protocol library this CLI depends on — was
  written by Tulir Asokan and the
  [mautrix](https://github.com/mautrix/gmessages) contributors. License:
  AGPL-3.0. gmcli would not be possible without their reverse-engineering
  work.
- The CLI verb structure is inspired by Peter Steinberger's
  [wacli](https://github.com/steipete/wacli) for WhatsApp.
- Storage and MCP-tool patterns draw from
  [openmessage](https://github.com/MaxGhenis/openmessage) by Max Ghenis,
  released under the Unlicense.

## License

GNU Affero General Public License, version 3 or later. See `LICENSE` for the
full text and `NOTICE` for the third-party notices required by upstream
licenses.

## Archive threads (this fork)

Resolve the exact conversation with `gmcli chats find`, then run:

```sh
gmcli --read-only=false chats archive <conversation-id>
gmcli --json chats show <conversation-id>
```

The archive command verifies the phone's status before reporting success and
updates the local `archived` field. Already-archived threads succeed without
another write. It requires the configured daemon; restart an older daemon
with the new binary and `--read-only=false` before archiving.
With no daemon socket, it fails immediately.
A failed readback or timeout is an error, even if Google acknowledged the write;
the remote change may still have taken effect. No messages are deleted.

The coverage cutover migrates the database to schema 4. Old rows are retained;
their scan coverage starts as unknown. Run `gmcli sync` to establish current
folder membership. Restart an older daemon before using the new commands.
No re-pairing is needed.

## Live reads, cached reads, and coverage

`chats list`, `chats find`, and `chats show` default to `--source live` and require
the configured daemon, as does `chats verify-archive`. Start `gmcli serve` before
live reads. If its socket is unavailable, the read fails immediately: it never
opens a second paired connection with stale credentials. `--source cached` makes an offline read
explicit. `chats list` defaults to `--folder inbox`; choose `archive` or `all`.
Send, reaction, media, history and sync commands use the same configured daemon.
`sync --follow` subscribes to daemon events. Text sends report the daemon's
approval status; a pending approval has not sent a message.
Message search/list/show/context remain local index queries and label themselves
`source=cached`. A search miss never proves absence from phone history.

Chat/message query JSON is now `{ "coverage": {...}, "data": ... }`, not a bare
array. Text output prints coverage to stderr. `coverage` reports source, folder,
folder scan times/counts/pages, message-history scope, and output truncation.
`complete=true` on a folder means its cursor was exhausted without errors during
that traversal; it is not an atomic snapshot or a promise about future messages.
`--limit` caps output only. `--page-size` is 1..100 (default 25); `--max-pages`
defaults to 1000 per folder. Budget exhaustion, repeated/opaque cursors, invalid
folder rows, persistence failures, and phone errors produce nonzero exits and
partial coverage. A short page with a cursor is **not** the end.

`sync` scans both folders and contacts. Message sampling is opt-in with
`--messages-per-thread N`; it never implies complete history. `history backfill`
reports its cursor stop reason and traversal completion separately from whole
history completeness. Starting at the oldest cached message cannot prove that
newer cache gaps do not exist. `doctor` and daemon `status` distinguish recorded
scans from connection activity. Daemon refresh now uses the same folder scanner.

`chats archive ID` waits up to five seconds for status propagation after the
acknowledgment without resending the mutation. Its JSON contains `conversation`
and `verification_scope: "individual_remote_readback"`. For one thread, add
`--verify-folders`; after a batch, use the read-only command:

```sh
gmcli --json chats verify-archive ID1 ID2 ID3
```

It scans inbox and archive completely and reads each target individually.
Disagreement returns nonzero with per-target `individual_status`, `in_inbox`,
`in_archive`, and `verified` fields. Neither incomplete scans nor failed reads
can produce verified success. This verifies state at read time; new activity can
subsequently move a thread back to the inbox.

Protocol investigation and live evidence: [folder pagination](docs/research/folder-pagination.md).

### Following a running Chrome login

Set `GMCLI_CHROME_CDP=http://127.0.0.1:9222` on the standing `gmcli serve` process to use the Google cookies from that browser for every relay/config request. The client reads the browser CDP endpoint without opening or changing tabs. Fresh cookie snapshots stay in memory; logs never print them. A missing browser or signed-out profile returns an unavailable-source error; the supervisor reconnects once Chrome returns. A Google 401/403 from any request, including acknowledgments, marks the pairing expired so the recovery watcher can re-pair it.

Google pairing creates a separate device identity for gmcli after loading browser configuration. Reusing Chrome's device identity with separate encryption keys causes HMAC failures when Messages for Web becomes active. Cookies can follow Chrome while the paired device identity and keys remain gmcli's own.

This deliberately depends on that browser remaining signed in. A pairing revoked on the phone still needs a new Google-account emoji handshake. Kaya's `gmcli-watch` dispatches that recovery through the existing agent task graph without a decision card; phone work waits for the real panel to be off and preserves existing Messages tasks. Live chat reads fetch only the requested recent sample, not complete history.
