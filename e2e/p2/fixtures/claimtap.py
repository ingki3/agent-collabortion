#!/usr/bin/env python3
"""e2e/p2/fixtures/claimtap.py — 데몬↔서버 사이에 끼워 **claim 응답(TaskBundle)** 을 기록하는 테스트 픽스처.

구현 코드는 건드리지 않는다. 데몬을 `--server http://localhost:<LISTEN>` 로 붙이면 모든 요청을 그대로
서버에 넘기고, `/v1/daemon/*/claim` 의 응답 본문만 JSONL 로 남긴다. E1-21("합류 묶음 페이로드에
자식 메시지가 실린다")은 서버가 데몬에 보내는 턴 프롬프트를 봐야만 증명되는데, 그 프롬프트는
디스크에 남지 않기 때문이다(claude_code 는 `_meta.systemPrompt` + `session/prompt` 로 전달).

사용: python3 claimtap.py <listen_port> <upstream_url> <out.jsonl>
"""
import json, sys, threading, time, urllib.error, urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LISTEN, UPSTREAM, OUTFILE = int(sys.argv[1]), sys.argv[2].rstrip("/"), sys.argv[3]
ACCESS = OUTFILE.rsplit(".", 1)[0] + "-access.tsv"
_lock = threading.Lock()
HOP = {"host", "connection", "keep-alive", "transfer-encoding", "content-length"}


def record(path, body):
    try:
        parsed = json.loads(body)
    except Exception:
        return
    with _lock, open(OUTFILE, "a") as f:
        f.write(json.dumps({"at": time.time(), "path": path, "body": parsed}, ensure_ascii=False) + "\n")


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def proxy(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n) if n else None
        req = urllib.request.Request(UPSTREAM + self.path, data=body, method=self.command)
        for k, v in self.headers.items():
            if k.lower() not in HOP:
                req.add_header(k, v)
        try:
            with urllib.request.urlopen(req, timeout=300) as r:
                status, headers, out = r.status, list(r.headers.items()), r.read()
        except urllib.error.HTTPError as e:
            status, headers, out = e.code, list(e.headers.items()), e.read()
        except Exception as e:  # upstream gone
            self.send_response(502)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        if self.path.endswith("/claim") and status == 200:
            record(self.path, out.decode("utf-8", "replace"))
        with _lock, open(ACCESS, "a") as f:
            f.write("%.3f\t%s\t%s\t%d\n" % (time.time(), self.command, self.path, status))
        self.send_response(status)
        for k, v in headers:
            if k.lower() not in HOP:
                self.send_header(k, v)
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = proxy


ThreadingHTTPServer.daemon_threads = True
ThreadingHTTPServer(("127.0.0.1", LISTEN), H).serve_forever()
