SLUG = "assumption-mcp-resource-uris-are-a-family"

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

OUTSIDE = ["rimsky://instances", "rimsky://instances/{id}", "rimsky://instances/{id}/nodes",
           "rimsky://instances/{id}/frames", "rimsky://instances/{id}/events",
           "rimsky://instances/{id}/messages", "rimsky://nodes/{id}", "rimsky://events",
           "rimsky://templates", "rimsky://templates/{tpl}", "rimsky://tags",
           "rimsky://runs/{id}", "rimsky://audit", "rimsky://observability/executors"]


def main():
    boot()
    admin = bootstrap_admin()
    _, out = call("POST", "/v1/templates", {"spec": {
        "name": "uri-probe", "version": "1",
        "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}}, admin)
    template_id = out["template_id"]
    call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    _, out = call("POST", "/v1/instances", {"template": template_id, "instance_key": "uri-a",
                                            "target_agent": "audit-agent"}, admin)
    instance_id = out["instance_id"]
    session = mcp_session(admin)

    print("== the listing is derived from instances, not from breakpoints ==")
    listing = rpc(session, admin, "resources/list")["result"]["resources"]
    check("with one instance and no breakpoints, resources/list already offers one URI",
          [r["uri"] for r in listing] == ["rimsky://instances/%s/breakpoint-hits" % instance_id],
          str([r["uri"] for r in listing]))

    _, out = call("POST", "/v1/instances/%s/breakpoints" % instance_id,
                  {"node_type": "w", "checkpoint": "before_dispatch"}, admin)
    breakpoint_id = out["breakpoint_id"]

    print("")
    print("== adding a breakpoint adds nothing to the listing ==")
    listing = rpc(session, admin, "resources/list")["result"]["resources"]
    uris = sorted(r["uri"] for r in listing)
    check("resources/list still offers exactly the one instance breakpoint-hits URI",
          uris == ["rimsky://instances/%s/breakpoint-hits" % instance_id], str(uris))
    check("the per-breakpoint form resolves but is never listed",
          "rimsky://breakpoints/%s/hits" % breakpoint_id not in uris
          and "result" in rpc(session, admin, "resources/read",
                              {"uri": "rimsky://breakpoints/%s/hits" % breakpoint_id}),
          "listed=%s" % ("rimsky://breakpoints/%s/hits" % breakpoint_id in uris))

    print("")
    print("== both breakpoint-hit forms read, and they are the whole scheme ==")
    for uri in ["rimsky://instances/%s/breakpoint-hits" % instance_id,
                "rimsky://breakpoints/%s/hits" % breakpoint_id]:
        body = rpc(session, admin, "resources/read", {"uri": uri})
        content = body["result"]["contents"][0]
        check("%-58s reads" % uri.replace(instance_id, "{iid}").replace(breakpoint_id, "{bid}"),
              content["mimeType"] == "application/x-rimsky-breakpoint-hits+json",
              content["mimeType"])

    print("")
    print("== PRIOR CONTRADICTED: nothing else in the scheme resolves ==")
    expected = ("uri must be rimsky://instances/{uuid}/breakpoint-hits "
                "or rimsky://breakpoints/{uuid}/hits")
    shapes = set()
    for template in OUTSIDE:
        uri = template.replace("{id}", instance_id).replace("{tpl}", template_id)
        body = rpc(session, admin, "resources/read", {"uri": uri})
        err = body.get("error") or {}
        message = err.get("message", "")
        shapes.add("enumerated" if expected in message else
                   "unknown-shape" if message.startswith("unknown uri shape") else "other")
        check("%-46s is rejected with -32602" % template, err.get("code") == -32602,
              json.dumps(err)[:80])
    check("every rejection is one of two messages, neither of which offers another form",
          shapes == {"enumerated", "unknown-shape"}, str(sorted(shapes)))

    print("")
    print("== the rejection names the whole scheme, which is two forms ==")
    body = rpc(session, admin, "resources/read", {"uri": "rimsky://anything"})
    check("the error enumerates exactly two accepted URI shapes",
          (body.get("error") or {}).get("message", "").startswith(expected),
          json.dumps(body.get("error"))[:130])
    check("resources/templates/list, which would advertise a family, is not implemented",
          (rpc(session, admin, "resources/templates/list").get("error") or {}).get("code") == -32601,
          "method not found")

    print("")
    print("== the REST surface reads dozens of resources the scheme does not address ==")
    reachable = 0
    for path in ["/v1/instances/" + instance_id, "/v1/instances/%s/nodes" % instance_id,
                 "/v1/instances/%s/frames" % instance_id, "/v1/instances/%s/messages" % instance_id,
                 "/v1/events?limit=1", "/v1/templates", "/v1/tags", "/v1/audit?limit=1"]:
        if call("GET", path, None, admin)[0] == 200:
            reachable += 1
    check("8 readable REST resources answer 200 and none has a rimsky:// address",
          reachable == 8, "%d of 8" % reachable)

    finish()


main()
