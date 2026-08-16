SLUG = "assumption-permission-scope-on-every-action"

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

ALL_ACTIONS = """instance:read instance:create instance:terminate instance:pause instance:resume
instance:kill instance:debug-override instance:list-frames instance:read-frame breakpoint:read
breakpoint:create breakpoint:resume breakpoint:delete template:read template:validate
template:register template:deploy template:undeploy template:deregister tag:read tag:create tag:set
tag:delete node:read node:reset run:read message:send message:read event:read audit:read
lineage:read lineage:prune parked-node:read waitset:read claim-holders:read asset:read asset:delete
diagnostics:read auth:read auth:create auth:revoke auth:rotate observability:read compose:origin
mcp:read service:enroll""".split()

SCOPE_BEARING = ["instance:create", "tag:delete", "tag:set", "template:deploy",
                 "template:deregister", "template:register", "template:undeploy"]


def main():
    boot()
    admin = bootstrap_admin()
    serial = [0]

    def try_scope(action, scope):
        serial[0] += 1
        return mint(admin, "scope-%d" % serial[0], [{"action": action, "scope": scope}])

    _, out = call("POST", "/v1/templates", {"spec": {
        "name": "scope-probe", "version": "1",
        "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}}, admin)
    template_id = out["template_id"]
    call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    for tag in ["mine:v1", "theirs:v1"]:
        call("POST", "/v1/tags", {"tag": tag, "template": template_id}, admin)
    _, out = call("POST", "/v1/instances", {"template": template_id, "instance_key": "scope-a",
                                            "target_agent": "audit-agent"}, admin)
    instance_id = out["instance_id"]

    print("== scope works, on seven actions out of forty-six ==")
    accepts = [a for a in ALL_ACTIONS if try_scope(a, {"template_tag": "mine:v1"})[0] == 201]
    check("exactly the template-and-tag actions accept a scope",
          sorted(accepts) == sorted(SCOPE_BEARING), str(sorted(accepts)))
    check("that is 7 of the %d grantable actions" % len(ALL_ACTIONS),
          len(accepts) == 7, "%d of %d" % (len(accepts), len(ALL_ACTIONS)))

    print("")
    print("== PRIOR CONTRADICTED: the other thirty-nine refuse a scope at key creation ==")
    for action in ["instance:read", "instance:terminate", "instance:kill", "instance:pause",
                   "node:read", "node:reset", "message:send", "message:read", "asset:read",
                   "asset:delete", "breakpoint:create", "event:read", "audit:read",
                   "template:read", "tag:create", "auth:read", "observability:read"]:
        status, body = try_scope(action, {"template_tag": "mine:v1"})
        check("%-22s does not support scope" % action,
              status == 400 and body == {"error": 'grant entry 0: action "%s" does not support scope' % action},
              "%s %s" % (status, json.dumps(body)[:70]))

    print("")
    print("== PRIOR CONTRADICTED: there is one scope dimension, and it is the template tag ==")
    for dimension in ["template_id", "instance_id", "instance_key", "tag", "node_type", "anything"]:
        status, body = try_scope("tag:delete", {dimension: "x"})
        check("scope dimension %-14s is rejected on tag:delete" % dimension,
              status == 400 and "unknown scope dimension" in json.dumps(body),
              json.dumps(body)[:95])
    status, _ = try_scope("tag:delete", {"template_tag": "mine:v1"})
    check("template_tag is the one dimension that is accepted", status == 201, str(status))
    status, body = try_scope("instance:create", {"instance_id": instance_id})
    check("even instance:create, which accepts a scope, will not take instance_id",
          status == 400 and "unknown scope dimension" in json.dumps(body), json.dumps(body)[:95])

    print("")
    print("== so an operator cannot pin a key to one instance at all ==")
    for action in ["instance:read", "instance:terminate", "instance:pause", "instance:kill",
                   "message:send", "node:reset"]:
        for dimension in ["instance_id", "instance_key"]:
            status, _ = try_scope(action, {dimension: instance_id})
            check("%-20s scoped by %-13s is refused" % (action, dimension), status == 400, str(status))

    print("")
    print("== where scope does work, it works exactly as least-privilege ==")
    serial[0] += 1
    status, sc = mint(admin, "scoped-tag", [{"action": "*:read"},
                                            {"action": "tag:delete",
                                             "scope": {"template_tag": "mine:v1"}}])
    scoped = sc["plaintext"]
    check("the scoped key mints", status == 201, str(status))
    status, body = call("DELETE", "/v1/tags/theirs:v1", None, scoped)
    check("DELETE the out-of-scope tag is refused 403",
          status == 403 and body == {"error": "permission denied"}, "%s %s" % (status, json.dumps(body)))
    status, body = call("DELETE", "/v1/tags/mine:v1", None, scoped)
    check("DELETE the in-scope tag succeeds", status == 200, "%s %s" % (status, json.dumps(body)))

    print("")
    print("== and it reaches template ids through their tags, not around them ==")
    call("POST", "/v1/tags", {"tag": "mine:v1", "template": template_id}, admin)
    _, out = call("POST", "/v1/templates", {"spec": {
        "name": "scope-other", "version": "1",
        "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}}, admin)
    other_id = out["template_id"]
    status, dep = mint(admin, "scoped-deploy", [{"action": "*:read"},
                                                {"action": "template:deploy",
                                                 "scope": {"template_tag": "mine:v1"}}])
    deployer = dep["plaintext"]
    status, _ = call("POST", "/v1/templates/%s/deploy" % template_id, {}, deployer)
    check("deploying the template that carries the scoped tag succeeds", status == 200, str(status))
    status, body = call("POST", "/v1/templates/%s/deploy" % other_id, {}, deployer)
    check("deploying an untagged template is refused 403", status == 403,
          "%s %s" % (status, json.dumps(body)))

    finish()


main()
