import json
import subprocess
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOCK = threading.Lock()
LOG = []
PG_CONTAINER = sys.argv[2]


def psql(sql):
    return subprocess.run(
        ["docker", "exec", "-i", PG_CONTAINER, "psql", "-U", "store", "-d", "storedb",
         "-v", "ON_ERROR_STOP=1", "-c", sql],
        capture_output=True, text=True)


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
        status = 200
        if self.path == "/stage":
            schema = parsed["staging_schema"]
            rows = int(parsed["rows"])
            values = ", ".join("(%d, 'row-%d')" % (i, i) for i in range(1, rows + 1))
            sql = ('CREATE TABLE "%s".items (id INT PRIMARY KEY, label TEXT); '
                   'INSERT INTO "%s".items (id, label) VALUES %s;' % (schema, schema, values))
            res = psql(sql)
            entry["sql_rc"] = res.returncode
            entry["sql_err"] = res.stderr.strip()[-400:]
            if res.returncode != 0:
                status = 500
        with LOCK:
            LOG.append(entry)
        body = b"{}"
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", int(sys.argv[1])), Handler).serve_forever()
