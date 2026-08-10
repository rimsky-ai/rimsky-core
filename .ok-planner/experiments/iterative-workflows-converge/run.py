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


CONVERGE_AT = 3
UNREACHABLE_CAP = 50


def sub(node_type, signal, when=None):
    entry = {"node": node_type, "type": signal, "force_upstream_refresh": False}
    if when:
        entry["when"] = when
    return entry


def counter(node_type, subscribes, cascade_mode=None):
    node = {
        "type": node_type,
        "kind": "loop_counter",
        "attributes": {"schema": {"type": "object", "properties": {
            "max": {"type": "integer", "default": UNREACHABLE_CAP},
            "count": {"type": "integer"}}}},
        "subscribes": subscribes,
    }
    if cascade_mode:
        node["cascade_mode"] = cascade_mode
    return node


def passthrough(node_type, subscribes=None):
    node = {
        "type": node_type,
        "kind": "attribute_passthrough",
        "attributes": {"schema": {"type": "object", "properties": {
            "count": {"type": "integer", "default": 0}}}},
    }
    if subscribes is not None:
        node["subscribes"] = subscribes
    return node


KEEP_GOING = "payload.attributes_delta.count < %d" % CONVERGE_AT
CONVERGED = "payload.attributes_delta.count >= %d" % CONVERGE_AT


def self_cycle_spec():
    return {
        "name": "exp-iterative-self-cycle",
        "version": "1",
        "nodes": [
            counter("iterate", [sub("iterate", "terminal/success", KEEP_GOING)], cascade_mode="sequenced"),
            passthrough("downstream", [sub("iterate", "terminal/success", CONVERGED)]),
        ],
    }


def two_node_cycle_spec():
    return {
        "name": "exp-iterative-two-node-cycle",
        "version": "1",
        "nodes": [
            passthrough("seed"),
            counter("ping", [sub("seed", "terminal/success"), sub("pong", "terminal/success")]),
            passthrough("pong", [sub("ping", "terminal/success", KEEP_GOING)]),
            passthrough("downstream", [sub("ping", "terminal/success", CONVERGED)]),
        ],
    }


def drive(spec):
    iid = instantiate(deploy(spec))
    call("POST", "/v1/instances/%s/messages" % iid, {}, {"Idempotency-Key": uuid.uuid4().hex})
    quiet(iid)
    return iid


def timeline(iid):
    types = {n["id"]: n["node_type"] for n in call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]}
    rows = call("GET", "/v1/observability/events?instance_id=%s&limit=1000" % iid)[1]["events"]
    return [{"node": types.get(r.get("node_id"), ""), "kind": r["kind"], "payload": r["payload"] or {}}
            for r in sorted(rows, key=lambda r: r["id"])]


def counts_emitted(tl, node_type):
    return [r["payload"].get("attributes_delta", {}).get("count")
            for r in tl
            if r["node"] == node_type and r["kind"] == "terminal/success"]


def dispatches(tl, node_type):
    return len([r for r in tl if r["node"] == node_type and r["kind"] == "work_started"])


def frames(iid):
    return call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]


def main():
    boot()

    print("  leg 1: one node re-running against its own output")
    iid = drive(self_cycle_spec())
    tl = timeline(iid)
    emitted = counts_emitted(tl, "iterate")
    check("the node ran repeatedly against its own output",
          emitted == list(range(1, CONVERGE_AT + 1)), json.dumps(emitted))
    check("iteration stopped when the declared stop condition stopped holding, not at a round ceiling",
          emitted[-1] == CONVERGE_AT and CONVERGE_AT < UNREACHABLE_CAP, json.dumps(emitted))
    check("the converged output reached the downstream node, so iteration composes with the rest of the graph",
          dispatches(tl, "downstream") == 1, str(dispatches(tl, "downstream")))
    fr = frames(iid)
    check("the whole iteration is one frame in observability",
          len(fr) == 1 and fr[0]["state"] == "completed", json.dumps([(f["frame_id"], f["state"]) for f in fr]))

    print("  leg 2: a two-node cycle walking back to its start")
    iid = drive(two_node_cycle_spec())
    tl = timeline(iid)
    emitted = counts_emitted(tl, "ping")
    check("the cycle went round until the declared stop condition stopped holding",
          emitted == list(range(1, CONVERGE_AT + 1)), json.dumps(emitted))
    check("the back-edge node ran once per round below the stop condition",
          dispatches(tl, "pong") == CONVERGE_AT - 1, str(dispatches(tl, "pong")))
    check("the converged output left the cycle for the downstream node",
          dispatches(tl, "downstream") == 1, str(dispatches(tl, "downstream")))
    fr = frames(iid)
    check("the whole cycle is one frame in observability",
          len(fr) == 1 and fr[0]["state"] == "completed", json.dumps([(f["frame_id"], f["state"]) for f in fr]))
    check("the instance came to rest with no live runs",
          not call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"])

    finish()


try:
    main()
finally:
    teardown()
