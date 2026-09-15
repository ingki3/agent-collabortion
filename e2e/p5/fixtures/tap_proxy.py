#!/usr/bin/env python3
"""tap_proxy.py LISTEN_PORT UPSTREAM_URL LOG — a see-through HTTP proxy that
appends one line per request ("METHOD PATH -> STATUS") to LOG.

The server keeps no access log, and "the CLI refused before sending" is a
claim about the WIRE, not about what the server stored — so the T-C7 smoke
(81_cli_allowed_commands.sh) points COLAB_SERVER_URL at this tap and counts
lines. It is deliberately dumb: no keep-alive, no streaming, headers copied
both ways, body buffered (CLI traffic is small)."""
import sys, http.server, urllib.request, urllib.error

port, upstream, log = int(sys.argv[1]), sys.argv[2].rstrip("/"), sys.argv[3]

class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def log_message(self, *a): pass
    def _do(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n) if n else None
        req = urllib.request.Request(upstream + self.path, data=body, method=self.command)
        for k, v in self.headers.items():
            if k.lower() not in ("host", "content-length", "connection", "accept-encoding"):
                req.add_header(k, v)
        try:
            res = urllib.request.urlopen(req, timeout=30)
            status, hdrs, data = res.status, res.headers, res.read()
        except urllib.error.HTTPError as e:
            status, hdrs, data = e.code, e.headers, e.read()
        with open(log, "a") as f:
            f.write(f"{self.command} {self.path} -> {status}\n")
        self.send_response(status)
        for k, v in hdrs.items():
            if k.lower() not in ("transfer-encoding", "content-length", "connection"):
                self.send_header(k, v)
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(data)
    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = _do

http.server.ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
