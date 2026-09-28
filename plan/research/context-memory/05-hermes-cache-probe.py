#!/usr/bin/env python3
"""hermes ACP 어댑터의 usage 보고를 wire 에서 실측한다 (T-HERMESCACHE).

`hermes acp` 를 데몬과 같은 방식(stdio JSON-RPC)으로 띄워 짧은 대화 2턴을 돌리고,
session/prompt 응답의 usage(inputTokens / cachedReadTokens / cachedWriteTokens / outputTokens)를
그대로 찍는다. 같은 세션의 2턴째가 1턴째 프롬프트를 캐시로 읽는지(=hermes 가 prompt caching 을
쓰는지)와, 어댑터가 어떤 칸을 채우는지를 본다.

실사용 방·데몬은 건드리지 않는다. ~/.hermes 설정은 읽기만 한다. 임시 작업 폴더에서 돈다.
"""
import json, os, subprocess, sys, tempfile, threading, time

MODEL = os.environ.get("HC_MODEL", "anthropic:claude-opus-5-5")
PAD = "이 문단은 캐시 접두를 만들기 위한 것이다. " * 200  # ≈ 5천 자


class ACP:
    def __init__(self, cwd):
        self.p = subprocess.Popen(["hermes", "acp"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, cwd=cwd, text=True, bufsize=1)
        self.id = 0
        self.resp = {}
        self.notes = []
        self.lock = threading.Condition()
        threading.Thread(target=self._read, daemon=True).start()
        threading.Thread(target=self._drain_err, daemon=True).start()

    def _drain_err(self):
        for line in self.p.stderr:
            if "error" in line.lower() or "Error" in line:
                print("[stderr]", line.rstrip()[:300], file=sys.stderr)

    def _read(self):
        for line in self.p.stdout:
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except Exception:
                continue
            with self.lock:
                if "id" in msg and ("result" in msg or "error" in msg):
                    self.resp[msg["id"]] = msg
                else:
                    self.notes.append(msg)
                    # 권한 요청이 오면 한 번만 허용한다(툴을 쓰지 않는 질문이라 보통 안 온다).
                    if msg.get("method") == "session/request_permission":
                        opts = msg["params"].get("options") or []
                        pick = next((o["optionId"] for o in opts if "allow" in o.get("optionId", "")), None)
                        if pick:
                            self._send({"jsonrpc": "2.0", "id": msg["id"],
                                        "result": {"outcome": {"outcome": "selected", "optionId": pick}}})
                self.lock.notify_all()

    def _send(self, obj):
        self.p.stdin.write(json.dumps(obj) + "\n")
        self.p.stdin.flush()

    def call(self, method, params, timeout=300):
        self.id += 1
        rid = self.id
        self._send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params})
        end = time.time() + timeout
        with self.lock:
            while rid not in self.resp:
                if not self.lock.wait(min(5, max(0.1, end - time.time()))):
                    if time.time() > end:
                        raise TimeoutError(method)
            msg = self.resp.pop(rid)
        if "error" in msg:
            raise RuntimeError(f"{method}: {msg['error']}")
        return msg["result"]


def main():
    cwd = tempfile.mkdtemp(prefix="hermes-cache-probe-")
    a = ACP(cwd)
    a.call("initialize", {"protocolVersion": 1,
                          "clientCapabilities": {"fs": {"readTextFile": False, "writeTextFile": False}}})
    s = a.call("session/new", {"cwd": cwd, "mcpServers": []})
    sid = s["sessionId"]
    try:
        a.call("session/set_model", {"sessionId": sid, "modelId": MODEL})
    except Exception as e:
        print("set_model failed (프로파일 기본 모델로 계속):", e, file=sys.stderr)
    out = {"cwd": cwd, "session": sid, "model": MODEL, "turns": []}
    prompts = [
        f"{PAD}\n\n위 문단은 무시해. 딱 한 단어로만 답해: 사과는 무슨 색인가?",
        "딱 한 단어로만 답해: 방금 내가 물어본 과일의 이름은?",
    ]
    for i, text in enumerate(prompts, 1):
        r = a.call("session/prompt", {"sessionId": sid, "prompt": [{"type": "text", "text": text}]})
        out["turns"].append({"turn": i, "chars": len(text), "stopReason": r.get("stopReason"),
                             "usage": r.get("usage"), "_meta": r.get("_meta")})
        print(f"turn {i}: usage={json.dumps(r.get('usage'), ensure_ascii=False)}")
    json.dump(out, open(os.environ.get("HC_OUT", "/tmp/hermes-cache-probe.json"), "w"),
              ensure_ascii=False, indent=1)
    a.p.terminate()


if __name__ == "__main__":
    main()
