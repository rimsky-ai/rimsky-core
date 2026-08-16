import json
import os
import socket
import subprocess
import threading
import sys
import time
import urllib.error
import urllib.request
import uuid

IMAGE = "rimsky-all-in-one:" + os.environ.get("RIMSKY_IMAGE_TAG", "latest")
STATE = {"base": None, "container": None, "checks": []}
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


def boot(env=None, network=None):
    url = os.environ.get("RIMSKY_CONTROL_API_URL")
    if url and not env and not network:
        STATE["base"] = url
        return
    if docker("image", "inspect", IMAGE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % IMAGE)
    port = free_port()
    name = "rimsky-exp-" + uuid.uuid4().hex[:8]
    args = ["run", "-d", "--name", name, "-p", "127.0.0.1:%d:8080" % port]
    if network:
        args += ["--network", network]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
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
    template_id = out["template_id"]
    status, out = call("POST", "/v1/templates/%s/deploy" % template_id, {})
    if status not in (200, 201):
        die("template deploy rejected: %s %s" % (status, out))
    return template_id


def new_instance(template_id):
    status, out = call("POST", "/v1/instances", {
        "template": template_id,
        "instance_key": "exp-" + uuid.uuid4().hex[:12],
        "target_agent": "audit-agent"})
    if status not in (200, 201):
        die("instance create rejected: %s %s" % (status, out))
    return out["instance_id"]


def send_message(iid, body=None):
    return call("POST", "/v1/instances/%s/messages" % iid,
                {} if body is None else body,
                {"Idempotency-Key": uuid.uuid4().hex})


def node_types(iid):
    return {n["id"]: n["node_type"] for n in call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]}


def live_runs(iid):
    return call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]


def timeline(iid):
    types = node_types(iid)
    rows = call("GET", "/v1/observability/events?instance_id=%s&limit=500" % iid)[1]["events"]
    out = []
    for e in sorted(rows, key=lambda r: r["id"]):
        out.append({
            "seq": e["id"],
            "node": types.get(e.get("node_id"), ""),
            "kind": e["kind"],
            "payload": e["payload"] or {},
            "at": e["occurred_at"],
        })
    return out


def quiet(iid):
    while True:
        frames = call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]
        if frames and all(f["state"] in SETTLED_FRAME_STATES for f in frames) and not live_runs(iid):
            return timeline(iid)
        time.sleep(0.25)


def wait_hits(iid, count):
    while True:
        hits = (call("GET", "/v1/instances/%s/breakpoint-hits" % iid)[1] or {}).get("hits") or []
        if len(hits) >= count:
            return hits
        time.sleep(0.2)


def wait_until(fn):
    while True:
        value = fn()
        if value:
            return value
        time.sleep(0.25)


def starts(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"] == "work_started"]


def terminals(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"].startswith("terminal/")]


def deltas(tl, node):
    return [r["payload"].get("attributes_delta") for r in terminals(tl, node)]


def show(tl):
    for r in tl:
        if r["kind"] == "work_started" or r["kind"].startswith("terminal/") or r["kind"].startswith("parked"):
            print("    %-5s %-22s %-32s %s" % (r["seq"], r["node"], r["kind"], json.dumps(r["payload"])[:110]))


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def finish():
    teardown()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    sys.exit(1 if failed else 0)


def counter_node(node_type, subscribes=None, maximum=9):
    node = {
        "type": node_type,
        "kind": "loop_counter",
        "attributes": {"schema": {"type": "object", "properties": {
            "max": {"type": "integer", "default": maximum},
            "count": {"type": "integer"}}}},
    }
    if subscribes:
        node["subscribes"] = subscribes
    return node


def passthrough_node(node_type, subscribes=None, properties=None, cascade_mode=None):
    node = {
        "type": node_type,
        "kind": "attribute_passthrough",
        "attributes": {"schema": {"type": "object",
                                  "properties": properties or {"v": {"type": "integer", "default": 1}}}},
    }
    if subscribes:
        node["subscribes"] = subscribes
    if cascade_mode:
        node["cascade_mode"] = cascade_mode
    return node


def sub(node_type, signal, force=False, when=None):
    entry = {"node": node_type, "type": signal, "force_upstream_refresh": force}
    if when:
        entry["when"] = when
    return entry


ROUNDS = 4

SPEC = {
    "name": "exp-sequenced-back-to-back",
    "version": "1",
    "nodes": [
        counter_node("emitter", [sub("emitter", "terminal/success", when='"loop" in payload.tags')],
                     maximum=ROUNDS),
        passthrough_node("observer",
                         [sub("emitter", "terminal/success"), sub("emitter", "attribute/count/changed")],
                         {"seen": {"type": "integer", "source": "{{nodes.emitter.attribute.count}}"}},
                         cascade_mode="sequenced"),
    ],
}


def poll_run_creation(iid, stop, order):
    seen = set()
    while not stop.is_set():
        try:
            rows = call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]
        except Exception:
            rows = []
        for r in rows:
            key = (r["id"], r["state"])
            if key not in seen:
                seen.add(key)
                order.append((r["id"][:8], r["state"]))
        time.sleep(0.05)


def main():
    boot()
    iid = new_instance(deploy(SPEC))
    order = []
    stop = threading.Event()
    poller = threading.Thread(target=poll_run_creation, args=(iid, stop, order), daemon=True)
    poller.start()
    send_message(iid)
    tl = quiet(iid)
    stop.set()
    poller.join(timeout=2)
    show(tl)
    print("  observer run rows, in the order each state was first seen:")
    dispatch_ids = [r["payload"]["dispatch_id"][:8] for r in tl
                    if r["node"] == "observer" and r["kind"] == "work_started"]
    for run_id, state in order:
        if run_id in dispatch_ids:
            print("    %s %s" % (run_id, state))
    print("  observer dispatch order: %s" % ", ".join(dispatch_ids))
    arrival = [d.get("count") for d in deltas(tl, "emitter")]
    dispatched = [d.get("seen") for d in deltas(tl, "observer")]
    check("the emitter produced %d distinct cascade rounds" % ROUNDS, arrival == list(range(1, ROUNDS + 1)),
          json.dumps(arrival))
    check("the sequenced observer dispatched once per round", len(dispatched) == ROUNDS,
          json.dumps(dispatched))
    check("each dispatch saw the inputs of its own round", sorted(dispatched) == list(range(1, ROUNDS + 1)),
          json.dumps(dispatched))
    check("the dispatches ran in arrival order", dispatched == arrival,
          "arrival=%s dispatch order=%s" % (json.dumps(arrival), json.dumps(dispatched)))
    finish()


try:
    main()
finally:
    teardown()
