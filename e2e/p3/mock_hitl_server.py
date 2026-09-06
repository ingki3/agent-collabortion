#!/usr/bin/env python3
"""Mock of the one server operation the P3 CLI HITL commands call.

`POST /api/v1/tasks/{T}/hitl` — openapi.yaml `createHitlRequest`. The real
handler lands with T-S5 (PR #124, still open at the time of writing), so this
smoke runs against the CONTRACT: the 201 shape, the 409 `hitl_already_open`
for a task's second open request (E7-04), and the 422 a missing
`proposed_default` would get if the CLI ever stopped catching it first
(E7-05 · E7-20).

Every request is appended to $CAPTURE as one JSON line so the smoke can assert
what the CLI actually sent — and, just as important, that it sent nothing at
all when a flag check should have stopped it.

`POST /__mock/reset` clears the task's open request and truncates the capture,
so one server serves the whole smoke: a task holds ONE open HITL (E7-04), and
without a reset every case after the first would only ever see the 409.

  usage: mock_hitl_server.py <port> <capture-file>
"""
import json
import os
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

TOKEN = "ctk_e2e_p3_smoke_token"
TASK_ID = "11111111-1111-4111-8111-111111111111"
SESSION_ID = "33333333-3333-4333-8333-333333333333"
LANE_ID = "22222222-2222-4222-8222-222222222222"
AGENT_ID = "44444444-4444-4444-8444-444444444444"
HITL_ID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
MESSAGE_ID = "99999999-9999-4999-8999-999999999999"

CAPTURE = sys.argv[2]
STATE = {"open_hitl_id": None}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):  # keep the smoke's output readable
        pass

    def _send(self, status, obj, content_type="application/json"):
        body = json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _problem(self, status, code, title, detail):
        self._send(status, {
            "type": "https://colab.dev/problems/" + code.replace("_", "-"),
            "title": title, "status": status, "code": code, "detail": detail,
        }, "application/problem+json")

    def do_POST(self):
        raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
        if self.path == "/__mock/reset":
            STATE["open_hitl_id"] = None
            open(CAPTURE, "w").close()
            return self._send(200, {"reset": True})
        try:
            body = json.loads(raw or b"{}")
        except ValueError:
            body = {"__unparseable__": raw.decode("utf-8", "replace")}
        with open(CAPTURE, "a") as f:
            f.write(json.dumps({"path": self.path, "body": body,
                                "idempotency_key": self.headers.get("Idempotency-Key", "")},
                               ensure_ascii=False) + "\n")

        if self.headers.get("Authorization") != "Bearer " + TOKEN:
            return self._problem(401, "unauthorized", "Unauthorized", "missing or invalid task token")
        if self.path != "/api/v1/tasks/%s/hitl" % TASK_ID:
            return self._problem(404, "not_found", "Not found", "no such route: " + self.path)

        # One open request per task (E7-04) — checked before the body, the way
        # the server does: the second request is refused whatever it says.
        if STATE["open_hitl_id"]:
            return self._problem(409, "hitl_already_open", "이미 대기 중인 요청이 있다",
                                 "이 task 에는 이미 열린 HITL 요청 %s 이 있다. 첫 요청이 유지된다 — 답을 기다려라."
                                 % STATE["open_hitl_id"])

        typ = body.get("type")
        if typ in ("question", "choice"):
            if not body.get("proposed_default"):
                return self._problem(422, "validation_failed", "Validation failed",
                                     "proposed_default is required for " + typ)
            if typ == "choice" and len(body.get("options") or []) < 2:
                return self._problem(422, "validation_failed", "Validation failed",
                                     "options needs at least 2 items")
            question = body.get("question", "")
        elif typ == "approval":
            question = body.get("summary", "")
        elif typ == "info":
            question = body.get("what", "")
        else:
            return self._problem(422, "validation_failed", "Validation failed", "unknown hitl type %r" % typ)

        STATE["open_hitl_id"] = HITL_ID
        self._send(201, {
            "hitl_request": {
                "id": HITL_ID, "session_id": SESSION_ID, "task_id": TASK_ID, "lane_id": LANE_ID,
                "agent": {"id": AGENT_ID, "name": "Researcher"},
                "source": "agent", "type": typ, "question": question,
                "context": body.get("context"), "options": body.get("options") or [],
                "proposed_default": body.get("proposed_default"),
                "artifact_id": body.get("artifact_id"),
                "purpose": "agent", "approver_spec": "director",
                "due_at": "2026-09-08T00:00:00Z", "overdue": False, "status": "open",
                "approved": None, "answer": None, "answered_by": None, "answered_at": None,
                "can_respond": False, "can_respond_from": None,
                "message_id": MESSAGE_ID, "created_at": "2026-09-07T00:00:00Z",
            },
            "turn_end_required": True,
            "message_id": MESSAGE_ID,
        })


if __name__ == "__main__":
    open(CAPTURE, "w").close()
    srv = HTTPServer(("127.0.0.1", int(sys.argv[1])), Handler)
    sys.stderr.write("mock hitl server on %d (pid %d)\n" % (srv.server_address[1], os.getpid()))
    sys.stderr.flush()
    srv.serve_forever()
