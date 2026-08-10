import json
import os
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG", "latest")
IMAGE = "rimsky-all-in-one:" + TAG
STATE = {"base": None, "containers": [], "networks": [], "checks": []}
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


def require_image(image):
    if docker("image", "inspect", image).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % image)


def network():
    name = "exp-net-" + uuid.uuid4().hex[:8]
    if docker("network", "create", name).returncode != 0:
        die("docker network create failed")
    STATE["networks"].append(name)
    return name


def run_container(image, args, name=None):
    require_image(image)
    name = name or ("rimsky-exp-" + uuid.uuid4().hex[:8])
    res = docker("run", "-d", "--name", name, *args, image)
    if res.returncode != 0:
        die("docker run %s failed: %s" % (image, res.stderr.strip()))
    STATE["containers"].append(name)
    return name


def boot(volumes=(), env=None, net=None, alias=None, extra=()):
    url = os.environ.get("RIMSKY_CONTROL_API_URL")
    if url and not volumes and not env and not net:
        STATE["base"] = url
        return
    port = free_port()
    args = ["-p", "127.0.0.1:%d:8080" % port]
    for v in volumes:
        args += ["-v", v]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    if net:
        args += ["--network", net]
        if alias:
            args += ["--network-alias", alias]
    args += list(extra)
    run_container(IMAGE, args)
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                return
        except Exception:
            pass
        time.sleep(0.3)


def teardown():
    for name in STATE["containers"]:
        docker("rm", "-f", name)
    STATE["containers"] = []
    for name in STATE["networks"]:
        docker("network", "rm", name)
    STATE["networks"] = []


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
        die("template deploy rejected: %s %s" % (status, out))
    return template_id


def new_instance(template_id, **extra):
    body = {"template": template_id,
            "instance_key": "exp-" + uuid.uuid4().hex[:12],
            "target_agent": "audit-probe-agent"}
    body.update(extra)
    status, out = call("POST", "/v1/instances", body)
    if status not in (200, 201):
        die("instance create rejected: %s %s" % (status, out))
    return out["instance_id"]


def send_message(iid, body=None, key=None):
    return call("POST", "/v1/instances/%s/messages" % iid,
                {} if body is None else body,
                {"Idempotency-Key": key or uuid.uuid4().hex})


def messages(iid):
    return call("GET", "/v1/instances/%s/messages" % iid)[1]["messages"]


def frames(iid):
    return call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]


def node_types(iid):
    return {n["id"]: n["node_type"] for n in call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]}


def live_runs(iid):
    return call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]


def timeline(iid):
    types = node_types(iid)
    rows = call("GET", "/v1/observability/events?instance_id=%s&limit=800" % iid)[1]["events"]
    out = []
    for e in sorted(rows, key=lambda r: r["id"]):
        out.append({
            "seq": e["id"],
            "node": types.get(e.get("node_id"), ""),
            "kind": e["kind"],
            "payload": e["payload"] or {},
        })
    return out


def pending(iid):
    return [m for m in messages(iid) if not m.get("delivered_at") and not m.get("cancelled")]


def quiet(iid):
    while True:
        fr = frames(iid)
        if fr and not pending(iid) and all(f["state"] in SETTLED_FRAME_STATES for f in fr) and not live_runs(iid):
            return timeline(iid)
        time.sleep(0.25)


def wait_hits(iid, count):
    while True:
        hits = (call("GET", "/v1/instances/%s/breakpoint-hits" % iid)[1] or {}).get("hits") or []
        if len(hits) >= count:
            return hits
        time.sleep(0.2)


def pause_breakpoint(iid, node_type):
    status, out = call("POST", "/v1/instances/%s/breakpoints" % iid,
                       {"matcher": {"node_type": node_type},
                        "checkpoint": "before_dispatch",
                        "mode": "pause"})
    if status not in (200, 201):
        die("breakpoint create rejected: %s %s" % (status, out))
    return out["breakpoint_id"]


def drain_breakpoint(iid, bpid):
    resumed = set()
    while True:
        hits = (call("GET", "/v1/instances/%s/breakpoint-hits" % iid)[1] or {}).get("hits") or []
        fresh = [h for h in hits if h["hit_id"] not in resumed]
        for h in fresh:
            resumed.add(h["hit_id"])
            call("POST", "/v1/instances/%s/breakpoints/%s/resume" % (iid, bpid), {"hit_id": h["hit_id"]})
        fr = frames(iid)
        if not fresh and not pending(iid) and fr and all(f["state"] in SETTLED_FRAME_STATES for f in fr):
            return timeline(iid)
        time.sleep(0.25)


