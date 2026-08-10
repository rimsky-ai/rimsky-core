import http.server
import json
import os
import threading

RETRY_AFTER = os.environ.get("RETRY_AFTER", "3600")

STATE = {"served": 0, "log": []}
LOCK = threading.Lock()


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/_log"):
            self.serve_log()
            return
        self.respond(b"")

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b""
        self.respond(raw)

    def serve_log(self):
        with LOCK:
            body = json.dumps({"requests": STATE["log"]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def respond(self, raw):
        try:
            parsed = json.loads(raw.decode()) if raw else None
        except ValueError:
            parsed = raw.decode(errors="replace")
        with LOCK:
            STATE["served"] += 1
            served = STATE["served"]
            STATE["log"].append({"n": served, "path": self.path, "body": parsed})
        if served == 1:
            self.send_response(429)
            self.send_header("Retry-After", RETRY_AFTER)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        body = json.dumps({"served": served}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        return


http.server.ThreadingHTTPServer(("0.0.0.0", 8000), Handler).serve_forever()
