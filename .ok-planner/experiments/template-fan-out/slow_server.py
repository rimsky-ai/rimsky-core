#!/usr/bin/env python3
"""Concurrency-observing endpoint for the fan-out experiment.

POST /slow  holds the request open for HOLD_SECONDS and records how many
            requests were in flight at once.
POST /fail  same, but answers 500 so every fan-out clone errors.
GET  /peak  returns {"peak": N, "served": M}.
"""
import json
import os
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HOLD_SECONDS = float(os.environ.get("HOLD_SECONDS", "1.5"))

state = {"inflight": 0, "peak": 0, "served": 0}
lock = threading.Lock()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def _hold(self):
        with lock:
            state["inflight"] += 1
            if state["inflight"] > state["peak"]:
                state["peak"] = state["inflight"]
        time.sleep(HOLD_SECONDS)
        with lock:
            state["inflight"] -= 1
            state["served"] += 1

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
        if self.path.startswith("/fail"):
            self._hold()
            self._json(500, {"class": "downstream_refused"})
            return
        self._hold()
        self._json(200, {"ok": True})

    def do_GET(self):
        if self.path.startswith("/peak"):
            with lock:
                self._json(200, {"peak": state["peak"], "served": state["served"]})
            return
        self._json(404, {"error": "not found"})


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18999
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
