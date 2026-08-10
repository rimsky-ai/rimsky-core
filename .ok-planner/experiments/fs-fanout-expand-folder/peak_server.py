import json
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HOLD_SECONDS = 1.5
state = {"inflight": 0, "peak": 0, "paths": []}
lock = threading.Lock()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def _json(self, code, body):
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        if length:
            self.rfile.read(length)
        with lock:
            state["inflight"] += 1
            state["peak"] = max(state["peak"], state["inflight"])
            state["paths"].append(self.path)
        time.sleep(HOLD_SECONDS)
        with lock:
            state["inflight"] -= 1
        self._json(200, {"ok": True})

    def do_GET(self):
        if self.path.startswith("/seen"):
            with lock:
                self._json(200, {"peak": state["peak"], "paths": sorted(state["paths"])})
            return
        self._json(404, {"error": "not found"})


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18802
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
