#!/usr/bin/env python3
"""plan/spikes/spike05/acp.py — 데몬을 거치지 않고 런타임과 직접 ACP(stdio JSON-RPC)로 말한다.

스파이크 5 는 데몬의 §10 코드(P4 T-D9)가 아직 없는 상태에서 **런타임이 지시 파일을
읽는가**만 재야 하므로, `contracts/harness.md` §2·§3 의 시퀀스를 그대로 흉내 내는
최소 클라이언트를 쓴다.

  claude_code : npx -y @agentclientprotocol/claude-agent-acp@0.74.0
                initialize → session/new(_meta §3) → session/set_config_option(model) → session/prompt
  hermes      : hermes acp
                initialize → session/new → session/set_model("anthropic:<model>") → session/prompt

권한 요청(`session/request_permission`)은 §4 대로 `allow_once` **kind** 로 고른다.
"""
import json, os, subprocess, threading, queue, time

ADAPTER_PIN = "0.74.0"
INIT_PARAMS = {
    "protocolVersion": 1,
    "clientCapabilities": {"fs": {"readTextFile": False, "writeTextFile": False}, "terminal": False},
}


def claude_meta(brief=None, setting_sources=None):
    """harness §3 `_meta`. setting_sources 는 이 스파이크의 변수다(계약 기본값은 [])."""
    opts = {
        "settingSources": [] if setting_sources is None else list(setting_sources),
        "strictMcpConfig": True,
        "disallowedTools": ["AskUserQuestion"],
        "settings": {"permissions": {"deny": []}},
        "permissionMode": "default",
    }
    meta = {"claudeCode": {"options": opts}}
    if brief is not None:
        meta["systemPrompt"] = {"append": brief}
    return meta


class Runtime:
    """한 런타임 프로세스 = 한 attempt (harness §2)."""

    def __init__(self, kind, cwd, log_path, model, meta=None):
        self.kind = kind
        self.cwd = cwd
        self.model = model
        self.meta = meta
        self.updates = []          # session/update 알림 전부
        self.permissions = []      # 우리가 응답한 권한 요청
        self._id = 0
        self._replies = {}
        self._lock = threading.Lock()
        self._q = queue.Queue()
        cmd = (["npx", "-y", f"@agentclientprotocol/claude-agent-acp@{ADAPTER_PIN}"]
               if kind == "claude_code" else ["hermes", "acp"])
        self.stderr = open(log_path, "ab")
        self.p = subprocess.Popen(cmd, cwd=cwd, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=self.stderr, text=True, bufsize=1)
        self._reader = threading.Thread(target=self._read_loop, daemon=True)
        self._reader.start()

    # ---- 배관 ----
    def _send(self, obj):
        with self._lock:
            self.p.stdin.write(json.dumps(obj, ensure_ascii=False) + "\n")
            self.p.stdin.flush()

    def _read_loop(self):
        for line in self.p.stdout:
            line = line.strip()
            if not line:
                continue
            try:
                m = json.loads(line)
            except Exception:
                continue
            if "id" in m and "method" in m:          # 에이전트 → 클라이언트 요청
                self._handle_request(m)
            elif "method" in m:                        # 알림
                if m["method"] == "session/update":
                    self.updates.append(m.get("params") or {})
            else:                                      # 응답
                self._replies[m.get("id")] = m
                self._q.put(m.get("id"))

    def _handle_request(self, m):
        method, params = m["method"], (m.get("params") or {})
        if method == "session/request_permission":
            opts = params.get("options") or []
            pick = next((o for o in opts if o.get("kind") == "allow_once"), None) \
                or next((o for o in opts if o.get("kind") == "allow_always"), None) \
                or (opts[0] if opts else None)
            self.permissions.append({"tool": (params.get("toolCall") or {}).get("title"),
                                     "kinds": [o.get("kind") for o in opts],
                                     "picked": (pick or {}).get("kind")})
            res = ({"outcome": {"outcome": "selected", "optionId": pick["optionId"]}} if pick
                   else {"outcome": {"outcome": "cancelled"}})
            self._send({"jsonrpc": "2.0", "id": m["id"], "result": res})
            return
        self._send({"jsonrpc": "2.0", "id": m["id"],
                    "error": {"code": -32601, "message": f"unsupported {method}"}})

    def call(self, method, params, timeout=300):
        self._id += 1
        mid = self._id
        self._send({"jsonrpc": "2.0", "id": mid, "method": method, "params": params})
        end = time.time() + timeout
        while time.time() < end:
            if mid in self._replies:
                return self._replies.pop(mid)
            try:
                self._q.get(timeout=1)
            except queue.Empty:
                if self.p.poll() is not None:
                    return {"error": {"message": f"runtime exited rc={self.p.returncode}"}}
        return {"error": {"message": "timeout"}}

    # ---- §2 시퀀스 ----
    def start(self):
        out = {"initialize": self.call("initialize", INIT_PARAMS, timeout=180)}
        params = {"cwd": self.cwd, "mcpServers": []}
        if self.kind == "claude_code" and self.meta is not None:
            params["_meta"] = self.meta
        r = self.call("session/new", params, timeout=240)
        out["session/new"] = r
        self.session_id = ((r.get("result") or {}).get("sessionId"))
        if not self.session_id:
            return out
        if self.kind == "claude_code":
            out["set_model"] = self.call("session/set_config_option",
                                         {"sessionId": self.session_id, "configId": "model", "value": self.model})
        else:
            out["set_model"] = self.call("session/set_model",
                                         {"sessionId": self.session_id, "modelId": f"anthropic:{self.model}"})
        return out

    def prompt(self, text, timeout=420):
        mark = len(self.updates)
        r = self.call("session/prompt", {"sessionId": self.session_id,
                                         "prompt": [{"type": "text", "text": text}]}, timeout=timeout)
        return {"reply": r, "text": self.assistant_text(mark), "tools": self.tool_calls(mark)}

    # ---- 관측 ----
    def assistant_text(self, since=0):
        buf = []
        for u in self.updates[since:]:
            up = u.get("update") or {}
            if up.get("sessionUpdate") == "agent_message_chunk":
                c = up.get("content") or {}
                if c.get("type") == "text":
                    buf.append(c.get("text") or "")
        return "".join(buf)

    def tool_calls(self, since=0):
        out = []
        for u in self.updates[since:]:
            up = u.get("update") or {}
            if up.get("sessionUpdate") in ("tool_call", "tool_call_update"):
                out.append({"kind": up.get("kind"), "title": up.get("title"),
                            "status": up.get("status"), "raw": up.get("rawInput")})
        return out

    def close(self):
        try:
            self.p.stdin.close()
        except Exception:
            pass
        try:
            self.p.terminate(); self.p.wait(10)
        except Exception:
            try:
                self.p.kill()
            except Exception:
                pass
        try:
            self.stderr.close()
        except Exception:
            pass
