import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOCK = threading.Lock()
RECEIVED = []


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        return

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length)
        entry = {"path": self.path,
                 "authorization": self.headers.get("Authorization") or "",
                 "content_type": self.headers.get("Content-Type") or "",
                 "event": json.loads(raw.decode()) if raw else None}
        with LOCK:
            RECEIVED.append(entry)
        self.send_response(200)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_GET(self):
        with LOCK:
            body = json.dumps(RECEIVED).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
