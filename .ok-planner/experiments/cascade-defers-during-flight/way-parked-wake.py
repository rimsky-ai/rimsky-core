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


NETWORK = "rimsky-exp-net-" + uuid.uuid4().hex[:8]
ENDPOINT = "rimsky-exp-endpoint-" + uuid.uuid4().hex[:8]
SUBNET = "172.31.88.0/24"
ENDPOINT_SOURCE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "rate-limited-endpoint.py")

SPEC = {
    "name": "exp-defers-parked-wake",
    "version": "1",
    "nodes": [
        counter_node("emitter"),
        {"type": "worker", "executor": "http-node",
         "subscribes": [sub("emitter", "terminal/success"), sub("emitter", "attribute/count/changed")],
         "attributes": {"schema": {"type": "object", "properties": {
             "url": {"type": "string", "default": "http://%s:8000/work" % ENDPOINT},
             "method": {"type": "string", "default": "POST"},
             "seen": {"type": "integer", "source": "{{nodes.emitter.attribute.count}}"}}}}},
    ],
}


def start_support():
    docker("network", "create", "--subnet", SUBNET, NETWORK)
    res = docker("run", "-d", "--name", ENDPOINT, "--network", NETWORK,
                 "-v", "%s:/srv/endpoint.py:ro" % ENDPOINT_SOURCE,
                 "python:3.12-alpine", "python", "/srv/endpoint.py")
    if res.returncode != 0:
        die("could not start the rate-limited endpoint: " + res.stderr.strip())


def stop_support():
    docker("rm", "-f", ENDPOINT)
    docker("network", "rm", NETWORK)


def main():
    start_support()
    boot(env={"RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST": SUBNET}, network=NETWORK)
    iid = new_instance(deploy(SPEC))
    send_message(iid)
    parked = wait_until(lambda: [r for r in live_runs(iid) if r["state"] == "parked"])
    park = [r for r in timeline(iid) if r["kind"] == "transient/park"][0]
    resume_at = park["payload"]["resume_at"]
    check("the worker run parked with a resume far in the future",
          resume_at > park["at"], "parked at %s, resume_at %s" % (park["at"], resume_at))
    check("the parked run is the one that was dispatched", len(parked) == 1,
          json.dumps([(r["id"], r["state"]) for r in parked]))

    call("POST", "/v1/instances/%s/pause" % iid, {})
    call("POST", "/v1/instances/%s/debug/override" % iid,
         {"action": "invalidate_node", "node_type": "emitter"})
    call("POST", "/v1/instances/%s/resume" % iid, {})

    wake = wait_until(lambda: [r for r in timeline(iid) if r["kind"] == "parked_resume_started"])
    check("the upstream cascade woke the parked run instead of leaving it asleep",
          wake[0]["payload"].get("resume_reason") == "upstream_cascade",
          json.dumps(wake[0]["payload"]))
    check("the wake happened long before the park's own resume time",
          wake[0]["at"] < resume_at, "woken at %s, resume_at was %s" % (wake[0]["at"], resume_at))

    tl = quiet(iid)
    show(tl)
    check("the woken work settled", any(r["kind"] == "terminal/success" for r in tl if r["node"] == "worker"),
          json.dumps(deltas(tl, "worker")))
    parked_run = parked[0]["id"]
    started = [r["payload"]["dispatch_id"] for r in tl
               if r["node"] == "worker" and r["kind"] == "work_started"]
    check("the work that ran after the wake is the parked run itself, not a replacement",
          len(started) > 1 and started[1] == parked_run,
          "parked run %s, dispatches %s" % (parked_run, json.dumps(started)))
    completed = [r["payload"].get("dispatch_id") for r in tl
                 if r["node"] == "worker" and r["kind"] == "work_completed"]
    check("the parked run settled rather than being discarded",
          parked_run in completed,
          "parked run %s, runs that completed %s" % (parked_run, json.dumps(completed)))
    finish()


try:
    main()
finally:
    teardown()
    stop_support()
