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


CLEAN_ROWS = [{"id": "a", "value": 10}, {"id": "b", "value": 20}, {"id": "c", "value": 30}]
DIRTY_ROWS = [{"id": "a", "value": 10}, {"id": "a", "value": 20}, {"id": "c", "value": 250}]
DECLARED_CHECKS = [
    {"kind": "pk_unique", "severity": "error", "config": {"field": "id"}},
    {"kind": "numeric_range", "severity": "error", "config": {"field": "value", "min": 0, "max": 100}},
    {"kind": "row_count_absolute", "severity": "error", "config": {"min": 1}},
]


def inline_spec(name, rows):
    return {
        "name": name,
        "version": "1",
        "nodes": [{
            "type": "verifier",
            "executor": "verifier-shape-checks",
            "attributes": {"schema": {"type": "object", "properties": {
                "checks": {"type": "array", "default": DECLARED_CHECKS},
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


def main():
    boot()

    print("  leg 1: rows that satisfy every declared check")
    view = run(inline_spec("exp-shape-checks-clean", CLEAN_ROWS))
    summary = view["run_summary"]
    latest = view.get("latest_attributes") or {}
    check("a node whose declared checks all pass settles fresh",
          summary["fresh_count"] > 0 and summary["failed_count"] == 0, json.dumps(summary))
    check("the verifier reports how many declared checks it ran",
          latest.get("verifier_checks") == len(DECLARED_CHECKS), json.dumps(latest.get("verifier_checks")))
    check("the verifier reports the row count it read",
          latest.get("verifier_rows") == len(CLEAN_ROWS), json.dumps(latest.get("verifier_rows")))

    print("  leg 2: rows that violate two of the declared checks")
    view = run(inline_spec("exp-shape-checks-dirty", DIRTY_ROWS))
    summary = view["run_summary"]
    classes = error_classes(view)
    check("a node whose declared checks fail is blocked, not committed",
          summary["failed_count"] > 0 and summary["fresh_count"] == 0, json.dumps(summary))
    check("the terminal error names the failing check kind",
          any(c.startswith("verifier/check_failed/") for c in classes), json.dumps(classes))

    print("  leg 3: the same rows, one extra declared check")
    spec = inline_spec("exp-shape-checks-stricter", CLEAN_ROWS)
    spec["nodes"][0]["attributes"]["schema"]["properties"]["checks"]["default"] = DECLARED_CHECKS + [
        {"kind": "no_nulls", "severity": "error", "config": {"field": "note"}}]
    view = run(spec)
    summary = view["run_summary"]
    classes = error_classes(view)
    check("rows accepted under one declaration are rejected under a stricter one, so the declaration is what governs",
          summary["failed_count"] > 0 and "verifier/check_failed/no_nulls" in classes,
          json.dumps(summary) + " " + json.dumps(classes))

    print("  leg 4: a check kind the bundled verifier does not implement")
    spec = inline_spec("exp-shape-checks-unknown-kind", CLEAN_ROWS)
    spec["nodes"][0]["attributes"]["schema"]["properties"]["checks"]["default"] = [
        {"kind": "no_such_check", "config": {}}]
    view = run(spec)
    classes = error_classes(view)
    check("an unimplemented check kind fails the node with an attribute error rather than passing silently",
          "verifier/attribute_invalid" in classes, json.dumps(classes))

    finish()


try:
    main()
finally:
    teardown()
