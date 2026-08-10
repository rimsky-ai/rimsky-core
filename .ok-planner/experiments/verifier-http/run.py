import json
import os
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

IMAGE = "rimsky-all-in-one:" + os.environ.get("RIMSKY_IMAGE_TAG", "latest")
STATE = {"base": None, "container": None, "checks": []}
SETTLED = ("completed", "failed", "terminated")


def die(msg):
    print("HARNESS ERROR: " + msg)
    teardown()
    sys.exit(2)


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def boot():
    if docker("image", "inspect", IMAGE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % IMAGE)
    port = free_port()
    name = "rimsky-exp-" + uuid.uuid4().hex[:8]
    res = docker("run", "-d", "--name", name, "-p", "127.0.0.1:%d:8080" % port, IMAGE)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
    STATE["container"] = name
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                return
        except Exception:
            pass
        time.sleep(0.3)


def teardown():
    if STATE["container"]:
        docker("rm", "-f", STATE["container"])
        STATE["container"] = None


def call(method, path, body=None, headers=None):
    data = None if body is None else json.dumps(body).encode()
    hdrs = {"Content-Type": "application/json"}
    if headers:
        hdrs.update(headers)
    req = urllib.request.Request(STATE["base"] + path, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            raw = resp.read().decode()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode()
        try:
            return exc.code, json.loads(raw)
        except ValueError:
            return exc.code, raw


def deploy(spec):
    status, out = call("POST", "/v1/templates", {"spec": spec})
    if status not in (200, 201):
        die("template register rejected: %s %s" % (status, out))
    tid = out["template_id"]
    status, out = call("POST", "/v1/templates/%s/deploy" % tid, {})
    if status not in (200, 201):
        die("template deploy rejected: %s %s" % (status, out))
    return tid


def instantiate(tid):
    status, out = call("POST", "/v1/instances", {
        "template": tid,
        "instance_key": "exp-" + uuid.uuid4().hex[:12],
        "target_agent": "audit-agent"})
    if status not in (200, 201):
        die("instance create rejected: %s %s" % (status, out))
    return out["instance_id"]


def quiet(iid):
    while True:
        frames = call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]
        runs = call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]
        if frames and all(f["state"] in SETTLED for f in frames) and not runs:
            return
        time.sleep(0.25)


def node_view(iid, node_type):
    return call("GET", "/v1/observability/nodes/%s/%s" % (iid, node_type))[1]


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def finish():
    teardown()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    sys.exit(1 if failed else 0)

import http.server
import threading

RECEIVED = []


class CheckService(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        length = int(self.headers.get("content-length") or 0)
        raw = self.rfile.read(length)
        RECEIVED.append({"path": self.path, "body": json.loads(raw or b"{}")})
        if self.path.startswith("/pass"):
            code, payload = 200, {"verdict": "ok"}
        elif self.path.startswith("/reject"):
            code, payload = 422, {"class": "schema_mismatch", "detail": "column count wrong"}
        else:
            code, payload = 503, {"class": "upstream_down"}
        body = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        return


def start_check_service():
    port = free_port()
    server = http.server.HTTPServer(("0.0.0.0", port), CheckService)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server, port


CLAIM_PAYLOAD = {"table": "items", "rows": 3, "checksum": "abc123"}


def spec_for(name, url):
    return {
        "name": name,
        "version": "1",
        "nodes": [{
            "type": "verifier",
            "executor": "verifier-http",
            "attributes": {"schema": {"type": "object", "properties": {
                "url": {"type": "string", "default": url},
                "body": {"type": "object", "default": CLAIM_PAYLOAD}}}},
        }],
    }


def run(spec):
    iid = instantiate(deploy(spec))
    call("POST", "/v1/instances/%s/messages" % iid, {}, {"Idempotency-Key": uuid.uuid4().hex})
    quiet(iid)
    return node_view(iid, "verifier")


def terminal_events(view):
    return [e for e in (view.get("events") or []) if e["kind"].startswith("terminal/")]


def main():
    server, port = start_check_service()
    base = "http://host.docker.internal:%d" % port
    try:
        boot()

        print("  leg 1: the check service answers 2xx")
        view = run(spec_for("exp-verifier-http-pass", base + "/pass"))
        summary = view["run_summary"]
        latest = view.get("latest_attributes") or {}
        check("a 2xx answer routes the node terminal to success",
              summary["fresh_count"] > 0 and summary["failed_count"] == 0, json.dumps(summary))
        check("the success records the status the check service returned",
              latest.get("verifier_status") == 200 and latest.get("verifier_pass") is True,
              json.dumps({k: latest.get(k) for k in ("verifier_status", "verifier_pass")}))
        check("the claim payload declared on the node reached the check service verbatim",
              any(r["body"] == CLAIM_PAYLOAD for r in RECEIVED), json.dumps(RECEIVED[-1:]))

        print("  leg 2: the check service answers 4xx with its own error class")
        view = run(spec_for("exp-verifier-http-reject", base + "/reject"))
        summary = view["run_summary"]
        terminals = terminal_events(view)
        classes = [(e.get("payload") or {}).get("error_class") for e in terminals]
        payloads = [(e.get("payload") or {}).get("error_payload") or {} for e in terminals]
        check("a 4xx answer routes the node terminal to error",
              summary["failed_count"] > 0 and summary["fresh_count"] == 0, json.dumps(summary))
        check("the error class carries the class the check service named",
              "verifier/check_failed/schema_mismatch" in classes, json.dumps(classes))
        check("the error payload records the upstream status and class",
              any(p.get("actual_status") == 422 and p.get("upstream_class") == "schema_mismatch" for p in payloads),
              json.dumps(payloads)[:300])

        print("  leg 3: the check service answers 5xx with its own error class")
        view = run(spec_for("exp-verifier-http-down", base + "/down"))
        summary = view["run_summary"]
        classes = [(e.get("payload") or {}).get("error_class") for e in terminal_events(view)]
        check("a 5xx answer routes the node terminal to error",
              summary["failed_count"] > 0 and summary["fresh_count"] == 0, json.dumps(summary))
        check("the 5xx error class carries the class the check service named",
              "verifier/check_failed/upstream_down" in classes, json.dumps(classes))

        finish()
    finally:
        server.shutdown()


try:
    main()
finally:
    teardown()
