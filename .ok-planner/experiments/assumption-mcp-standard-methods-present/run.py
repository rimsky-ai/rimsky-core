SLUG = "assumption-mcp-standard-methods-present"

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

BASE_METHODS = ["ping", "notifications/initialized", "notifications/cancelled",
                "prompts/list", "prompts/get", "resources/subscribe", "resources/unsubscribe",
                "resources/templates/list", "completion/complete", "logging/setLevel",
                "roots/list", "sampling/createMessage"]

IMPLEMENTED = ["initialize", "tools/list", "tools/call", "resources/list", "resources/read"]


def main():
    boot()
    admin = bootstrap_admin()
    session = mcp_session(admin)

    print("== the five that are implemented ==")
    for method, params in [("tools/list", None), ("resources/list", None),
                           ("tools/call", {"name": "auth_status", "arguments": {}}),
                           ("resources/read", {"uri": "rimsky://instances/x/breakpoint-hits"})]:
        body = rpc(session, admin, method, params)
        check("%-16s is dispatched (no -32601)" % method,
              body.get("error", {}).get("code") != -32601 if body.get("error") else True,
              json.dumps(body.get("error") or "ok")[:80])

    print("")
    print("== PRIOR CONTRADICTED: every other base method answers -32601 method not found ==")
    for method in BASE_METHODS:
        body = rpc(session, admin, method)
        err = body.get("error") or {}
        check("%-28s -> -32601 method not found" % method,
              err.get("code") == -32601 and err.get("message") == "method not found: " + method,
              json.dumps(err)[:90])

    print("")
    print("== notifications/initialized is the one a conforming client sends unprompted ==")
    body = rpc(session, admin, "notifications/initialized")
    check("the post-initialize lifecycle notification is rejected, not ignored",
          (body.get("error") or {}).get("code") == -32601, json.dumps(body)[:100])
    body = rpc(session, admin, "tools/list")
    check("the session still works afterwards, so the rejection is not fatal",
          "result" in body, json.dumps(body)[:60])

    print("")
    print("== the server does declare what it has, which is the mitigating half ==")
    _, init = call("POST", "/v1/mcp",
                   {"jsonrpc": "2.0", "id": 1, "method": "initialize",
                    "params": {"protocolVersion": "2025-06-18", "capabilities": {},
                               "clientInfo": {"name": "probe", "version": "1"}}}, admin)
    caps = init["result"]["capabilities"]
    check("initialize advertises only tools and resources",
          sorted(caps.keys()) == ["resources", "tools"], json.dumps(caps))
    check("it advertises resources.subscribe: false, matching the missing method",
          caps["resources"] == {"subscribe": False, "listChanged": False}, json.dumps(caps["resources"]))
    check("it advertises no prompts capability, matching the missing prompts methods",
          "prompts" not in caps and "logging" not in caps, json.dumps(sorted(caps.keys())))
    check("ping and notifications/initialized are base protocol, not capability-gated",
          (rpc(session, admin, "ping").get("error") or {}).get("code") == -32601, "both still absent")

    print("")
    print("== an unknown method and a missing base method are indistinguishable ==")
    body = rpc(session, admin, "totally/made/up")
    check("a made-up method answers the same -32601 shape",
          (body.get("error") or {}).get("code") == -32601, json.dumps(body.get("error"))[:80])

    finish()


main()
