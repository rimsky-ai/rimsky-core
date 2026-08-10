import json
import os
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    print("HARNESS ERROR: set RIMSKY_IMAGE_TAG to the tag built from this tree")
    sys.exit(2)

STATE = {"base": None, "containers": [], "networks": [], "checks": [], "tmp": []}
SETTLED_FRAME_STATES = ("completed", "failed", "terminated")


def die(msg):
    print("HARNESS ERROR: " + msg)
    teardown()
    sys.exit(2)


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def image(name):
    ref = "%s:%s" % (name, TAG)
    if docker("image", "inspect", ref).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % ref)
    return ref


def free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def network(slug):
    name = "rimsky-exp-%s-%s" % (slug, uuid.uuid4().hex[:6])
    res = docker("network", "create", name)
    if res.returncode != 0:
        die("docker network create failed: " + res.stderr.strip())
    STATE["networks"].append(name)
    return name


def run_container(name, ref, net=None, alias=None, env=None, ports=None, volumes=None, cmd=None):
    args = ["run", "-d", "--name", name]
    if net:
        args += ["--network", net]
        args += ["--network-alias", alias or name]
    for host, cport in (ports or []):
        args += ["-p", "127.0.0.1:%d:%d" % (host, cport)]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    for spec in (volumes or []):
        args += ["-v", spec]
    args.append(ref)
    if cmd:
        args += list(cmd)
    res = docker(*args)
    if res.returncode != 0:
        die("docker run %s failed: %s" % (name, res.stderr.strip()))
    if name not in STATE["containers"]:
        STATE["containers"].append(name)
    return name


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def teardown():
    for name in STATE["containers"]:
        docker("rm", "-f", name)
    STATE["containers"] = []
    for net in STATE["networks"]:
        docker("network", "rm", net)
    STATE["networks"] = []


def call(method, path, body=None, headers=None, base=None):
    data = None if body is None else json.dumps(body).encode()
    hdrs = {"Content-Type": "application/json"}
    if headers:
        hdrs.update(headers)
    req = urllib.request.Request((base or STATE["base"]) + path, data=data, headers=hdrs, method=method)
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


def raw_post(url, body, headers=None):
    req = urllib.request.Request(url, data=body, headers=headers or {}, method="POST")
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()
    except urllib.error.URLError as exc:
        return 0, str(exc)


def boot_rimsky(slug, net=None, alias=None, config_path=None, env=None, volumes=None):
    port = free_port()
    name = "rimsky-exp-%s-api-%s" % (slug, uuid.uuid4().hex[:6])
    vols = list(volumes or [])
    if config_path:
        vols.append("%s:/etc/rimsky/rimsky.yml:ro" % config_path)
    run_container(name, image("rimsky-all-in-one"), net=net, alias=alias or "rimsky",
                  env=env, ports=[(port, 8080)], volumes=vols)
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                return name
        except Exception:
            pass
        time.sleep(0.3)


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


def instance(iid):
    return call("GET", "/v1/instances/%s" % iid)[1]


def wait_subscriptions_active(iid):
    while True:
        d = instance(iid)
        subs = d.get("subscriptions") or []
        for s in subs:
            if s["state"] == "failed":
                die("publisher subscription %s failed: %s" % (s["publisher_name"], s.get("failure_reason")))
        if subs and all(s["state"] == "active" for s in subs):
            return subs
        time.sleep(0.25)


def messages(iid, query=""):
    return call("GET", "/v1/instances/%s/messages%s" % (iid, query))[1]["messages"]


def frames(iid):
    return call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]


def nodes(iid):
    out = call("GET", "/v1/instances/%s/nodes" % iid)[1]
    return out["nodes"] if isinstance(out, dict) else out


def node_runs(iid):
    return {n["node_type"]: n["run_summary"]["fresh_count"] + n["run_summary"]["failed_count"]
            for n in nodes(iid) if n.get("node_type")}


def wait_until(fn, note=""):
    while True:
        value = fn()
        if value:
            return value
        time.sleep(0.25)


def wait_node_ran(iid, node_type, count=1):
    return wait_until(lambda: node_runs(iid).get(node_type, 0) >= count)


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + str(detail)[:300]) if detail else ""))


def finish():
    teardown()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


SLUG = "frame-origin-audit"
HERE = os.path.dirname(os.path.abspath(__file__))
HOOK_SECRET = "frame-origin-hook-secret"

