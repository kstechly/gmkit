#!/usr/bin/env -S uv run --script
"""Observe a live gmcli daemon without logging message bodies or credentials."""
import argparse
import datetime
import json
import os
import socket
import subprocess
import time
import urllib.request


def rpc(method):
    with socket.socket(socket.AF_UNIX) as client:
        client.settimeout(10)
        root = os.environ.get("GMCLI_STORE", os.path.expanduser("~/.local/state/gmcli"))
        client.connect(root + "/gmcli.sock")
        client.sendall((json.dumps({"jsonrpc": "2.0", "id": 1, "method": method}) + "\n").encode())
        response = json.loads(client.makefile().readline())
        if "error" in response:
            raise RuntimeError(response["error"]["message"])
        return response["result"]


def report(**fields):
    print(json.dumps({"at": datetime.datetime.now(datetime.timezone.utc).isoformat(), **fields}), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--conversation", required=True)
    parser.add_argument("--duration", type=float, default=86400)
    parser.add_argument("--interval", type=float, default=300)
    parser.add_argument("--read-interval", type=float, default=300)
    args = parser.parse_args()
    chrome = os.environ.get("GMCLI_CHROME_CDP", "http://127.0.0.1:9222")
    started_wall = time.time()
    healthy_since = None
    last_read = float("-inf")
    reads = 0
    report(event="start", required_healthy_seconds=args.duration)
    while True:
        try:
            with urllib.request.urlopen(chrome + "/json/version", timeout=5):
                pass
        except OSError:
            healthy_since = None
            report(event="waiting_for_chrome")
            time.sleep(args.interval)
            continue
        journal = subprocess.run(["journalctl", "--user", "-u", "gmcli-serve.service", "--since", f"@{started_wall:.6f}", "-o", "json", "--no-pager"], capture_output=True, text=True, check=True)
        for line in journal.stdout.splitlines():
            message = json.loads(line).get("MESSAGE", "")
            if any(failure in message for failure in (
                "HTTP 401", "HTTP 403", "Google rejected the pairing",
                "Failed to decode incoming RPC message",
                "cookie input missing required Google cookies",
            )):
                raise RuntimeError("journal recorded an authentication or incoming message failure")
        status = rpc("status")
        if status["auth_expired"] or not status["connected"]:
            raise RuntimeError("daemon lost its authenticated connection")
        now = time.monotonic()
        if healthy_since is None:
            healthy_since = now
        if now - last_read >= args.read_interval or now - healthy_since >= args.duration:
            result = subprocess.run(
                ["gmcli", "--json", "chats", "show", args.conversation, "--limit", "1"],
                capture_output=True, text=True, timeout=75, check=True,
            )
            data = json.loads(result.stdout)
            if data["coverage"]["source"] != "live" or not data["data"]["messages"]:
                raise RuntimeError("no live message returned")
            reads += 1
            last_read = time.monotonic()
            report(event="live_read", message_id=data["data"]["messages"][0]["message_id"], reads=reads)
        elapsed = time.monotonic() - healthy_since
        report(event="healthy", seconds=round(elapsed), reads=reads)
        if elapsed >= args.duration:
            report(event="PAIRING_OBSERVATION_PASSED", healthy_seconds=round(elapsed), reads=reads)
            return
        time.sleep(min(args.interval, args.duration - elapsed))


if __name__ == "__main__":
    main()
