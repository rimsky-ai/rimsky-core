SLUG = "assumption-mcp-catalog-hides-denied-tools"

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

def main():
    boot()
    admin = bootstrap_admin()
    _, out = call("POST", "/v1/templates", {"spec": {
        "name": "catalog-probe", "version": "1",
        "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}}, admin)
    template_id = out["template_id"]
    call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    _, out = call("POST", "/v1/instances", {"template": template_id, "instance_key": "catalog-a",
                                            "target_agent": "audit-agent"}, admin)
    instance_id = out["instance_id"]
    for tag in ["mine:v1", "theirs:v1"]:
        call("POST", "/v1/tags", {"tag": tag, "template": template_id}, admin)

    admin_session = mcp_session(admin)
    admin_tools = rpc(admin_session, admin, "tools/list")["result"]["tools"]

    print("== the catalog really is filtered by the grant ==")
    status, ro = mint(admin, "reader", [{"action": "*:read"}])
    reader = ro["key"] if "key" in ro else ro.get("plaintext")
    check("minting a read-only key returns its plaintext", status == 201 and bool(reader),
          str(sorted(ro.keys())))
    reader_session = mcp_session(reader)
    reader_tools = rpc(reader_session, reader, "tools/list")["result"]["tools"]
    reader_names = sorted(t["name"] for t in reader_tools)
    check("admin sees 57 tools and the read-only key sees 30",
          len(admin_tools) == 57 and len(reader_tools) == 30,
          "%d vs %d" % (len(admin_tools), len(reader_tools)))
    check("no mutating tool is listed for the read-only key",
          not [n for n in reader_names
               if any(n.endswith(v) or ("_" + v + "_") in n
                      for v in ("create", "delete", "register", "deploy", "undeploy", "prune",
                                "reset", "revoke", "rotate", "send", "kill", "terminate", "pause",
                                "resume", "set", "deregister", "enroll", "override"))],
          str([n for n in reader_names if "create" in n or "delete" in n]))

    print("")
    print("== and every tool it does list is callable without a permission error ==")
    args = {"id": instance_id, "idOrKey": instance_id, "alias": "w.x", "tag": "mine:v1",
            "template": template_id, "nameOrID": "reader", "before": "2000-01-01T00:00:00Z",
            "path_suffix": "executors", "name": "reader", "instance_id": instance_id,
            "executor_name": "http-node", "source_type": "x", "source_id": "y",
            "node_type": "w", "checkpoint": "before_dispatch", "action": "invalidate",
            "type": "t", "body": {}, "role": "read-only", "reason": "probe", "label": "probe"}
    unknown = "11111111-1111-1111-1111-111111111111"
    denied = []
    for tool in reader_tools:
        props = tool["inputSchema"].get("properties") or {}
        body = tool_call(reader_session, reader, tool["name"],
                         {k: args.get(k, unknown) for k in props})
        if "permission denied" in json.dumps(body):
            denied.append(tool["name"])
    check("calling all %d listed tools with the read-only key: 0 permission denials" % len(reader_tools),
          not denied, str(denied))

    print("")
    print("== PRIOR CONTRADICTED: a scoped grant lists a tool the key may not invoke ==")
    status, sc = mint(admin, "scoped", [{"action": "*:read"},
                                        {"action": "tag:delete",
                                         "scope": {"template_tag": "mine:v1"}}])
    scoped = sc["key"] if "key" in sc else sc.get("plaintext")
    check("a scope-bearing grant is accepted at key creation", status == 201, str(status))
    scoped_session = mcp_session(scoped)
    scoped_tools = rpc(scoped_session, scoped, "tools/list")["result"]["tools"]
    names = sorted(t["name"] for t in scoped_tools)
    check("tag_delete is listed for the scoped key", "tag_delete" in names,
          "%d tools, tag_delete listed=%s" % (len(scoped_tools), "tag_delete" in names))
    body = tool_call(scoped_session, scoped, "tag_delete", {"tag": "theirs:v1"})
    check("calling the listed tag_delete on an out-of-scope tag is a permission denial",
          "permission denied" in json.dumps(body) and body["result"]["isError"] is True,
          json.dumps(body["result"]["content"][0]["text"])[:90])
    body = tool_call(scoped_session, scoped, "tag_delete", {"tag": "mine:v1"})
    check("the same listed tool on the in-scope tag succeeds",
          body["result"]["isError"] is False, json.dumps(body["result"]["content"][0]["text"])[:70])

    print("")
    print("== the catalog cannot filter on scope, because the target is per call ==")
    check("one listed tool answers success and permission-denied depending on its arguments",
          True, "tag_delete on mine:v1 succeeds, on theirs:v1 is 403")
    status, body = call("DELETE", "/v1/tags/theirs:v1", None, scoped)
    check("the same split shows over HTTP, so it is the grant and not the MCP skin",
          status == 403 and body == {"error": "permission denied"}, "%s %s" % (status, json.dumps(body)))

    finish()


main()
