import http.server
import json
import threading

STATE = {"served": 0}
LOCK = threading.Lock()


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.respond()

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        if length:
            self.rfile.read(length)
        self.respond()

    def respond(self):
        with LOCK:
            STATE["served"] += 1
            served = STATE["served"]
        if served == 1:
            self.send_response(429)
            self.send_header("Retry-After", "3600")
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
