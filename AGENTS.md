# Repository Guidelines

## Project Structure & Module Organization

`gmkit` (module `github.com/johnlindquist/gmkit`) is a Go 1.26 CLI for Google Messages. `cmd/gmcli/main.go` is the entrypoint; it wires into the Cobra command tree in `internal/cmd/`. Keep user-facing verbs and flags in `internal/cmd/`, and reusable implementation in the other `internal/` packages:

- `internal/gm/`: session, pairing, sync events, send/react, media.
- `internal/store/`: SQLite schema, queries, aliases, approvals, message/contact persistence.
- `internal/sync/`: event-to-store synchronization.
- `internal/history/`: backfill engine shared by the CLI and the daemon.
- `internal/rpc/`: `gmcli serve` daemon — JSON-RPC over unix socket, event broadcast, send-approval queue. Protocol reference: `docs/rpc-protocol.md`. Keep gmtui's `src/model.rs` and `docs/rpc-protocol.md` in sync with wire-shape changes.
- `internal/output/`: JSON and tabular rendering helpers.
- `internal/paths/` and `internal/logging/`: environment paths and logging setup.
- `gmtui/`: Rust ratatui TUI client for the daemon (own Cargo crate; `cargo fmt && cargo clippy && cargo build` before committing changes there).
- `skills/google-messages/`: upstream bundled skill. Kaya’s active Codex/Claude skill is machine-local at `~/.codex/skills/google-messages-cli`; maintain that local skill for this fork’s interface.
- `docs/research/`: design notes and protocol research.

Tests live beside their packages as `*_test.go`.

## Build, Test, and Development Commands

- `go build -o gmcli ./cmd/gmcli` builds the local binary.
- `go build -ldflags "-X github.com/johnlindquist/gmkit/internal/cmd.Version=$(git describe --tags --always --dirty)" -o gmcli ./cmd/gmcli` builds with version metadata.
- `./install.sh` builds and globally installs both gmcli and gmtui.
- `go test ./...` runs the full test suite used by CI.
- `go vet ./...` runs static checks used by CI.
- `./gmcli --help` or `./gmcli <command> --help` validates command wiring while developing.

Use `--store <tmpdir>` when exercising live commands to avoid touching real state.

## Agent Operations: Searching and Sending Texts

Use the user's real store for requested personal message tasks unless the user asks for an isolated store. Do not use `--store <tmpdir>` for real archive lookup, history backfill, or sends.

To refresh the local archive before searching:

- Run `./gmcli sync` for a one-shot import. Sync follows inbox/archive cursors and imports contacts; use `--messages-per-thread N` to request a recent sample. Check coverage and exit status. A short page can still have a continuation cursor.
- Run `./gmcli chats list --folder inbox --limit 100` for a live scan. `--limit` only limits output; inspect `coverage.truncated`. Use `--source cached --folder all` for known cached rows.
- Run `./gmcli contacts search "<name-or-number>" --limit 20` to find a participant.
- Run `./gmcli messages search "<fts-query>" --limit 50` to search archived message bodies. This is local SQLite FTS only; it cannot find conversations that have not been synced or backfilled.

For old texts that are not surfaced by normal sync:

- If the user provides a phone number, normalize it to E.164 before lookup, for example `385-230-5832` becomes `+13852305832`.
- Run `./gmcli --json history lookup --phone +13852305832 --requests 20 --count 50` to ask Google Messages for that conversation by number and backfill older messages.
- If the conversation is already known, use `./gmcli --json history backfill --chat <conversation_id> --requests 20 --count 50`.
- After backfill, inspect with `./gmcli chats show <conversation_id> --limit 100 --full` or `./gmcli --json messages list --conv <conversation_id> --limit 100`.
- Historical `FetchMessages` backfill may omit downloadable media payloads even when message rows show an image MIME type; report that limitation plainly instead of claiming images can be downloaded.