def terminals(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"].startswith("terminal/")]


def deltas(tl, node):
    return [r["payload"].get("attributes_delta") for r in terminals(tl, node)]


def starts(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"] == "work_started"]


def show(tl):
    for r in tl:
        if r["kind"] == "work_started" or r["kind"].startswith("terminal/"):
            print("    %-5s %-22s %-34s %s" % (r["seq"], r["node"], r["kind"], json.dumps(r["payload"])[:110]))


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def finish():
    teardown()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    sys.exit(1 if failed else 0)


def passthrough(node_type, subscribes=None, properties=None):
    node = {
        "type": node_type,
        "kind": "attribute_passthrough",
        "attributes": {"schema": {"type": "object",
                                  "properties": properties or {"v": {"type": "integer", "default": 1}}}},
    }
    if subscribes:
        node["subscribes"] = subscribes
    return node


def sub(source, signal, when=None, force=False):
    entry = {"node": source, "type": signal, "force_upstream_refresh": force}
    if when:
        entry["when"] = when
    return entry


DISAMBIGUATE = {
    "name": "exp-typed-message-substitution",
    "version": "1",
    "messages": [
        {"type": "ops/alpha", "body_schema": {
            "type": "object", "properties": {"a": {"type": "string"}}, "required": ["a"]}},
        {"type": "ops/beta", "body_schema": {
            "type": "object", "properties": {"b": {"type": "string"}}, "required": ["b"]}},
    ],
    "nodes": [passthrough("reactor",
                          [sub("ops/alpha", "terminal/success"), sub("ops/beta", "terminal/success")],
                          {"from_alpha": {"type": "string", "source": '{{messages.ops/alpha.a | "none"}}'},
                           "from_beta": {"type": "string", "source": '{{messages.ops/beta.b | "none"}}'}})],
}

ACROSS_FRAMES = {
    "name": "exp-typed-message-across-frames",
    "version": "1",
    "messages": [{"type": "ops/carry", "body_schema": {
        "type": "object", "properties": {"value": {"type": "integer"}}, "required": ["value"]}}],
    "nodes": [
        {"type": "counter", "kind": "loop_counter",
         "attributes": {"schema": {"type": "object", "properties": {
             "max": {"type": "integer", "default": 1},
             "count": {"type": "integer"}}}}},
        {"type": "sender", "sends_message": "ops/carry",
         "subscribes": [sub("counter", "attribute/count/changed")],
         "attributes": {"schema": {"type": "object",
                                   "properties": {"value": {"type": "integer",
                                                            "source": "{{nodes.counter.attribute.count}}"}},
                                   "required": ["value"]}}},
        passthrough("later", [sub("ops/carry", "terminal/success")],
                    {"carried": {"type": "integer", "source": "{{messages.ops/carry.value}}"}}),
    ],
}


def main():
    boot()

    iid = new_instance(deploy(DISAMBIGUATE))
    send_message(iid, {"type": "ops/alpha", "payload": {"a": "A1"}})
    tl = quiet(iid)
    check("in the frame an alpha message opened, the node read alpha's field by its declared name",
          deltas(tl, "reactor") == [{"from_alpha": "A1", "from_beta": "none"}],
          json.dumps(deltas(tl, "reactor")))

    send_message(iid, {"type": "ops/beta", "payload": {"b": "B1"}})
    tl = quiet(iid)
    show(tl)
    check("in the frame a beta message opened, the same node read beta's field instead",
          deltas(tl, "reactor")[-1] == {"from_alpha": "none", "from_beta": "B1"},
          json.dumps(deltas(tl, "reactor")))
    check("one node that could react to either type never confused the two",
          [d["from_alpha"] for d in deltas(tl, "reactor")] == ["A1", "none"]
          and [d["from_beta"] for d in deltas(tl, "reactor")] == ["none", "B1"],
          json.dumps(deltas(tl, "reactor")))

    status, out = register({
        "name": "exp-typed-message-substitution-x", "version": "1",
        "messages": [{"type": "ops/alpha", "body_schema": {
            "type": "object", "properties": {"a": {"type": "string"}}}}],
        "nodes": [passthrough("reactor", [sub("ops/alpha", "terminal/success")],
                              {"v": {"type": "string", "source": "{{messages.ops/alpha.nosuch}}"}})]})
    check("a field the declared body schema does not carry is refused at registration",
          status == 400 and "nosuch" in json.dumps(out), json.dumps(out)[:220])

    iid2 = new_instance(deploy(ACROSS_FRAMES))
    send_message(iid2)
    tl2 = quiet(iid2)
    show(tl2)

    sent = [m for m in messages(iid2) if m["type"] == "ops/carry"]
    check("a node composed a typed message body from an attribute it held in its own frame",
          len(sent) == 1 and sent[0]["payload"] == {"value": 1} and sent[0]["sender_kind"] == "instance",
          json.dumps([m["payload"] for m in sent]))
    check("the body opened a later frame and a node there read it through the same grammar",
          deltas(tl2, "later") == [{"carried": 1}], json.dumps(deltas(tl2, "later")))
    fr2 = frames(iid2)
    carry_frames = [f for f in fr2 if f.get("message_type") == "ops/carry"]
    check("the value crossed a frame boundary: the operator's wake opened one frame and the body "
          "the node composed opened the next, which is the frame the reader ran in",
          len(fr2) == 2 and len(carry_frames) == 1
          and sent[0]["frame_id"] == carry_frames[0]["frame_id"],
          json.dumps([f.get("message_type") for f in fr2]))

    finish()


try:
    main()
finally:
    teardown()
