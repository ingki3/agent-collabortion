#!/usr/bin/env python3
"""plan/spikes/spike04c/hermes_wire.py — Hermes `session/load` 원시 응답을 본다 (E8-03 근거).

데몬을 거치지 않고 `hermes acp` 와 직접 stdio JSON-RPC 로 말한다. 순서:
  1) 새 프로세스 → initialize → session/new → 턴 1
  2) 프로세스 종료 → 새 프로세스 → session/load (대조군: 세션 살아 있음)
  3) ~/.hermes/state.db 에서 그 세션 행 삭제 (hermes_forget.py) → 새 프로세스 → session/load
각 단계의 응답 전문을 stdout 에 JSON 으로 찍는다.
"""
import json, os, subprocess, sys, tempfile, time

WORK = tempfile.mkdtemp(prefix="s4c-wire-")


class Conn:
    def __init__(self):
        self.p = subprocess.Popen(["hermes", "acp"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=open(os.path.join(WORK, "stderr.log"), "ab"), text=True, bufsize=1)
        self.n = 0

    def call(self, method, params, timeout=180):
        self.n += 1
        mid = self.n
        self.p.stdin.write(json.dumps({"jsonrpc": "2.0", "id": mid, "method": method, "params": params}) + "\n")
        self.p.stdin.flush()
        end = time.time() + timeout
        while time.time() < end:
            line = self.p.stdout.readline()
            if not line:
                return {"error": "eof"}
            try:
                m = json.loads(line)
            except Exception:
                continue
            if m.get("id") == mid:
                return m
        return {"error": "timeout"}

    def close(self):
        try:
            self.p.stdin.close()
            self.p.terminate()
            self.p.wait(5)
        except Exception:
            self.p.kill()


INIT = {"protocolVersion": 1, "clientCapabilities": {"fs": {"readTextFile": False, "writeTextFile": False}, "terminal": False}}
out = {}

c = Conn()
out["initialize"] = c.call("initialize", INIT)
r = c.call("session/new", {"cwd": WORK, "mcpServers": []})
sid = (r.get("result") or {}).get("sessionId")
out["session/new"] = r
c.call("session/set_model", {"sessionId": sid, "model": "anthropic:claude-haiku-4-5-20251001"})
out["turn"] = c.call("session/prompt", {"sessionId": sid, "prompt": [{"type": "text", "text": "Reply with exactly: PONG"}]})
c.close()

c = Conn()
c.call("initialize", INIT)
out["load_control"] = c.call("session/load", {"sessionId": sid, "cwd": WORK, "mcpServers": []})
c.close()

forget = subprocess.run([sys.executable, os.path.join(os.path.dirname(__file__), "hermes_forget.py"), sid],
                        capture_output=True, text=True)
out["forget"] = forget.stdout.strip() + forget.stderr.strip()

c = Conn()
c.call("initialize", INIT)
out["load_after_delete"] = c.call("session/load", {"sessionId": sid, "cwd": WORK, "mcpServers": []})
out["turn_after_delete"] = c.call("session/prompt", {"sessionId": sid, "prompt": [{"type": "text", "text": "What did I ask you to reply with in my first message? Answer in one word."}]})
c.close()

out["session_id"] = sid
out["workdir"] = WORK
print(json.dumps(out, ensure_ascii=False, indent=2))