For the building-keys thread from `.notes/keys.md`, the old conversation was found by looking up Russell Franklin at `+13852305832`; the relevant local conversation ID was `398` at the time of that run. Do not assume that ID is stable across stores; prefer phone lookup or contact search when redoing the task.

To send a text:

- First resolve the recipient to a `conversation_id` with `contacts search`, `chats list`, `history lookup`, or `chats show`.
- Preview the exact recipient and message body in the terminal or final response before sending when there is any ambiguity.
- Sending mutates phone state and is blocked by default. Only send when the user explicitly asks to send, then run `./gmcli --read-only=false send text --to <conversation_id> --message "<body>"`.
- For quote replies, add `--reply-to <message_id>`.
- Prefer `--json` when the caller needs a machine-readable receipt: `./gmcli --json --read-only=false send text --to <conversation_id> --message "<body>"`.
- If a `gmcli serve` daemon is running on the store (check for `<store>/gmcli.sock`), do not open a second session with `sync`, `send`, or `history` — the phone allows one connected client. Go through the daemon instead: `send.text` over the socket queues an approval; the human approves in gmtui or with `gmcli --read-only=false approvals approve <id>`. Never approve an approval you queued yourself unless the user explicitly told you to send.

## Daemon, MCP, and approvals

- `./gmcli serve` runs the long-lived daemon (unix-socket JSON-RPC + event stream, `docs/rpc-protocol.md`). `--offline` serves the archive without a phone; `--read-only=false` enables the approval queue; `--read-only=false --sends direct` sends immediately with an audit row.
- Start the daemon with its intended credentials or manage it as a service. gmtui, MCP live tools and CLI commands require that configured socket; they never spawn another daemon or open a second phone connection. Cached MCP tools still work directly against the local archive.
- `./gmcli mcp` is the MCP stdio server for agent runtimes. Read tools hit SQLite directly; `send_text`/`backfill_history` go through the daemon. `send_text` returning `status: "pending"` means queued for human approval, NOT sent — report it that way.
- To resolve a person to a conversation, prefer `./gmcli chats find "<name-or-number>"` (or the `find_chats` MCP tool) over manual contacts/chats cross-referencing.
- Chat/message query JSON uses `{coverage,data}`; archive receipts use `{conversation,verification_scope}`. Use `chats verify-archive ID...` to compare individual status with complete folder scans.
- `./gmcli messages search` accepts natural-language queries: raw FTS5 first, then a quoted AND fallback, then OR. Rich results include sender/conversation names.
- `./gmcli approvals list|approve|deny` reviews the queue from the CLI; `approve` performs the send and requires `--read-only=false`.

## Coding Style & Naming Conventions

Use standard Go formatting: run `gofmt` on changed Go files. Package names are short lowercase nouns (`store`, `paths`, `output`). Exported identifiers need clear doc comments when part of cross-package APIs. Keep command names and flags lowercase and shell-oriented, for example `history backfill`, `--read-only`, and `--json`.

## Testing Guidelines

Prefer focused table-driven tests in the package under change. Name tests after behavior, such as `TestAliasesSetAndResolve` or `TestVersionCommandUsesInjectedVersion`. For CLI behavior, use the existing command test patterns in `cmd/*_test.go`. Any schema or query change in `internal/store/` should include coverage for migration/query behavior.

## Commit & Pull Request Guidelines

Recent history uses short imperative subjects, sometimes with Conventional Commit prefixes: `fix: harden live sync smoke paths`, `docs: fix build command`, `chore: prepare alpha release`. Keep subjects concise and specific.

Pull requests should describe the behavior change, list test commands run, and call out live-device implications for auth, sync, send, history, or media changes. Include terminal output when changing human-readable command output.

## Security & Configuration Tips

This project handles local message archives and session tokens. Do not commit generated databases, `session.json`, downloaded media, or logs. Preserve read-only defaults for commands that mutate phone state, and require explicit opt-in for sends or reactions.
