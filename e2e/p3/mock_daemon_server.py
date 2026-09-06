#!/usr/bin/env python3
"""A minimal daemon-protocol server for e2e/p3/55 (daemon-protocol.md §2·§3·§4·§6).

It exists because the double-write property of FR-9.1 is about the DAEMON
BINARY dying, and nothing in-process can measure that. It speaks only what the
daemon calls: pair, probe, claim, phase, events, heartbeat, finish, workdirs.
No database, no real server — the point is the daemon, not the server.

State lands in --state as JSON lines so the shell script can assert on it.
"""
import json
import os
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

STATE = None
LOCK = threading.Lock()
QUEUE = []
GIVEN = set()
COMMANDS = []


def record(kind, body):
    with LOCK:
        with open(STATE, "a") as f:
            f.write(json.dumps({"kind": kind, "ts": time.time(), "body": body}) + "\n")


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _read(self):
        n = int(self.headers.get("Content-Length") or 0)
        if not n:
            return {}
        try:
            return json.loads(self.rfile.read(n) or b"{}")
        except Exception:
            return {}

    def _send(self, obj, code=200):
        b = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_POST(self):
        p = self.path
        body = self._read()
        if p.endswith("/pair"):
            record("pair", body)
            return self._send({"runtime_id": "rt-55", "daemon_token": "cdt_55"})
        if p.endswith("/probe"):
            record("probe", {"repos": body.get("repos"), "capabilities": [
                {"kind": c.get("kind"), "usage_midturn": c.get("usage_midturn")}
                for c in body.get("capabilities", [])]})
            return self._send({"ok": True})
        if p.endswith("/claim"):
            with LOCK:
                out = [b for b in QUEUE if b["task"]["id"] not in GIVEN]
                for b in out:
                    GIVEN.add(b["task"]["id"])
                cmds = list(COMMANDS)
            if out:
                record("claim", [b["task"]["id"] for b in out])
            return self._send({"tasks": out, "commands": cmds})
        if p.endswith("/phase"):
            record("phase", body)
            return self._send({"ok": True})
        if p.endswith("/events"):
            evs = body.get("events") or []
            record("events", [{"class": e.get("class"), "verb": e.get("verb"),
                               "outcome": e.get("outcome")} for e in evs])
            with LOCK:
                cmds = list(COMMANDS)
            return self._send({"accepted_seq_max": max([e.get("seq", 0) for e in evs] or [0]),
                               "commands": cmds})
        if p.endswith("/heartbeat"):
            with LOCK:
                cmds = list(COMMANDS)
            return self._send({"commands": cmds})
        if p.endswith("/finish"):
            record("finish", body)
            return self._send({"ok": True})
        if p.endswith("/workdirs"):
            record("workdirs", body)
            return self._send({"ok": True})
        record("unknown", {"path": p})
        return self._send({"ok": True})


def main():
    global STATE
    args = dict(zip(sys.argv[1::2], sys.argv[2::2]))
    port = int(args.get("--port", "8099"))
    STATE = args["--state"]
    q = args.get("--queue")
    if q and os.path.exists(q):
        QUEUE.extend(json.load(open(q)))
    open(STATE, "w").close()
    srv = ThreadingHTTPServer(("127.0.0.1", port), H)
    with open(args["--pid"], "w") as f:
        f.write(str(os.getpid()))
    srv.serve_forever()


if __name__ == "__main__":
    main()
