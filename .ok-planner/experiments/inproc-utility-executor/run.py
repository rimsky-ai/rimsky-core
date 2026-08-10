import base64
import json
import os
import shutil
import socket
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

HERE = os.path.dirname(os.path.abspath(__file__))
TAG = os.environ.get("RIMSKY_IMAGE_TAG", "latest")
IMAGE = "rimsky-all-in-one:" + TAG
STATE = {"base": None, "containers": [], "servers": [], "checks": [], "proc": None}
SETTLED_FRAME_STATES = ("completed", "failed", "terminated")


def die(msg):
    print("HARNESS ERROR: " + msg)
    teardown()
    sys.exit(2)


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def require_image(image):
    if docker("image", "inspect", image).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images" % image)


def free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def run_container(image, env=None, ports=None, files=None, mounts=None):
    require_image(image)
    name = "rk-exp-" + uuid.uuid4().hex[:8]
    args = ["run", "-d", "--name", name, "--add-host", "host.docker.internal:host-gateway"]
    for host_port, container_port in (ports or []):
        args += ["-p", "127.0.0.1:%d:%d" % (host_port, container_port)]
    for host_path, container_path in (files or []):
        args += ["-v", "%s:%s:ro" % (host_path, container_path)]
    for host_path, container_path in (mounts or []):
        args += ["-v", "%s:%s" % (host_path, container_path)]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(image)
    res = docker(*args)
    if res.returncode != 0:
        die("docker run %s failed: %s" % (image, res.stderr.strip()))
    STATE["containers"].append(name)
    return name


def container_logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def teardown():
    for name in STATE["containers"]:
        docker("rm", "-f", name)
    STATE["containers"] = []
    for srv in STATE["servers"]:
        srv.shutdown()
    STATE["servers"] = []
    if STATE["proc"]:
        STATE["proc"].kill()
        STATE["proc"] = None


def purge_build():
    shutil.rmtree(os.path.join(HERE, ".probe-build"), ignore_errors=True)


def serve_http(handler_cls):
    port = free_port()
    srv = ThreadingHTTPServer(("0.0.0.0", port), handler_cls)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    STATE["servers"].append(srv)
    return port


class JSONHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def read_json(self):
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b""
        return json.loads(raw or b"{}")

    def send_json(self, status, obj, extra_headers=None):
        raw = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        for key, value in (extra_headers or {}).items():
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(raw)


def boot_rimsky(env=None, files=None, mounts=None):
    port = free_port()
    full_env = {"RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST": "127.0.0.1"}
    full_env.update(env or {})
    name = run_container(IMAGE, env=full_env, ports=[(port, 8080)], files=files, mounts=mounts)
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                return name
        except Exception:
            pass
        if docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() != "true":
            die("rimsky container exited during boot:\n" + container_logs(name))
        time.sleep(0.3)


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


def nodes_of(iid):
    return call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]


def node_types(iid):
    return {n["id"]: n["node_type"] for n in nodes_of(iid)}


def live_runs(iid):
    return call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]


def timeline(iid):
    types = node_types(iid)
    rows = call("GET", "/v1/observability/events?instance_id=%s&limit=500" % iid)[1]["events"] or []
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


