SLUG = "assumption-permission-actions-cover-full-crud"

import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG") or sys.exit("export RIMSKY_IMAGE_TAG=src-<tree hash> first")
IMAGE = "rimsky-all-in-one:" + TAG
NAME = "exp-" + SLUG
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
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
    res = docker("run", "-d", "--name", NAME, "-p", "127.0.0.1:%d:8080" % port, IMAGE)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
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


def cli_env():
    env = dict(os.environ, HOME=tempfile.mkdtemp(prefix="rimsky-exp-"))
    env.pop("RIMSKY_API_KEY", None)
    env.pop("RIMSKY_CONTROL_API_URL", None)
    return env


def plaintext_of(out):
    for line in out.splitlines():
        if "RIMSKY_API_KEY" in line and "for subsequent" in line:
            return line.split("RIMSKY_API_KEY=")[1].split(" ")[0].strip('"')
    die("could not read a key plaintext out of:\n" + out)


def bootstrap_admin():
    return plaintext_of(subprocess.run([CLI, "auth", "init", "--endpoint", STATE["base"]],
                                       capture_output=True, text=True, env=cli_env()).stdout)


def finish():
    docker("rm", "-f", NAME)
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


def mcp_session(token):
    status, _, headers = raw("POST", "/v1/mcp",
                             {"jsonrpc": "2.0", "id": 1, "method": "initialize",
                              "params": {"protocolVersion": "2025-06-18", "capabilities": {},
                                         "clientInfo": {"name": "probe", "version": "1"}}}, token)
    if status != 200 or not headers.get("Mcp-Session-Id"):
        die("MCP initialize failed: %s" % status)
    return headers["Mcp-Session-Id"]


def rpc(session, token, method, params=None):
    payload = {"jsonrpc": "2.0", "id": 7, "method": method}
    if params is not None:
        payload["params"] = params
    return call("POST", "/v1/mcp", payload, token, {"Mcp-Session-Id": session})[1]


def tool_call(session, token, name, arguments):
    return rpc(session, token, "tools/call", {"name": name, "arguments": arguments})


def mint(admin, name, permissions):
    return call("POST", "/v1/auth/keys", {"name": name, "permissions": permissions}, admin)

NAMED_BY_THE_PRIOR = ["instance:update", "asset:create", "message:delete"]

PLAUSIBLE_SIBLINGS = ["instance:delete", "asset:update", "message:update", "node:update",
                      "node:delete", "tag:update", "template:update", "template:delete",
                      "run:create", "run:delete", "event:write", "event:delete", "audit:write",
                      "breakpoint:update", "lineage:write", "auth:update"]

REGISTRY = """instance:read instance:create instance:terminate instance:pause instance:resume
instance:kill instance:debug-override instance:list-frames instance:read-frame breakpoint:read
breakpoint:create breakpoint:resume breakpoint:delete template:read template:validate
template:register template:deploy template:undeploy template:deregister tag:read tag:create tag:set
tag:delete node:read node:reset run:read message:send message:read event:read audit:read
lineage:read lineage:prune parked-node:read waitset:read claim-holders:read asset:read asset:delete
diagnostics:read auth:read auth:create auth:revoke auth:rotate observability:read compose:origin
mcp:read service:enroll""".split()

UNGRANTABLE = ["health:probe", "peer-auth:ca-root", "auth:whoami"]


def main():
    boot()
    admin = bootstrap_admin()
    serial = [0]

    def try_action(action):
        serial[0] += 1
        return mint(admin, "probe-%d" % serial[0], [{"action": action}])

    print("== PRIOR CONTRADICTED: the three verbs the prior names do not exist ==")
    for action in NAMED_BY_THE_PRIOR:
        status, body = try_action(action)
        check("%-18s is rejected: unknown action" % action,
              status == 400 and body == {"error": "unknown action: " + action},
              "%s %s" % (status, json.dumps(body)))

    print("")
    print("== nor do sixteen more the same reasoning would reach for ==")
    for action in PLAUSIBLE_SIBLINGS:
        status, body = try_action(action)
        check("%-20s is rejected: unknown action" % action,
              status == 400 and body.get("error") == "unknown action: " + action,
              "%s %s" % (status, json.dumps(body)[:60]))

    print("")
    print("== the registry is closed, and it is exactly the actions the routes gate ==")
    accepted, refused = [], []
    for action in REGISTRY:
        status, _ = try_action(action)
        (accepted if status == 201 else refused).append(action)
    check("all %d grantable registry actions are accepted" % len(REGISTRY),
          not refused, str(refused))
    for action in UNGRANTABLE:
        status, body = try_action(action)
        check("%-18s is a real action and still refuses a grant, with its reason" % action,
              status == 400 and "unknown action" not in json.dumps(body),
              json.dumps(body)[:110])

    print("")
    print("== the missing verbs are missing operations, not missing permissions ==")
    _, out = call("POST", "/v1/templates", {"spec": {
        "name": "crud-probe", "version": "1",
        "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}}, admin)
    template_id = out["template_id"]
    call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    _, out = call("POST", "/v1/instances", {"template": template_id, "instance_key": "crud-a",
                                            "target_agent": "audit-agent"}, admin)
    instance_id = out["instance_id"]
    for method, path, label in [("PUT", "/v1/instances/" + instance_id, "instance:update"),
                                ("PATCH", "/v1/instances/" + instance_id, "instance:update"),
                                ("POST", "/v1/instances/%s/assets" % instance_id, "asset:create"),
                                ("DELETE", "/v1/messages/11111111-1111-1111-1111-111111111111",
                                 "message:delete")]:
        status, _ = call(method, path, {} if method in ("PUT", "PATCH", "POST") else None, admin)
        check("%-6s %-44s answers 405 — the operation the verb would gate has no route"
              % (method, path.replace(instance_id, "{id}")), status == 405, "%s (%s)" % (status, label))

    print("")
    print("== the verb the prior would call update exists under another name ==")
    call("POST", "/v1/tags", {"tag": "crud:v1", "template": template_id}, admin)
    status, _ = call("PUT", "/v1/tags/crud:v1", {"template": template_id}, admin)
    check("PUT /v1/tags/{tag} updates a tag and is gated by tag:set, not tag:update",
          status == 200 and try_action("tag:set")[0] == 201 and try_action("tag:update")[0] == 400,
          "PUT answered %s" % status)

    print("")
    print("== the wildcard grammar is closed the same way ==")
    for action, ok in [("*", True), ("instance:*", True), ("*:read", True), ("*:delete", True),
                       ("*:*", False), ("inst*:read", False), ("instance:re*", False),
                       ("instanceread", False), ("instance:read:extra", False)]:
        status, body = try_action(action)
        check("%-20s %s" % (action, "accepted" if ok else "rejected"),
              (status == 201) == ok, "%s %s" % (status, json.dumps(body)[:70]))

    finish()


main()
