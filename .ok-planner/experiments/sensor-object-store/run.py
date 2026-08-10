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


import shutil
import tempfile

SLUG = "sensor-object-store"
HERE = os.path.dirname(os.path.abspath(__file__))

SPEC = {
    "name": "exp-sensor-object-store",
    "version": "1",
    "messages": [{"type": "drop/arrived"}],
    "nodes": [{
        "type": "handler",
        "kind": "attribute_passthrough",
        "subscribes": [{"node": "drop/arrived", "type": "terminal/success", "force_upstream_refresh": False}],
        "attributes": {"schema": {"type": "object", "properties": {"v": {"type": "integer", "default": 1}}}},
    }],
    "publishers": [{
        "name": "inbox", "kind": "object-store", "message_type": "drop/arrived",
        "config": {"backend": "filesystem", "bucket": "inbox", "prefix": "in/",
                   "poll_interval": "1s", "watermark_field": "name"},
    }],
}


def arrivals(iid):
    return sorted([m for m in messages(iid) if m["type"] == "drop/arrived"],
                  key=lambda m: m["received_at"])


def names(iid):
    return [m["payload"]["object_name"] for m in arrivals(iid)]


def deposit(root, name, content):
    path = os.path.join(root, "inbox", name)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    tmp = os.path.join(root, "staging", uuid.uuid4().hex)
    with open(tmp, "w") as fh:
        fh.write(content)
    os.replace(tmp, path)


def main():
    root = tempfile.mkdtemp(prefix="rimsky-exp-objstore-")
    STATE["tmp"].append(root)
    os.makedirs(os.path.join(root, "inbox", "in"))
    os.makedirs(os.path.join(root, "staging"))

    net = network(SLUG)
    sensor = "rimsky-exp-objstore-sensor-" + uuid.uuid4().hex[:6]
    run_container(sensor, image("rimsky-sensor-object-store"), net=net, alias="object-store-sensor",
                  volumes=["%s:/data:ro" % root], env={
                      "RIMSKY_SENSOR_OBJECT_STORE_PORT": "9083",
                      "RIMSKY_CONTROL_API_URL": "http://rimsky:8080",
                      "RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT": "/data",
                  })
    booted = wait_until(lambda: "backend registered" in logs(sensor) or None)
    check("the operator designates the watched location with an environment root and nothing else",
          bool(booted) and "filesystem" in logs(sensor), "filesystem backend registered")

    boot_rimsky(SLUG, net=net, alias="rimsky", config_path=os.path.join(HERE, "rimsky.yml"))
    tid = deploy(SPEC)
    iid = new_instance(tid)
    subs = wait_subscriptions_active(iid)
    check("the template's object-store subscription mounts live on the instance",
          len(subs) == 1 and subs[0]["kind"] == "object-store", json.dumps(subs))

    deposit(root, "in/alpha.txt", "first payload")
    first = wait_until(lambda: (arrivals(iid) or [None])[0])
    check("depositing content into the designated location hands it to the graph",
          first["sender_kind"] == "publisher" and first["payload"]["object_name"] == "in/alpha.txt"
          and first["payload"]["bucket"] == "inbox" and first["payload"]["backend"] == "filesystem",
          json.dumps(first["payload"]))
    check("the handed-over message describes the deposited object",
          first["payload"]["size"] == len("first payload") and bool(first["payload"]["etag"])
          and bool(first["payload"]["last_modified"]), json.dumps(first["payload"]))
    wait_node_ran(iid, "handler", 1)
    check("the deposit drove the subscribed node to run without any operator message",
          node_runs(iid).get("handler", 0) == 1
          and all(m["sender_kind"] == "publisher" for m in messages(iid)),
          json.dumps(node_runs(iid)))

    deposit(root, "in/beta.txt", "second payload")
    wait_until(lambda: len(arrivals(iid)) >= 2 or None)
    check("a second deposit is handed over as its own message",
          names(iid) == ["in/alpha.txt", "in/beta.txt"], json.dumps(names(iid)))
    wait_node_ran(iid, "handler", 2)
    check("each deposit drives one node run", node_runs(iid).get("handler", 0) == 2,
          json.dumps(node_runs(iid)))

    deposit(root, "elsewhere/gamma.txt", "outside the designated prefix")
    deposit(root, "in/delta.txt", "third payload")
    wait_until(lambda: len(arrivals(iid)) >= 3 or None)
    check("only content under the designated prefix is handed to the graph",
          names(iid) == ["in/alpha.txt", "in/beta.txt", "in/delta.txt"], json.dumps(names(iid)))
    check("content already handed over is not handed over again",
          len(names(iid)) == len(set(names(iid))) == 3, json.dumps(names(iid)))
    wait_node_ran(iid, "handler", 3)
    check("the graph ran once per deposited object and no more",
          node_runs(iid).get("handler", 0) == 3, json.dumps(node_runs(iid)))
    finish()


try:
    main()
finally:
    teardown()
    for d in STATE["tmp"]:
        shutil.rmtree(d, ignore_errors=True)