def starts(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"] == "work_started"]


def terminals(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"].startswith("terminal/")]


def transients(tl, node):
    return [r for r in tl if r["node"] == node and r["kind"].startswith("transient/")]


def deltas(tl, node):
    return [r["payload"].get("attributes_delta") for r in terminals(tl, node)]


def show(tl):
    for r in tl:
        if r["kind"] == "work_started" or r["kind"].startswith("terminal/") or r["kind"].startswith("transient/"):
            print("    %-5s %-22s %-40s %s" % (r["seq"], r["node"], r["kind"], json.dumps(r["payload"])[:220]))


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def finish():
    teardown()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    sys.exit(1 if failed else 0)


def sub(node_type, signal, force=False, when=None):
    entry = {"node": node_type, "type": signal, "force_upstream_refresh": force}
    if when:
        entry["when"] = when
    return entry


def docker_arch():
    arch = docker("version", "--format", "{{.Server.Arch}}").stdout.strip()
    if not arch:
        die("cannot read docker server architecture")
    return arch


def build_probe_agent():
    workdir = os.path.join(HERE, ".probe-build")
    os.makedirs(workdir, exist_ok=True)
    with open(os.path.join(workdir, "main.go"), "w") as fh:
        fh.write(open(os.path.join(HERE, "probe-agent.go.txt")).read())
    with open(os.path.join(workdir, "go.mod"), "w") as fh:
        fh.write("module probeagent\n\ngo 1.25\n")
    linux_bin = os.path.join(workdir, "probe-agent")
    res = subprocess.run(["go", "build", "-o", linux_bin, "."], cwd=workdir,
                         env=dict(os.environ, GOOS="linux", GOARCH=docker_arch(), CGO_ENABLED="0"),
                         capture_output=True, text=True)
    if res.returncode != 0:
        die("go build (container target) failed: " + res.stderr.strip())
    host_bin = os.path.join(workdir, "probe-agent-host")
    res = subprocess.run(["go", "build", "-o", host_bin, "."], cwd=workdir,
                         env=dict(os.environ, CGO_ENABLED="0"), capture_output=True, text=True)
    if res.returncode != 0:
        die("go build (host target) failed: " + res.stderr.strip())
    res = subprocess.run([host_bin, "-genkey", workdir], capture_output=True, text=True)
    if res.returncode != 0:
        die("probe-agent -genkey failed: " + res.stderr.strip())
    return {
        "dir": workdir,
        "binary": linux_bin,
        "private_key": os.path.join(workdir, "signoff-private.pem"),
        "public_key_pem": open(os.path.join(workdir, "signoff-public.pem")).read(),
    }


def agent_prompt(**directives):
    return "\n".join("probe.%s=%s" % (k, v) for k, v in sorted(directives.items()))


def b64json(obj):
    return base64.b64encode(json.dumps(obj).encode()).decode()


UTILITY_KINDS = ("loop_counter", "attribute_passthrough", "send_message")

SPEC = {
    "name": "exp-inproc-utility-executor",
    "version": "1",
    "messages": [{"type": "util/ping", "body_schema": {
        "type": "object",
        "properties": {"value": {"type": "integer"}},
        "required": ["value"]}}],
    "nodes": [
        {
            "type": "counter",
            "kind": "loop_counter",
            "attributes": {"schema": {"type": "object", "properties": {
                "max": {"type": "integer", "default": 1},
                "count": {"type": "integer"}}}},
        },
        {
            "type": "sender",
            "sends_message": "util/ping",
            "subscribes": [sub("counter", "terminal/success"), sub("counter", "attribute/count/changed")],
            "attributes": {"schema": {"type": "object",
                                      "properties": {"value": {"type": "integer",
                                                               "source": "{{nodes.counter.attribute.count}}"}},
                                      "required": ["value"]}},
        },
        {
            "type": "receiver",
            "kind": "attribute_passthrough",
            "subscribes": [sub("util/ping", "terminal/success")],
            "attributes": {"schema": {"type": "object", "properties": {
                "got": {"type": "integer", "source": "{{messages.util/ping.value}}"}}}},
        },
    ],
}


def main():
    boot_rimsky()

    configured = call("GET", "/v1/observability/executors")[1]["executors"]
    remote = [e["name"] for e in configured if not e["endpoint"].startswith("inproc://")]
    check("the deployment has no executor reachable at a service address",
          remote == [], json.dumps([(e["name"], e["endpoint"]) for e in configured]))

    iid = new_instance(deploy(SPEC))
    send_message(iid)
    tl = quiet(iid)
    show(tl)

    for node_type, kind in (("counter", "loop_counter"),
                            ("sender", "send_message"),
                            ("receiver", "attribute_passthrough")):
        check("the %s node kind dispatched with no executor service registered for it" % kind,
              len(starts(tl, node_type)) >= 1,
              "starts=%d" % len(starts(tl, node_type)))
        check("the %s node kind settled successfully" % kind,
              [r["kind"] for r in terminals(tl, node_type)] == ["terminal/success"],
              json.dumps([r["kind"] for r in terminals(tl, node_type)]))

    check("the loop-counter emitted its count", deltas(tl, "counter") == [{"count": 1}],
          json.dumps(deltas(tl, "counter")))
    messages = call("GET", "/v1/instances/%s/messages" % iid)[1]["messages"]
    sent = [m for m in messages if m["type"] == "util/ping"]
    check("the send-message kind put one message in the ledger", len(sent) == 1, json.dumps(sent)[:200])
    check("the attribute-passthrough kind carried the message body into its output attributes",
          deltas(tl, "receiver") == [{"got": 1}], json.dumps(deltas(tl, "receiver")))

    check("all three bundled utility kinds ran in one template with no executor deployment",
          len(UTILITY_KINDS) == 3 and all(
              len(terminals(tl, t)) == 1 for t in ("counter", "sender", "receiver")))
    finish()


try:
    main()
finally:
    teardown()
