mod app;
mod event;
mod model;
mod rpc;
mod ui;

use std::path::{Path, PathBuf};
use std::time::Duration;

use clap::Parser;
use color_eyre::eyre::{eyre, Result};
use tokio::sync::mpsc;

use crate::app::App;
use crate::event::Event;
use crate::model::ServerEvent;
use crate::rpc::RpcClient;

/// Terminal UI for gmcli — browse, search, and approve Google Messages.
///
/// Connects only to the configured daemon. Start `gmcli serve` with its
/// intended credential source before opening the TUI.
#[derive(Parser, Debug, Clone)]
#[command(version, about)]
struct Args {
    /// gmcli store directory (default: $XDG_STATE_HOME/gmcli or
    /// ~/.local/state/gmcli). The daemon socket lives at <store>/gmcli.sock.
    #[arg(long)]
    store: Option<PathBuf>,

    /// Explicit socket path; overrides --store.
    #[arg(long)]
    socket: Option<PathBuf>,
}

fn resolve_socket(args: &Args) -> Result<PathBuf> {
    if let Some(sock) = &args.socket {
        return Ok(sock.clone());
    }
    if let Some(store) = &args.store {
        return Ok(store.join("gmcli.sock"));
    }
    // Mirror gmcli's paths.Resolve: XDG_STATE_HOME, else ~/.local/state.
    if let Ok(xdg) = std::env::var("XDG_STATE_HOME") {
        if !xdg.is_empty() {
            return Ok(PathBuf::from(xdg).join("gmcli").join("gmcli.sock"));
        }
    }
    let home = std::env::var("HOME").map_err(|_| eyre!("cannot determine home directory"))?;
    Ok(PathBuf::from(home)
        .join(".local")
        .join("state")
        .join("gmcli")
        .join("gmcli.sock"))
}

/// Dial the configured daemon and subscribe without launching a replacement.
async fn connect_daemon(
    socket: &Path,
) -> Result<(RpcClient, mpsc::UnboundedReceiver<ServerEvent>)> {
    let (client, server_events) = RpcClient::connect(socket).await.map_err(|e| eyre!("{e}"))?;
    client.subscribe().await.map_err(|e| eyre!("{e}"))?;
    Ok((client, server_events))
}

/// Background loop that keeps trying to restore the daemon connection.
/// Attaches the new event stream and hands the client to the main loop.
fn spawn_reconnect(socket: PathBuf, tx: mpsc::UnboundedSender<Event>) {
    tokio::spawn(async move {
        let mut delay = Duration::from_secs(1);
        loop {
            match connect_daemon(&socket).await {
                Ok((client, server_events)) => {
                    event::attach_server_events(tx.clone(), server_events);
                    let _ = tx.send(Event::Reconnected(client));
                    return;
                }
                Err(_) => {
                    tokio::time::sleep(delay).await;
                    delay = (delay * 2).min(Duration::from_secs(15));
                }
            }
        }
    });
}

#[tokio::main]
async fn main() -> Result<()> {
    color_eyre::install()?;
    let args = Args::parse();
    let socket = resolve_socket(&args)?;

    // Connect before touching the terminal so first-run connection errors
    // print normally instead of corrupting the screen.
    let (client, server_events) = connect_daemon(&socket).await?;

    let (event_tx, app_tx, mut events) = event::start();
    event::attach_server_events(event_tx.clone(), server_events);
    let mut app = App::new(client, app_tx);
    app.fetch_chats();
    app.fetch_status();
    app.fetch_approvals();
    // Catch up on anything the archive missed while no daemon was running;
    // the daemon broadcasts "refreshed" and we refetch.
    app.request_refresh();

    let mut terminal = ratatui::init();
    // Bracketed paste lets cookie blobs land in the pairing overlay intact.
    let _ = crossterm::execute!(std::io::stdout(), crossterm::event::EnableBracketedPaste);
    let result = run(&mut terminal, &mut app, &mut events, &socket, &event_tx).await;
    let _ = crossterm::execute!(std::io::stdout(), crossterm::event::DisableBracketedPaste);
    ratatui::restore();
    result
}

async fn run(
    terminal: &mut ratatui::DefaultTerminal,
    app: &mut App,
    events: &mut mpsc::UnboundedReceiver<Event>,
    socket: &Path,
    event_tx: &mpsc::UnboundedSender<Event>,
) -> Result<()> {
    while !app.should_quit {
        if app.take_bell() {
            // Terminal bell for incoming messages; terminals surface this
            // as a sound and/or a dock/tab attention marker.
            use std::io::Write;
            let mut out = std::io::stdout();
            let _ = out.write_all(b"\x07");
            let _ = out.flush();
        }
        terminal.draw(|frame| ui::render(frame, app))?;
        match events.recv().await {
            Some(Event::Tick) => app.on_tick(),
            Some(Event::Term(ev)) => app.handle_term_event(ev),
            Some(Event::App(ev)) => {
                app.handle_app_event(ev);
                // The daemon vanished: reconnect to its configured socket
                // after the service returns.
                if app.daemon_lost && !app.reconnecting {
                    app.reconnecting = true;
                    spawn_reconnect(socket.to_path_buf(), event_tx.clone());
                }
            }
            Some(Event::Reconnected(client)) => app.adopt_rpc(client),
            None => break,
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn unavailable_daemon_never_creates_a_replacement() {
        let directory =
            std::env::temp_dir().join(format!("gmtui-configured-{}", std::process::id()));
        std::fs::create_dir_all(&directory).unwrap();
        let socket = directory.join("gmcli.sock");
        assert!(connect_daemon(&socket).await.is_err());
        std::fs::write(&socket, b"stale socket").unwrap();
        assert!(connect_daemon(&socket).await.is_err());
        assert!(!directory.join("daemon.log").exists());
        std::fs::remove_file(socket).unwrap();
        std::fs::remove_dir(directory).unwrap();
    }

    #[tokio::test]
    async fn configured_daemon_receives_subscription() {
        use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
        let socket =
            std::env::temp_dir().join(format!("gmtui-subscription-{}.sock", std::process::id()));
        let listener = tokio::net::UnixListener::bind(&socket).unwrap();
        let peer = tokio::spawn(async move {
            let (connection, _) = listener.accept().await.unwrap();
            let mut connection = BufReader::new(connection);
            let mut line = String::new();
            connection.read_line(&mut line).await.unwrap();
            let request: serde_json::Value = serde_json::from_str(&line).unwrap();
            assert_eq!(request["method"], "subscribe");
            let response = serde_json::json!({"jsonrpc":"2.0","id":request["id"],"result":{"subscribed":true}});
            connection
                .get_mut()
                .write_all(format!("{response}\n").as_bytes())
                .await
                .unwrap();
        });
        let result = connect_daemon(&socket).await;
        assert!(result.is_ok());
        peer.await.unwrap();
        std::fs::remove_file(socket).unwrap();
    }
}
