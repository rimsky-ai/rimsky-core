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


SPEC = {
    "name": "exp-messages-as-nodes",
    "version": "1",
    "messages": [{"type": "ops/note", "body_schema": {
        "type": "object",
        "properties": {"text": {"type": "string"}},
        "required": ["text"]}}],
    "nodes": [
        passthrough("plain", [sub("ops/note", "terminal/success")],
                    {"text": {"type": "string", "default": "from-a-node"}}),
        passthrough("reader",
                    [sub("ops/note", "terminal/success"), sub("plain", "attribute/text/changed")],
                    {"via_messages": {"type": "string", "source": "{{messages.ops/note.text}}"},
                     "via_nodes": {"type": "string", "source": "{{nodes.plain.attribute.text}}"}}),
    ],
}


def main():
    boot()
    iid = new_instance(deploy(SPEC))

    check("the declared message type is materialised as an ordinary node of the instance",
          "ops/note" in set(node_types(iid).values()),
          ",".join(sorted(node_types(iid).values())))

    status, _ = send_message(iid, {"type": "ops/note", "payload": {"text": "from-a-message"}})
    check("the message is accepted", status == 201, str(status))
    tl = quiet(iid)
    show(tl)

    check("one node resolved a messages-directive and a nodes-directive side by side",
          deltas(tl, "reader") == [{"via_messages": "from-a-message", "via_nodes": "from-a-node"}],
          json.dumps(deltas(tl, "reader")))

    changed = [r["payload"] for r in tl
               if r["node"] == "ops/note" and r["kind"] == "attribute/text/changed"]
    check("the value the messages-directive read is an attribute write on the message type's own node",
          changed == [{"key": "text", "old_value": None, "value": "from-a-message"}],
          json.dumps(changed))

    status, out = register({
        "name": "exp-messages-as-nodes-x0", "version": "1",
        "messages": [{"type": "ops/note", "body_schema": {"type": "object"}}],
        "nodes": [passthrough("watcher", [sub("ops/note", "attribute/text/changed")],
                              {"woke": {"type": "string", "default": "y"}})]})
    check("the two namespaces stay apart on the subscription side: an attribute-changed edge on a "
          "message type is refused, because delivery only ever manifests as terminal/success",
          status == 400 and "subscription_message_type_unreachable" in json.dumps(out),
          json.dumps(out)[:220])

    status, out = register({
        "name": "exp-messages-as-nodes-x1", "version": "1",
        "messages": [{"type": "ops/note", "body_schema": {"type": "object"}}],
        "nodes": [passthrough("reader", [sub("ops/note", "terminal/success")],
                              {"v": {"type": "string", "source": "{{messages.ops/undeclared.text}}"}})]})
    check("a messages-directive naming an undeclared message type is refused at registration",
          status == 400 and "unknown message type" in json.dumps(out), json.dumps(out)[:200])

    status, out = register({
        "name": "exp-messages-as-nodes-x2", "version": "1",
        "messages": [{"type": "ops/note", "body_schema": {"type": "object"}}],
        "nodes": [passthrough("reader", [sub("ops/note", "terminal/success")],
                              {"v": {"type": "string", "source": "{{nodes.ops/note.attribute.text}}"}})]})
    check("a nodes-directive naming a message type is refused: the nodes namespace is declared node-types",
          status == 400 and "unknown node" in json.dumps(out), json.dumps(out)[:200])

    def uncovered(source):
        status, out = register({
            "name": "exp-messages-as-nodes-x3-" + uuid.uuid4().hex[:6], "version": "1",
            "messages": [{"type": "ops/note", "body_schema": {"type": "object"}}],
            "nodes": [passthrough("plain", None, {"text": {"type": "string", "default": "x"}}),
                      passthrough("reader", None, {"v": {"type": "string", "source": source}})]})
        errs = (out or {}).get("validation_errors") or []
        return status, [e for e in errs if e.get("kind") == "substitution_ref_uncovered"]

    s1, e1 = uncovered("{{messages.ops/note.text}}")
    s2, e2 = uncovered("{{nodes.plain.attribute.text}}")
    check("an uncovered messages-directive is refused by the same subscription-coverage check as a "
          "nodes-directive, and both are told which subscribes entry to add",
          s1 == 400 and s2 == 400 and len(e1) == 1 and len(e2) == 1
          and e1[0]["suggested_subscribes_entry"]["node"] == "ops/note"
          and e2[0]["suggested_subscribes_entry"]["node"] == "plain",
          json.dumps([e1, e2])[:260])

    finish()


try:
    main()
finally:
    teardown()
