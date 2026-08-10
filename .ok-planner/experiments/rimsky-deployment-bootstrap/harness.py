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
ALL_IN_ONE = "rimsky-all-in-one:" + TAG
RIMSKY = "rimsky:" + TAG
STATE = {"base": None, "containers": [], "networks": [], "checks": []}
SETTLED_FRAME_STATES = ("completed", "failed", "terminated")


def die(msg):
    print("HARNESS ERROR: " + msg)
    teardown()
    sys.exit(2)


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def require_image(image):
    if docker("image", "inspect", image).returncode != 0:
        die("image %s is not present locally; build it with: "
            "make core-images service-images test-images" % image)


def free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def new_network(subnet):
    name = "rimsky-exp-net-" + uuid.uuid4().hex[:8]
    res = docker("network", "create", "--subnet", subnet, name)
    if res.returncode != 0:
        die("docker network create failed: " + res.stderr.strip())
    STATE["networks"].append(name)
    return name


def run_container(image, name=None, env=None, network=None, publish=None,
                  command=None, mounts=None, extra=None, detach=True):
    name = name or ("rimsky-exp-" + uuid.uuid4().hex[:8])
    args = ["run", "--name", name]
    if detach:
        args.append("-d")
    if publish:
        args += ["-p", "127.0.0.1:%d:%d" % publish]
    if network:
        args += ["--network", network]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    for host, guest in (mounts or []):
        args += ["-v", "%s:%s:ro" % (host, guest)]
    args += extra or []
    args.append(image)
    args += command or []
    res = docker(*args)
    STATE["containers"].append(name)
    return name, res


def start_endpoint(network, source, name=None, env=None):
    name = name or ("rimsky-exp-endpoint-" + uuid.uuid4().hex[:8])
    port = free_port()
    _, res = run_container("python:3.12-alpine", name=name, network=network,
                           publish=(port, 8000), env=env,
                           mounts=[(source, "/srv/endpoint.py")],
                           command=["python", "/srv/endpoint.py"])
    if res.returncode != 0:
        die("could not start the rate-limited endpoint: " + res.stderr.strip())
    base = "http://127.0.0.1:%d" % port
    while True:
        try:
            with urllib.request.urlopen(base + "/_log") as resp:
                if resp.status == 200:
                    return name, base
        except Exception:
            time.sleep(0.2)


def endpoint_log(base):
    with urllib.request.urlopen(base + "/_log") as resp:
        return json.loads(resp.read().decode())["requests"]


def wait_healthy(base):
    while True:
        try:
            req = urllib.request.Request(base + "/v1/health")
            with urllib.request.urlopen(req) as resp:
                if resp.status == 200:
                    return
        except Exception:
            pass
        time.sleep(0.3)


def boot(env=None, network=None, mounts=None, image=None, command=None, name=None):
    image = image or ALL_IN_ONE
    require_image(image)
    port = free_port()
    cname, res = run_container(image, name=name, env=env, network=network,
                               publish=(port, 8080), mounts=mounts, command=command)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
    base = "http://127.0.0.1:%d" % port
    STATE["base"] = base
    wait_healthy(base)
    return cname, base


def logs(container):
    res = docker("logs", container)
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
    req = urllib.request.Request((base or STATE["base"]) + path, data=data,
                                 headers=hdrs, method=method)
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


def deploy(spec, base=None):
    status, out = call("POST", "/v1/templates", {"spec": spec}, base=base)
    if status not in (200, 201):
        die("template register rejected: %s %s" % (status, out))
    template_id = out["template_id"]
    status, out = call("POST", "/v1/templates/%s/deploy" % template_id, {}, base=base)
    if status not in (200, 201):
        die("template deploy rejected: %s %s" % (status, out))
    return template_id


def new_instance(template_id, base=None):
    status, out = call("POST", "/v1/instances", {
        "template": template_id,
        "instance_key": "exp-" + uuid.uuid4().hex[:12],
        "target_agent": "audit-agent"}, base=base)
    if status not in (200, 201):
        die("instance create rejected: %s %s" % (status, out))
    return out["instance_id"]


def send_message(iid, body=None, base=None):
    return call("POST", "/v1/instances/%s/messages" % iid,
                {} if body is None else body,
                {"Idempotency-Key": uuid.uuid4().hex}, base=base)


def node_types(iid, base=None):
    return {n["id"]: n["node_type"]
            for n in call("GET", "/v1/instances/%s/nodes" % iid, base=base)[1]["nodes"]}


def live_runs(iid, base=None):
    return call("GET", "/v1/observability/node-runs?instance_id=%s" % iid,
                base=base)[1]["node_runs"]


def timeline(iid, base=None):
    types = node_types(iid, base=base)
    rows = call("GET", "/v1/observability/events?instance_id=%s&limit=500" % iid,
                base=base)[1]["events"]
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


def quiet(iid, base=None):
    while True:
        frames = call("GET", "/v1/instances/%s/frames" % iid, base=base)[1]["frames"]
        if frames and all(f["state"] in SETTLED_FRAME_STATES for f in frames) \
                and not live_runs(iid, base=base):
            return timeline(iid, base=base)
        time.sleep(0.25)


def wait_hits(iid, count, base=None):
    while True:
        hits = (call("GET", "/v1/instances/%s/breakpoint-hits" % iid, base=base)[1]
                or {}).get("hits") or []
        if len(hits) >= count:
            return hits
        time.sleep(0.2)


def wait_until(fn):
    while True:
        value = fn()
        if value:
            return value
        time.sleep(0.25)


def terminals(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"].startswith("terminal/")]


def deltas(tl, node):
    return [r["payload"].get("attributes_delta") for r in terminals(tl, node)]


def starts(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"] == "work_started"]


def show(tl):
    for r in tl:
        if r["kind"] == "work_started" or r["kind"].startswith("terminal/") \
                or r["kind"].startswith("parked") or r["kind"].startswith("transient/"):
            print("    %-5s %-22s %-34s %s"
                  % (r["seq"], r["node"], r["kind"], json.dumps(r["payload"])[:110]))


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


def passthrough_node(node_type, subscribes=None, properties=None):
    node = {
        "type": node_type,
        "kind": "attribute_passthrough",
        "attributes": {"schema": {"type": "object",
                                  "properties": properties or {"v": {"type": "integer", "default": 1}}}},
    }
    if subscribes:
        node["subscribes"] = subscribes
    return node


def sub(node_type, signal, force=False, when=None):
    entry = {"node": node_type, "type": signal, "force_upstream_refresh": force}
    if when:
        entry["when"] = when
    return entry
