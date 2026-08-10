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
                         env=dict(os.environ, GOOS="linux", GOARCH=docker_arch(), CGO_ENABLED="0", GOWORK="off"),
                         capture_output=True, text=True)
    if res.returncode != 0:
        die("go build (container target) failed: " + res.stderr.strip())
    host_bin = os.path.join(workdir, "probe-agent-host")
    res = subprocess.run(["go", "build", "-o", host_bin, "."], cwd=workdir,
                         env=dict(os.environ, CGO_ENABLED="0", GOWORK="off"), capture_output=True, text=True)
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


PROBE_BINARY = "/probe/probe-agent"
PRODUCER_CONFIG = "root: /workspace\nhost: 127.0.0.1\ngrpc_port: 9200\nhttp_port: 9210\nsweep_interval_seconds: 60\n"


def agent_node(node_type, prompt, extra=None):
    node = {"type": node_type, "executor": "claude-agent",
            "attributes": {"schema": {"type": "object", "properties": {
                "model": {"type": "string", "default": "probe-model"},
                "system_prompt": {"type": "string", "default": "dispatch-context probe"},
                "user_prompt": {"type": "string", "default": prompt},
                "cli": {"type": "object", "default": {}}}}}}
    node.update(extra or {})
    return node


SPEC = {
    "name": "exp-executor-reads-dispatch-context",
    "version": "1",
    "nodes": [
        agent_node("fresh", agent_prompt(mode="observe", read_dispatch_context="1")),
        agent_node("recovered",
                   agent_prompt(mode="adapt", first="hang", read_dispatch_context="1"),
                   {"max_quiet_period": "2s"}),
        agent_node("partitioned", agent_prompt(mode="report"),
                   {"claim_producers": [{"name": "claim-producer-filesystem", "selector": "data",
                                         "intent": "rw", "alias": "parent"}],
                    "error_types": {"acquire/unavailable": {"action": "give_up"}},
                    "fan_out": {"claim": "parent",
                                "partition_request": '{"list":[{"key":"p1"},{"key":"p2"}]}',
                                "parallelism": 2,
                                "error_policy": {"kind": "strict"}}}),
        agent_node("receiver", agent_prompt(mode="observe", read_dispatch_context="1"),
                   {"subscribes": [sub("partitioned", "terminal/success")]}),
    ],
}


def contexts_of(tl, node_type):
    out = []
    for r in terminals(tl, node_type):
        if r["kind"] != "terminal/success":
            continue
        obs = (r["payload"].get("attributes_delta") or {}).get("cli_observation") or {}
        if obs.get("dispatch_context"):
            out.append(obs["dispatch_context"])
    return out


def main():
    probe = build_probe_agent()
    workspace = os.path.join(HERE, ".probe-build", "workspace")
    os.makedirs(os.path.join(workspace, "data"), exist_ok=True)
    producer_path = os.path.join(HERE, ".probe-build", "claim-producer-filesystem.yml")
    with open(producer_path, "w") as fh:
        fh.write(PRODUCER_CONFIG)

    boot_rimsky(env={"CLAUDE_CODE_OAUTH_TOKEN": "probe-stand-in",
                     "RIMSKY_EXECUTOR_CLAUDE_BINARY": PROBE_BINARY,
                     "RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG":
                         "/etc/rimsky/claim-producer-filesystem.yml"},
                files=[(probe["binary"], PROBE_BINARY),
                       (producer_path, "/etc/rimsky/claim-producer-filesystem.yml")],
                mounts=[(workspace, "/workspace")])
    iid = new_instance(deploy(SPEC))
    send_message(iid)
    tl = quiet(iid)
    show(tl)

    fresh = contexts_of(tl, "fresh")
    fresh_run = starts(tl, "fresh")[0]["payload"]["dispatch_id"]
    check("a fresh dispatch reads its own dispatch identity",
          len(fresh) == 1 and fresh[0].get("dispatch_id") == fresh_run, json.dumps(fresh))
    check("a fresh dispatch reads the run-scope it belongs to",
          bool(fresh and fresh[0].get("run_scope_id")), json.dumps(fresh))
    check("a fresh dispatch reads no predecessor",
          bool(fresh) and fresh[0].get("prior_dispatch_id") is None
          and fresh[0].get("prior_dispatch_disposition") is None, json.dumps(fresh))

    recovered = contexts_of(tl, "recovered")
    check("the dispatch that follows a stale recovery reads a predecessor",
          bool(recovered) and bool(recovered[-1].get("prior_dispatch_id")), json.dumps(recovered))
    check("the dispatch that follows a stale recovery reads the recovery disposition",
          bool(recovered) and recovered[-1].get("prior_dispatch_disposition") == "stale_recovery",
          json.dumps(recovered))
    check("the recovered node reached success only by adapting to its predecessor",
          [r["kind"] for r in terminals(tl, "recovered")][-1] == "terminal/success",
          json.dumps([r["kind"] for r in terminals(tl, "recovered")]))

    receiver = contexts_of(tl, "receiver")
    recalculated = [c for c in receiver if c.get("prior_dispatch_disposition") == "recalculate"]
    check("a recalculated dispatch reads the recalculate disposition and its predecessor",
          bool(recalculated) and bool(recalculated[-1].get("prior_dispatch_id")),
          json.dumps(receiver))

    dispositions = sorted({c.get("prior_dispatch_disposition") for c in
                           (fresh + recovered + receiver)}, key=lambda v: (v is None, v or ""))
    check("the script distinguishes the recovery paths without any indirect signal",
          dispositions == ["recalculate", "stale_recovery", None], json.dumps(dispositions))
    finish()


try:
    main()
finally:
    teardown()
    purge_build()
