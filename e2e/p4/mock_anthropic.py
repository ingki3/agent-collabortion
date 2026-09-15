#!/usr/bin/env python3
"""e2e/p4/mock_anthropic.py — §8.5 플랫폼 LLM(Messages API) 목 엔드포인트.

구현 코드는 건드리지 않는다. 서버의 `llm.FromEnv` 는 `ANTHROPIC_BASE_URL` 로 주소를 바꿀 수
있게 열려 있고("It is overridable so the isolated stack can point at a stub without an API key
leaving the machine"), 요약은 그 클라이언트를 통해서만 나간다. 여기서 세 가지 답을 낸다.

  --mode refusal    stop_reason=refusal + stop_details.category (E6-11)
  --mode transport  HTTP 500 (전송 오류 — stop_reason 자체가 없다, E6-12)
  --mode ok         stop_reason=end_turn + 본문 (요약이 실제로 이 경로로 온다는 증거)

사용: python3 mock_anthropic.py --port 8117 --mode refusal --log <file>
"""
import argparse, json, sys, threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ap = argparse.ArgumentParser()
ap.add_argument("--port", type=int, required=True)
ap.add_argument("--mode", default="refusal", choices=["refusal", "transport", "ok"])
ap.add_argument("--log", default="")
A = ap.parse_args()
_lock = threading.Lock()
_calls = [0]


def note(obj):
    if not A.log:
        return
    with _lock, open(A.log, "a") as f:
        f.write(json.dumps(obj, ensure_ascii=False) + "\n")


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def do_GET(self):
        self.send_response(200); self.send_header("content-length", "2"); self.end_headers()
        self.wfile.write(b"ok")

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(n) if n else b"{}"
        try:
            body = json.loads(raw)
        except Exception:
            body = {}
        with _lock:
            _calls[0] += 1
        note({"path": self.path, "mode": A.mode, "call": _calls[0],
              "model": body.get("model"), "stream": body.get("stream"),
              "has_cache_control": "cache_control" in raw.decode("utf-8", "replace"),
              "beta": self.headers.get("anthropic-beta"),
              "api_key_present": bool(self.headers.get("x-api-key"))})
        if A.mode == "transport":
            payload = json.dumps({"type": "error", "error": {"type": "api_error",
                                  "message": "upstream exploded (mock)"}}).encode()
            self.send_response(500)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(payload)))
            self.end_headers(); self.wfile.write(payload); return
        if A.mode == "refusal":
            out = {"stop_reason": "refusal",
                   "stop_details": {"category": "policy_violation"},
                   # 거절도 본문을 싣는다 — 순서를 틀리면 이 문장이 요약이 된다(§8.5 폴백 행).
                   "content": [{"type": "text", "text": "I can't help with that."}],
                   "usage": {"input_tokens": 10, "output_tokens": 5,
                             "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}}
        else:
            out = {"stop_reason": "end_turn",
                   "content": [{"type": "text", "text": "MOCK-SUMMARY-8421\n\n### 결정 기록\n- 없음\n### 아티팩트\n- 없음\n### 비용\n- $0\n### 타임라인\n- 시작/종료"}],
                   "usage": {"input_tokens": 10, "output_tokens": 40,
                             "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}}
        payload = json.dumps(out, ensure_ascii=False).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(payload)))
        self.end_headers(); self.wfile.write(payload)


ThreadingHTTPServer(("127.0.0.1", A.port), H).serve_forever()
