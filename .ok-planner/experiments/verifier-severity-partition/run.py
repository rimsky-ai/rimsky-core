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


WARNING_CHECK = {"kind": "no_nulls", "severity": "warning", "config": {"field": "id"}}
ERROR_CHECK = {"kind": "numeric_range", "severity": "error", "config": {"field": "value", "min": 0, "max": 100}}
ROWS_TRIPPING_WARNING_ONLY = [{"id": "a", "value": 10}, {"value": 20}, {"id": "c", "value": 30}]
ROWS_TRIPPING_BOTH = [{"id": "a", "value": 10}, {"value": 20}, {"id": "c", "value": 250}]


def spec_for(name, rows, checks):
    return {
        "name": name,
        "version": "1",
        "nodes": [{
            "type": "verifier",
            "executor": "verifier-shape-checks",
            "attributes": {"schema": {"type": "object", "properties": {
                "checks": {"type": "array", "default": checks},
                "rows": {"type": "array", "default": rows}}}},
        }],
    }


def run(spec):
    iid = instantiate(deploy(spec))
    call("POST", "/v1/instances/%s/messages" % iid, {}, {"Idempotency-Key": uuid.uuid4().hex})
    quiet(iid)
    return node_view(iid, "verifier")


def error_classes(view):
    out = []
    for e in (view.get("events") or []):
        cls = (e.get("payload") or {}).get("error_class")
        if cls:
            out.append(cls)
    return out


def error_payloads(view):
    out = []
    for e in (view.get("events") or []):
        payload = (e.get("payload") or {}).get("error_payload")
        if payload:
            out.append(payload)
    return out


def main():
    boot()

    print("  leg 1: a failing warning-severity check beside a passing error-severity check")
    view = run(spec_for("exp-severity-warning-only", ROWS_TRIPPING_WARNING_ONLY, [WARNING_CHECK, ERROR_CHECK]))
    summary = view["run_summary"]
    latest = view.get("latest_attributes") or {}
    warnings = latest.get("verifier_warnings") or []
    check("a failing warning-severity check does not block the commit",
          summary["fresh_count"] > 0 and summary["failed_count"] == 0, json.dumps(summary))
    check("the failing warning is still counted",
          latest.get("verifier_warning_count") == 1, json.dumps(latest.get("verifier_warning_count")))
    check("the failing warning is named with its kind and severity",
          any(w.get("kind") == "no_nulls" and w.get("severity") == "warning" for w in warnings),
          json.dumps(warnings))

    print("  leg 2: the same rows, the same check labelled error instead of warning")
    escalated = dict(WARNING_CHECK)
    escalated["severity"] = "error"
    view = run(spec_for("exp-severity-escalated", ROWS_TRIPPING_WARNING_ONLY, [escalated, ERROR_CHECK]))
    summary = view["run_summary"]
    classes = error_classes(view)
    check("relabelling that one check error turns the same data into a blocked commit",
          summary["failed_count"] > 0 and summary["fresh_count"] == 0, json.dumps(summary))
    check("the blocking terminal names the escalated check",
          "verifier/check_failed/no_nulls" in classes, json.dumps(classes))

    print("  leg 3: a failing error-severity check beside a failing warning-severity check")
    view = run(spec_for("exp-severity-both", ROWS_TRIPPING_BOTH, [WARNING_CHECK, ERROR_CHECK]))
    summary = view["run_summary"]
    classes = error_classes(view)
    payloads = error_payloads(view)
    check("a failing error-severity check blocks the commit",
          summary["failed_count"] > 0 and summary["fresh_count"] == 0, json.dumps(summary))
    check("the error names the error-severity check, not the warning-severity one",
          "verifier/check_failed/numeric_range" in classes, json.dumps(classes))
    check("the blocked terminal still carries the non-blocking warning beside the blocking failure",
          any(any(w.get("kind") == "no_nulls" for w in (p.get("warnings") or [])) and
              any(f.get("kind") == "numeric_range" for f in (p.get("failures") or []))
              for p in payloads),
          json.dumps(payloads)[:300])

    finish()


try:
    main()
finally:
    teardown()
