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


import hashlib

PROBE_BINARY = "/probe/probe-agent"
PROBE_KEY = "/probe/signoff-private.pem"
VALIDATOR_TOKEN = "validator-token-plaintext-do-not-leak-7f1c9a"
REVIEWER_SEED = "reviewer-seed-plaintext-do-not-leak-3b2d1e"
MCP_URL = "http://127.0.0.1:9/mcp/validator"


def agent_node(node_type, prompt, cli=None, extra=None):
    node = {"type": node_type, "executor": "claude-agent",
            "attributes": {"schema": {"type": "object", "properties": {
                "model": {"type": "string", "default": "probe-model"},
                "system_prompt": {"type": "string", "default": "agentic node probe"},
                "user_prompt": {"type": "string", "default": prompt},
                "cli": {"type": "object", "default": cli or {}}}}}}
    node.update(extra or {})
    return node


def spec_for(public_pem):
    signoff = [{"public_key": public_pem, "path": "note"}]
    observe = dict(mode="observe", env="VALIDATOR_TOKEN,REVIEWER_SEED",
                   sign_key=PROBE_KEY)
    return {
        "name": "exp-claude-agent",
        "version": "1",
        "nodes": [
            agent_node("worker", agent_prompt(**observe),
                       {"mcp_servers": [{"transport": "http", "name": "validator", "url": MCP_URL}],
                        "expose_env": ["VALIDATOR_TOKEN"],
                        "required_signoffs": signoff}),
            agent_node("forged", agent_prompt(sign_binding="not-this-dispatch", **observe),
                       {"required_signoffs": signoff, "max_signoff_attempts": 0}),
            agent_node("tampered", agent_prompt(sign_value="a-value-never-written", **observe),
                       {"required_signoffs": signoff, "max_signoff_attempts": 0}),
            agent_node("passed", agent_prompt(mode="error", error_class="agent/context_exceeded"),
                       extra={"error_types": {"agent/context_exceeded": {"action": "pass"}}}),
            agent_node("refused", agent_prompt(mode="error", error_class="agent/refused")),
            agent_node("undeclared", agent_prompt(mode="error", error_class="probe/not-declared")),
            {"type": "watcher", "kind": "attribute_passthrough",
             "subscribes": [sub("refused", "terminal/error/agent/*")],
             "attributes": {"schema": {"type": "object", "properties": {
                 "noticed": {"type": "boolean", "default": True}}}}},
        ],
    }


def last_terminal(tl, node_type):
    rows = terminals(tl, node_type)
    return rows[-1] if rows else {"kind": None, "payload": {}}


def observation(tl, node_type):
    delta = last_terminal(tl, node_type)["payload"].get("attributes_delta") or {}
    return delta.get("cli_observation") or {}


def main():
    probe = build_probe_agent()
    boot_rimsky(env={"CLAUDE_CODE_OAUTH_TOKEN": "probe-stand-in",
                     "RIMSKY_EXECUTOR_CLAUDE_BINARY": PROBE_BINARY,
                     "RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST": "validator",
                     "RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST": "VALIDATOR_TOKEN",
                     "VALIDATOR_TOKEN": VALIDATOR_TOKEN,
                     "REVIEWER_SEED": REVIEWER_SEED},
                files=[(probe["binary"], PROBE_BINARY), (probe["private_key"], PROBE_KEY)])

    advertised = (call("GET", "/v1/observability/executors/claude-agent")[1] or {}).get("peer") or {}
    classes = (advertised.get("observability_capabilities") or {}).get("declared_error_classes") or []
    check("the bundled agent executor is deployed and advertises a declared error-class vocabulary",
          "agent/refused" in classes and "agent/signoff_unobtained" in classes,
          json.dumps(classes))

    iid = new_instance(deploy(spec_for(probe["public_key_pem"])))
    send_message(iid)
    tl = quiet(iid)
    show(tl)

    awaits = [r for r in transients(tl, "worker") if r["kind"] == "transient/await_async"]
    check("the agent node's work is dispatched asynchronously and settled by callback",
          len(awaits) == 1 and last_terminal(tl, "worker")["kind"] == "terminal/success",
          json.dumps([r["kind"] for r in transients(tl, "worker")]))

    obs = observation(tl, "worker")
    check("the node's own inline MCP declaration is the tool surface its agent gets",
          sorted(obs.get("mcp_servers") or []) == ["rimsky-callback:http", "validator:http"],
          json.dumps(obs.get("mcp_servers")))
    check("the node's own inline expose-env declaration is the secret surface its agent gets",
          obs.get("env_present") == ["VALIDATOR_TOKEN"]
          and (obs.get("env_digest") or {}).get("VALIDATOR_TOKEN")
          == hashlib.sha256(VALIDATOR_TOKEN.encode()).hexdigest(),
          json.dumps(obs.get("env_present")))

    delta = last_terminal(tl, "worker")["payload"].get("attributes_delta") or {}
    worker_id = [n["id"] for n in nodes_of(iid) if n["node_type"] == "worker"][0]
    committed = (call("GET", "/v1/nodes/%s" % worker_id)[1] or {}).get("latest_attributes") or {}
    check("a run whose sign-off covers the value it writes out is committed",
          last_terminal(tl, "worker")["kind"] == "terminal/success" and bool(delta.get("note")),
          json.dumps({"note": delta.get("note")}))
    check("the value the sign-off was taken over is the value the run committed",
          committed.get("note") == delta.get("note")
          and committed.get("note")
          == hashlib.sha256(json.dumps(obs, sort_keys=True, separators=(",", ":")).encode()).hexdigest(),
          json.dumps({"committed": committed.get("note")}))

    check("a sign-off bound to another dispatch does not pass the gate",
          last_terminal(tl, "forged")["kind"] == "terminal/error/agent/signoff_unobtained",
          json.dumps(last_terminal(tl, "forged")["kind"]))
    check("a sign-off taken over a value the run never writes does not pass the gate",
          last_terminal(tl, "tampered")["kind"] == "terminal/error/agent/signoff_unobtained",
          json.dumps(last_terminal(tl, "tampered")["kind"]))

    check("a declared error class routed by policy settles the run without failing it",
          last_terminal(tl, "passed")["kind"] == "terminal/error/agent/context_exceeded"
          and [n for n in nodes_of(iid) if n["node_type"] == "passed"][0]["run_summary"]["fresh_count"] == 1,
          json.dumps([last_terminal(tl, "passed")["kind"],
                      [n["run_summary"] for n in nodes_of(iid) if n["node_type"] == "passed"]]))
    check("a declared error class with no policy fails the run under its own name",
          last_terminal(tl, "refused")["kind"] == "terminal/error/agent/refused",
          json.dumps(last_terminal(tl, "refused")["kind"]))
    check("a wildcard subscription over the agent error-class family observes that failure",
          last_terminal(tl, "watcher")["kind"] == "terminal/success",
          json.dumps([r["kind"] for r in terminals(tl, "watcher")]))
    check("an error class outside the declared vocabulary is refused at the callback surface",
          last_terminal(tl, "undeclared")["kind"].startswith("terminal/error/agent/")
          and "probe/not-declared" not in last_terminal(tl, "undeclared")["kind"],
          json.dumps(last_terminal(tl, "undeclared")["kind"]))
    finish()


try:
    main()
finally:
    teardown()
    purge_build()
