import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

IMAGE = "rimsky-all-in-one:" + os.environ.get("RIMSKY_IMAGE_TAG", "latest")
HERE = os.path.dirname(os.path.abspath(__file__))
STATE = {"base": None, "container": None, "workspace": None, "checks": []}
SETTLED_FRAME_STATES = ("completed", "failed", "terminated")


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


def boot(env=None, mounts=None):
    url = os.environ.get("RIMSKY_CONTROL_API_URL")
    if url and not env and not mounts:
        STATE["base"] = url
        return
    if docker("image", "inspect", IMAGE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % IMAGE)
    port = free_port()
    name = "rimsky-exp-" + uuid.uuid4().hex[:8]
    args = ["run", "-d", "--name", name, "-p", "127.0.0.1:%d:8080" % port]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    for spec in (mounts or []):
        args += ["-v", spec]
    args.append(IMAGE)
    res = docker(*args)
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


def workspace(folders):
    path = tempfile.mkdtemp(prefix="rimsky-exp-ws-")
    STATE["workspace"] = path
    for folder in folders:
        os.makedirs(os.path.join(path, folder), exist_ok=True)
    return path


def teardown():
    if STATE["container"]:
        docker("rm", "-f", STATE["container"])
        STATE["container"] = None
    if STATE["workspace"]:
        shutil.rmtree(STATE["workspace"], ignore_errors=True)
        STATE["workspace"] = None


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


def register(spec):
    return call("POST", "/v1/templates", {"spec": spec})


def deploy(spec):
    status, out = register(spec)
    if status not in (200, 201):
        die("template register rejected: %s %s" % (status, json.dumps(out)))
    template_id = out["template_id"]
    status, out = call("POST", "/v1/templates/%s/deploy" % template_id, {})
    if status not in (200, 201):
        die("template deploy rejected: %s %s" % (status, json.dumps(out)))
    return template_id


def new_instance(template_id, params=None):
    body = {"template": template_id,
            "instance_key": "exp-" + uuid.uuid4().hex[:12],
            "target_agent": "audit-agent"}
    if params is not None:
        body["params"] = params
    status, out = call("POST", "/v1/instances", body)
    if status not in (200, 201):
        die("instance create rejected: %s %s" % (status, json.dumps(out)))
    return out["instance_id"]


def send_message(iid, message_type=None, payload=None):
    body = {}
    if message_type is not None:
        body["type"] = message_type
    if payload is not None:
        body["payload"] = payload
    status, out = call("POST", "/v1/instances/%s/messages" % iid, body,
                       {"Idempotency-Key": uuid.uuid4().hex})
    if status not in (200, 201):
        die("message post rejected: %s %s" % (status, json.dumps(out)))
    return out


def nodes(iid):
    return call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]


def node_id(iid, node_type):
    for n in nodes(iid):
        if n["node_type"] == node_type:
            return n["id"]
    return None


def node_types(iid):
    return {n["id"]: n["node_type"] for n in nodes(iid)}


def live_runs(iid):
    return call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]


def frames(iid):
    return call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]


def timeline(iid):
    types = node_types(iid)
    rows = call("GET", "/v1/observability/events?instance_id=%s&limit=500" % iid)[1]["events"]
    out = []
    for e in sorted(rows, key=lambda r: r["id"]):
        out.append({"seq": e["id"], "node": types.get(e.get("node_id"), ""),
                    "kind": e["kind"], "payload": e["payload"] or {}})
    return out


def quiet(iid, want_frames=1):
    while True:
        fr = frames(iid)
        if len(fr) >= want_frames and all(f["state"] in SETTLED_FRAME_STATES for f in fr) and not live_runs(iid):
            return timeline(iid)
        time.sleep(0.25)


def starts(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"] == "work_started"]


def terminals(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"].startswith("terminal/")]


def deltas(tl, node):
    return [r["payload"].get("attributes_delta") for r in terminals(tl, node)]


def payloads(tl, kind):
    return [r["payload"] for r in tl if r["kind"] == kind]


def show(tl):
    for r in tl:
        if r["kind"] == "work_started" or r["kind"].startswith("terminal/") or r["kind"] in (
                "fan_out_dispatched", "subclaim.acquired", "lock_acquired"):
            print("    %-5s %-16s %-40s %s" % (r["seq"], r["node"], r["kind"], json.dumps(r["payload"])[:120]))


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def finish():
    teardown()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    sys.exit(1 if failed else 0)


def counter_node(node_type, maximum, self_loop=True, extra_subscribes=None, **kw):
    node = {"type": node_type, "kind": "loop_counter",
            "attributes": {"schema": {"type": "object", "properties": {
                "max": {"type": "integer", "default": maximum},
                "count": {"type": "integer", "default": 0}}}}}
    subs = list(extra_subscribes or [])
    if self_loop:
        subs.append({"node": node_type, "type": "terminal/success",
                     "force_upstream_refresh": False, "when": '"loop" in payload.tags'})
    if subs:
        node["subscribes"] = subs
    node.update(kw)
    return node


def passthrough_node(node_type, properties, subscribes=None, **kw):
    node = {"type": node_type, "kind": "attribute_passthrough",
            "attributes": {"schema": {"type": "object", "properties": properties}}}
    if subscribes:
        node["subscribes"] = subscribes
    node.update(kw)
    return node

def spec_for(mode, source, name):
    receiver = passthrough_node("b", {"snapshot": {"type": "integer", "source": source}},
                                subscribes=[{"node": "a", "type": "attribute/*",
                                             "force_upstream_refresh": False}])
    receiver["cascade_mode"] = mode
    return {"name": name, "version": "1", "nodes": [counter_node("a", 4), receiver]}


def run(mode, source, name):
    iid = new_instance(deploy(spec_for(mode, source, name)))
    send_message(iid)
    tl = quiet(iid)
    return len(starts(tl, "a")), len(starts(tl, "b")), deltas(tl, "b")


STABLE = "{{nodes.a.attribute.max}}"
CHANGING = "{{nodes.a.attribute.count}}"


def main():
    boot()

    rounds, dispatches, bags = run("sequenced", STABLE, "exp-idem-control-sequenced")
    print("  control (sequenced), identical inputs: %d rounds -> %d dispatches %s"
          % (rounds, dispatches, json.dumps(bags)))
    check("without an idempotent mode every cascade round reaches the executor",
          rounds == 4 and dispatches == 4, json.dumps(bags))
    check("all four of those rounds carried byte-identical inputs",
          bags == [{"snapshot": 4}] * 4, json.dumps(bags))

    for mode in ("idempotent-queue", "idempotent-settled"):
        rounds, dispatches, bags = run(mode, STABLE, "exp-idem-%s-stable" % mode)
        print("  %s, identical inputs: %d rounds -> %d dispatches %s"
              % (mode, rounds, dispatches, json.dumps(bags)))
        check("under %s the three re-runs with identical inputs never reach the executor" % mode,
              rounds == 4 and dispatches == 1, json.dumps(bags))

        rounds, dispatches, bags = run(mode, CHANGING, "exp-idem-%s-changing" % mode)
        print("  %s, differing inputs: %d rounds -> %d dispatches %s"
              % (mode, rounds, dispatches, json.dumps(bags)))
        check("under %s rounds whose inputs differ are all still dispatched" % mode,
              rounds == 4 and dispatches == 4, json.dumps(bags))
        check("each surviving %s dispatch saw a distinct input bag" % mode,
              len({json.dumps(d, sort_keys=True) for d in bags}) == 4, json.dumps(bags))


try:
    main()
finally:
    teardown()
