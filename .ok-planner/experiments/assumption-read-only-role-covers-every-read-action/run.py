SLUG = "assumption-read-only-role-covers-every-read-action"

import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG") or sys.exit("export RIMSKY_IMAGE_TAG=src-<tree hash> first")
IMAGE = "rimsky-all-in-one:" + TAG
NAME = "exp-" + SLUG
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
HOMEDIR = tempfile.mkdtemp(prefix="rimsky-exp-")
STATE = {"base": None, "checks": []}


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def die(msg):
    print("HARNESS ERROR: " + msg)
    docker("rm", "-f", NAME)
    sys.exit(2)


def raw(method, path, body=None, token=None, headers=None):
    hdrs = {"Content-Type": "application/json", "Idempotency-Key": uuid.uuid4().hex}
    if token:
        hdrs["Authorization"] = "Bearer " + token
    if headers:
        hdrs.update(headers)
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(STATE["base"] + path, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, resp.read().decode(), dict(resp.headers)
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode(), dict(exc.headers)


def call(method, path, body=None, token=None, headers=None):
    status, text, _ = raw(method, path, body, token, headers)
    try:
        return status, json.loads(text) if text else None
    except ValueError:
        return status, text


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def boot():
    if docker("image", "inspect", IMAGE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % IMAGE)
    docker("rm", "-f", NAME)
    port = free_port()
    if docker("run", "-d", "--name", NAME, "-p", "127.0.0.1:%d:8080" % port, IMAGE).returncode != 0:
        die("docker run failed")
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                return
        except Exception:
            pass
        if docker("inspect", "-f", "{{.State.Running}}", NAME).stdout.strip() != "true":
            die("container exited during boot:\n" + docker("logs", NAME).stdout + docker("logs", NAME).stderr)
        time.sleep(0.3)


def cli(*args):
    env = dict(os.environ, HOME=HOMEDIR)
    env.pop("RIMSKY_API_KEY", None)
    env.pop("RIMSKY_CONTROL_API_URL", None)
    return subprocess.run([CLI, *args, "--endpoint", STATE["base"]],
                          capture_output=True, text=True, env=env)


def plaintext_of(out):
    for line in out.splitlines():
        if "RIMSKY_API_KEY" in line and "for subsequent" in line:
            return line.split("RIMSKY_API_KEY=")[1].split(" ")[0].strip('"')
    die("could not read a key plaintext out of:\n" + out)


def finish():
    docker("rm", "-f", NAME)
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


def main():
    boot()
    admin = plaintext_of(cli("auth", "init").stdout)

    _, out = call("POST", "/v1/templates", {"spec": {
        "name": "ro-probe", "version": "1",
        "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}}, admin)
    template_id = out["template_id"]
    call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    call("POST", "/v1/tags", {"tag": "ro:v1", "template": template_id}, admin)
    _, out = call("POST", "/v1/instances", {"template": template_id, "instance_key": "ro-1",
                                            "target_agent": "audit-agent"}, admin)
    instance = out["instance_id"]
    absent = str(uuid.uuid4())

    ro = plaintext_of(cli("auth", "create-key", "--key", admin, "--name", "dashboard",
                          "--role", "read-only").stdout)
    _, key = call("GET", "/v1/auth/keys/dashboard", None, admin)
    check("the read-only role expands to exactly one grant entry",
          key["permissions"] == [{"action": "*:read"}], json.dumps(key["permissions"]))

    print("")
    print("== every action ending in :read, driven at a route it gates ==")
    READ_ROUTES = [
        ("instance:read", "GET", "/v1/instances", None),
        ("breakpoint:read", "GET", "/v1/instances/%s/breakpoints" % instance, None),
        ("template:read", "GET", "/v1/templates", None),
        ("tag:read", "GET", "/v1/tags", None),
        ("node:read", "GET", "/v1/instances/%s/nodes" % instance, None),
        ("run:read", "GET", "/v1/runs/%s" % absent, None),
        ("message:read", "GET", "/v1/instances/%s/messages" % instance, None),
        ("event:read", "GET", "/v1/events", None),
        ("audit:read", "GET", "/v1/audit", None),
        ("lineage:read", "GET", "/v1/lineage/runs/%s" % absent, None),
        ("parked-node:read", "GET", "/v1/admin/diagnostics/parked-nodes", None),
        ("waitset:read", "GET", "/v1/admin/diagnostics/wait-sets", None),
        ("claim-holders:read", "GET", "/v1/claim-handles/%s/holders" % absent, None),
        ("asset:read", "GET", "/v1/instances/%s/assets" % instance, None),
        ("diagnostics:read", "GET", "/v1/admin/diagnostics/held-frames", None),
        ("auth:read", "GET", "/v1/auth/keys", None),
        ("observability:read", "GET", "/v1/observability/system/health", None),
    ]
    granted = []
    for action, method, path, body in READ_ROUTES:
        status, _ = call(method, path, body, ro)
        ok = status not in (401, 403)
        granted.append(action if ok else None)
        check("%-20s %-6s %-44s not refused" % (action, method, path), ok, str(status))

    status, _, headers = raw("POST", "/v1/mcp", {
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {"protocolVersion": "2025-06-18", "capabilities": {},
                   "clientInfo": {"name": "probe", "version": "1"}}}, ro)
    check("%-20s %-6s %-44s not refused" % ("mcp:read", "POST", "/v1/mcp"),
          status == 200 and bool(headers.get("Mcp-Session-Id")), str(status))
    granted.append("mcp:read" if status == 200 else None)

    check("all 18 actions ending in :read are granted by the role",
          all(granted) and len(granted) == 18,
          "%d of 18" % len([g for g in granted if g]))

    print("")
    print("== the same key is refused every write ==")
    for label, method, path, body in [
        ("template:register", "POST", "/v1/templates", {"spec": {"name": "x", "version": "1", "nodes": [
            {"type": "w", "kind": "attribute_passthrough"}]}}),
        ("instance:create", "POST", "/v1/instances", {"template": template_id, "instance_key": "x"}),
        ("instance:kill", "POST", "/v1/instances/%s/terminate" % instance, {}),
        ("node:reset", "POST", "/v1/nodes/%s/reset" % absent, {}),
        ("auth:create", "POST", "/v1/auth/keys", {"name": "z", "permissions": [{"action": "*"}]}),
        ("tag:create", "POST", "/v1/tags", {"tag": "z:v1", "template": template_id}),
    ]:
        status, _ = call(method, path, body, ro)
        check("%-20s refused 403" % label, status == 403, str(status))

    print("")
    print("== the read-shaped actions that do not end in :read are refused too ==")
    for label, path in [("instance:list-frames", "/v1/instances/%s/frames" % instance),
                        ("instance:read-frame", "/v1/instances/%s/frames/%s" % (instance, absent))]:
        status, body = call("GET", path, None, ro)
        check("%-20s refused 403 — the wildcard matches the verb, not its prefix" % label,
              status == 403, "%s %s" % (status, json.dumps(body)))
    check("auth:whoami is ungated and answers for the read-only key",
          call("GET", "/v1/auth/whoami", None, ro)[0] == 200)
    check("health:probe is ungated and answers for the read-only key",
          call("GET", "/v1/health", None, ro)[0] == 200)

    finish()


main()
