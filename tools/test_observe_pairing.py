import contextlib
import io
import json
import types
import unittest
from unittest.mock import patch

import observe_pairing


class PairingObservationTest(unittest.TestCase):
    def observe(self, journal_message):
        def run(command, **kwargs):
            if command[0] == "journalctl":
                return types.SimpleNamespace(stdout=json.dumps({"MESSAGE": journal_message}))
            self.assertEqual(command[:4], ["gmcli", "--json", "chats", "show"])
            return types.SimpleNamespace(stdout=json.dumps({
                "coverage": {"source": "live"},
                "data": {"messages": [{"message_id": "recent"}]},
            }))

        with (
            patch("sys.argv", ["observe_pairing", "--conversation", "369", "--duration", "0"]),
            patch.object(observe_pairing.urllib.request, "urlopen", return_value=contextlib.nullcontext()),
            patch.object(observe_pairing, "rpc", return_value={"connected": True, "auth_expired": False}),
            patch.object(observe_pairing.subprocess, "run", side_effect=run),
            contextlib.redirect_stdout(io.StringIO()) as output,
        ):
            observe_pairing.main()
            return [json.loads(line) for line in output.getvalue().splitlines()]

    def test_incoming_failure_rejects_healthy_transport(self):
        for message in (
            "Failed to decode incoming RPC message: HMAC mismatch",
            "cookie input missing required Google cookies: OSID",
            "Failed to send acks HTTP 401",
        ):
            with self.subTest(message=message), self.assertRaises(RuntimeError):
                self.observe(message)

    def test_colored_failure_rejects_connected_transport(self):
        # journalctl -o json gives ESC-colored records as byte arrays.
        for message in (
            "\x1b[1mFailed to decode incoming RPC message\x1b[0m error=HMAC mismatch",
            "\x1b[31mERR\x1b[0m cookie input missing required Google cookies: OSID",
        ):
            with self.subTest(message=message), self.assertRaises(RuntimeError):
                self.observe(list(message.encode()))

    def test_success_requires_live_read(self):
        events = self.observe("Phone responding again")
        self.assertTrue(any(event["event"] == "live_read" for event in events))
        self.assertEqual(events[-1]["event"], "PAIRING_OBSERVATION_PASSED")


if __name__ == "__main__":
    unittest.main()