SPEC = {
    "name": "exp-frame-origin-audit",
    "version": "1",
    "messages": [
        {"type": "loop/iterate", "body_schema": {"type": "object",
                                                 "properties": {"value": {"type": "integer"}},
                                                 "required": ["value"]}},
        {"type": "hook/in"},
        {"type": "ops/kick"},
    ],
    "nodes": [
        {"type": "counter", "kind": "loop_counter",
         "subscribes": [{"node": "ops/kick", "type": "terminal/success", "force_upstream_refresh": False}],
         "attributes": {"schema": {"type": "object", "properties": {
             "max": {"type": "integer", "default": 9}, "count": {"type": "integer"}}}}},
        {"type": "sender", "sends_message": "loop/iterate",
         "subscribes": [{"node": "counter", "type": "terminal/success", "force_upstream_refresh": False},
                        {"node": "counter", "type": "attribute/count/changed", "force_upstream_refresh": False}],
         "attributes": {"schema": {"type": "object", "properties": {
             "value": {"type": "integer", "source": "{{nodes.counter.attribute.count}}"}},
             "required": ["value"]}}},
        {"type": "receiver", "kind": "attribute_passthrough",
         "subscribes": [{"node": "loop/iterate", "type": "terminal/success", "force_upstream_refresh": False}],
         "attributes": {"schema": {"type": "object", "properties": {
             "got": {"type": "integer", "source": "{{messages.loop/iterate.value}}"}}}}},
        {"type": "hooked", "kind": "attribute_passthrough",
         "subscribes": [{"node": "hook/in", "type": "terminal/success", "force_upstream_refresh": False}],
         "attributes": {"schema": {"type": "object", "properties": {"v": {"type": "integer", "default": 1}}}}},
    ],
    "publishers": [{"name": "hook", "kind": "webhook", "message_type": "hook/in",
                    "config": {"path_prefix": "/inbound",
                               "auth": {"mode": "secret_header", "header": "X-Token",
                                        "secret": HOOK_SECRET}}}],
}


def main():
    net = network(SLUG)
    hook_port = free_port()
    run_container("rimsky-exp-frameorigin-hook-" + uuid.uuid4().hex[:6], image("rimsky-sensor-webhook"),
                  net=net, alias="webhook-sensor", ports=[(hook_port, 9184)], env={
                      "RIMSKY_SENSOR_WEBHOOK_PORT": "9084",
                      "RIMSKY_SENSOR_WEBHOOK_HTTP_PORT": "9184",
                      "RIMSKY_CONTROL_API_URL": "http://rimsky:8080",
                  })
    hook = "http://127.0.0.1:%d" % hook_port

    def health():
        try:
            return call("GET", "/health", base=hook)[0] == 200 or None
        except Exception:
            return None

    wait_until(health)
    boot_rimsky(SLUG, net=net, alias="rimsky", config_path=os.path.join(HERE, "rimsky.yml"))
    tid = deploy(SPEC)
    iid = new_instance(tid)
    wait_subscriptions_active(iid)

    status, _ = send_message(iid, {"type": "ops/kick"})
    check("an operator message is accepted", status == 201, status)
    wait_node_ran(iid, "receiver", 1)

    status, _ = raw_post(hook + "/inbound", json.dumps({"event": "poke"}).encode(),
                         {"Content-Type": "application/json", "X-Token": HOOK_SECRET})
    check("a webhook POST is accepted", status == 200, status)
    wait_node_ran(iid, "hooked", 1)

    fs = wait_until(lambda: (frames(iid) if len(frames(iid)) >= 3 else None))
    kinds = sorted(f.get("message_sender_kind", "") for f in fs)
    check("every frame the instance opened names the message that triggered it",
          all(f.get("triggering_message_id") and f.get("message_type")
              and f.get("message_sender") and f.get("message_sender_kind") for f in fs),
          json.dumps([(f.get("message_type"), f.get("message_sender"), f.get("message_sender_kind")) for f in fs]))
    check("the frame list distinguishes an operator-triggered frame",
          any(f["message_sender_kind"] == "operator" for f in fs), json.dumps(kinds))
    check("the frame list distinguishes a publisher-triggered frame",
          any(f["message_sender_kind"] == "publisher" and f.get("message_type") == "hook/in" for f in fs),
          json.dumps(kinds))
    check("the frame list distinguishes a frame triggered by a message the instance itself sent",
          any(f["message_sender_kind"] == "instance" and f.get("message_type") == "loop/iterate" for f in fs),
          json.dumps(kinds))
    check("the three trigger kinds are the three the instance actually produced",
          set(kinds) == {"operator", "publisher", "instance"}, json.dumps(kinds))

    for f in fs:
        status, one = call("GET", "/v1/instances/%s/frames/%s" % (iid, f["frame_id"]))
        if status != 200 or one.get("message_sender_kind") != f["message_sender_kind"]:
            check("reading one frame gives the same trigger the list gave", False, json.dumps(one))
            break
    else:
        check("reading one frame gives the same trigger the list gave", True,
              "%d frames read back individually" % len(fs))

    ok = True
    for f in fs:
        status, msg = call("GET", "/v1/messages/%s" % f["triggering_message_id"])
        if status != 200 or msg["type"] != f.get("message_type") or msg["sender_kind"] != f["message_sender_kind"]:
            ok = False
            check("the triggering message id resolves to the message the frame names", False, json.dumps(msg))
            break
    if ok:
        check("the triggering message id resolves to the message the frame names", True,
              "%d triggering messages resolved" % len(fs))

    target = [f for f in fs if f["message_sender_kind"] == "instance"][0]
    status, filtered = call("GET", "/v1/instances/%s/frames?triggering_message_id=%s"
                            % (iid, target["triggering_message_id"]))
    check("the frame list can be narrowed to one triggering message",
          status == 200 and [x["frame_id"] for x in filtered["frames"]] == [target["frame_id"]],
          json.dumps([x["frame_id"] for x in filtered["frames"]]))
    finish()


try:
    main()
finally:
    teardown()
