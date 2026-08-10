import json
import os
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOCK = threading.Lock()
LOG = []
HOST_ROOT = sys.argv[2]
CONTAINER_ROOT = "/workspace"


def host_path(addr):
    if not addr.startswith(CONTAINER_ROOT):
        raise ValueError("address %r is not under %s" % (addr, CONTAINER_ROOT))
    return HOST_ROOT + addr[len(CONTAINER_ROOT):]


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        if self.path == "/log":
            with LOCK:
                body = json.dumps(LOG).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(n).decode() if n else ""
        try:
            parsed = json.loads(raw) if raw else {}
        except ValueError:
            parsed = {}
        entry = {"path": self.path, "body": parsed}
        if self.path == "/write":
            target = os.path.join(host_path(parsed["held_addr"]), parsed["filename"])
            with open(target, "w") as fh:
                fh.write(parsed["content"])
            entry["wrote"] = target
        with LOCK:
            LOG.append(entry)
        body = b"{}"
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", int(sys.argv[1])), Handler).serve_forever()
