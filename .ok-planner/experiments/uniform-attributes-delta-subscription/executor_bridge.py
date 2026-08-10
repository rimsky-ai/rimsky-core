import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

PORT = int(sys.argv[1])


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        request = json.loads(self.rfile.read(length) or b"{}")
        node_type = request.get("nodeType") or request.get("node_type") or ""
        verdict = "red" if node_type.endswith("_red") else "green"
        if node_type.startswith("err"):
            outcome = {"error": {"errorClass": "probe/refused",
                                 "attributesDelta": {"verdict": verdict}}}
        else:
            outcome = {"success": {"changed": True, "changeSummary": "probe verdict",
                                   "attributesDelta": {"verdict": verdict}}}
        body = json.dumps(outcome).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


HTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
