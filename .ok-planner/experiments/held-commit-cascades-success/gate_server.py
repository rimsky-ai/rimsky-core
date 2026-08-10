import json
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

arrived = threading.Event()
released = threading.Event()


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
        if self.path.startswith("/release"):
            released.set()
            self._json(200, {"released": True})
            return
        arrived.set()
        released.wait()
        self._json(200, {"ok": True})

    def do_GET(self):
        if self.path.startswith("/arrived"):
            self._json(200, {"arrived": arrived.is_set()})
            return
        self._json(404, {"error": "not found"})


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18801
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
