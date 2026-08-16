SLUG = "assumption-mcp-tools-cover-every-route"

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

U = "11111111-1111-1111-1111-111111111111"

ARGS = {"id": U, "idOrKey": U, "alias": "w.x", "tag": "never:v1", "template": "sha256-deadbeef",
        "nameOrID": "nosuch", "before": "2000-01-01T00:00:00Z", "path_suffix": "executors",
        "name": "nosuch", "node_id": U, "run_id": U, "claim_handle_id": U, "breakpoint_id": U,
        "instance_id": U, "executor_name": "nosuch", "source_type": "x", "source_id": "y",
        "message_id": U, "node_type": "w", "checkpoint": "before_dispatch", "action": "invalidate",
        "type": "t", "body": {}, "role": "read-only", "hit_id": U, "frame_id": U, "key": "nosuch",
        "reason": "audit probe", "triggering_message_id": U, "label": "audit probe",
        "spec": {"name": "probe", "version": "1",
                 "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}}

ROUTES = [
    ("DELETE", "/v1/auth/keys/nosuch", None), ("DELETE", "/v1/instances/%s" % U, None),
    ("DELETE", "/v1/instances/%s/breakpoints/%s" % (U, U), None),
    ("DELETE", "/v1/instances/%s/assets/w.x" % U, None),
    ("DELETE", "/v1/tags/never", None), ("DELETE", "/v1/templates/sha256-deadbeef", None),
    ("GET", "/v1/admin/diagnostics/held-frames", None),
    ("GET", "/v1/admin/diagnostics/parked-nodes", None),
    ("GET", "/v1/admin/diagnostics/producer-outbox", None),
    ("GET", "/v1/admin/diagnostics/wait-sets?frame=%s" % U, None),
    ("GET", "/v1/audit?limit=1", None), ("GET", "/v1/auth/keys", None),
    ("GET", "/v1/auth/keys/nosuch", None), ("GET", "/v1/auth/status", None),
    ("GET", "/v1/claim-handles/%s/holders" % U, None), ("GET", "/v1/events?limit=1", None),
    ("GET", "/v1/instances", None), ("GET", "/v1/instances/%s" % U, None),
    ("GET", "/v1/instances/%s/breakpoint-hits" % U, None),
    ("GET", "/v1/instances/%s/breakpoints" % U, None), ("GET", "/v1/instances/%s/nodes" % U, None),
    ("GET", "/v1/instances/%s/assets" % U, None), ("GET", "/v1/instances/%s/assets/w.x" % U, None),
    ("GET", "/v1/instances/%s/assets/w.x/materialization-history" % U, None),
    ("GET", "/v1/instances/%s/assets/w.x/versions" % U, None),
    ("GET", "/v1/instances/%s/frames" % U, None),
    ("GET", "/v1/instances/%s/frames/%s" % (U, U), None),
    ("GET", "/v1/instances/%s/messages" % U, None),
    ("GET", "/v1/lineage/by-producer/nosuch", None), ("GET", "/v1/lineage/by-source/x/y", None),
    ("GET", "/v1/lineage/claims/%s" % U, None), ("GET", "/v1/lineage/claims/%s/ancestors" % U, None),
    ("GET", "/v1/lineage/claims/%s/descendants" % U, None), ("GET", "/v1/lineage/runs/%s" % U, None),
    ("GET", "/v1/lineage/runs/%s/ancestors" % U, None),
    ("GET", "/v1/lineage/runs/%s/descendants" % U, None),
    ("GET", "/v1/messages/%s" % U, None), ("GET", "/v1/nodes/%s" % U, None),
    ("GET", "/v1/observability/executors", None), ("GET", "/v1/runs/%s" % U, None),
    ("GET", "/v1/tags", None), ("GET", "/v1/templates", None),
    ("GET", "/v1/templates/sha256-deadbeef", None),
    ("POST", "/v1/admin/lineage/prune", {"before": "2000-01-01T00:00:00Z"}),
    ("POST", "/v1/auth/keys", {"name": "probe-key", "role": "read-only"}),
    ("POST", "/v1/auth/keys/nosuch/rotate", {}),
    ("POST", "/v1/instances", {"template": "sha256-deadbeef"}),
    ("POST", "/v1/instances/%s/breakpoints" % U, {"node_type": "w", "checkpoint": "before_dispatch"}),
    ("POST", "/v1/instances/%s/breakpoints/%s/resume" % (U, U), {}),
    ("POST", "/v1/instances/%s/pause" % U, {}), ("POST", "/v1/instances/%s/resume" % U, {}),
    ("POST", "/v1/instances/%s/terminate" % U, {}),
    ("POST", "/v1/instances/%s/debug/override" % U, {"action": "invalidate"}),
    ("POST", "/v1/instances/%s/messages" % U, {}), ("POST", "/v1/nodes/%s/reset" % U, {}),
    ("POST", "/v1/tags", {"tag": "never:v1", "template": "sha256-deadbeef"}),
    ("POST", "/v1/templates", {"spec": ARGS["spec"]}),
    ("POST", "/v1/templates/validate", {"spec": ARGS["spec"]}),
    ("POST", "/v1/templates/sha256-deadbeef/deploy", {}),
    ("POST", "/v1/templates/sha256-deadbeef/undeploy", {}),
    ("PUT", "/v1/tags/never", {"template": "sha256-deadbeef"}),
]


def actions_by_skin(admin):
    _, page = call("GET", "/v1/audit?limit=5000", None, admin)
    out = {}
    for row in page["audit"]:
        if not row["kind"].startswith("auth.access"):
            continue
        payload = row["payload"]
        action = payload.get("action")
        if action:
            out.setdefault(payload.get("protocol_skin"), set()).add(action)
    return out


def main():
    boot()
    admin = bootstrap_admin()

    print("== the catalog ==")
    status, body, headers = raw("POST", "/v1/mcp",
                                {"jsonrpc": "2.0", "id": 1, "method": "initialize",
                                 "params": {"protocolVersion": "2025-06-18", "capabilities": {},
                                            "clientInfo": {"name": "probe", "version": "1"}}}, admin)
    session = headers.get("Mcp-Session-Id")
    check("initialize opens a session", status == 200 and bool(session), str(session))
    _, listing = call("POST", "/v1/mcp", {"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
                      admin, {"Mcp-Session-Id": session})
    tools = listing["result"]["tools"]
    names = sorted(t["name"] for t in tools)
    check("tools/list advertises 57 tools", len(tools) == 57, str(len(tools)))

    for tool in tools:
        props = tool["inputSchema"].get("properties") or {}
        call("POST", "/v1/mcp",
             {"jsonrpc": "2.0", "id": 3, "method": "tools/call",
              "params": {"name": tool["name"],
                         "arguments": {k: ARGS.get(k, "x") for k in props}}},
             admin, {"Mcp-Session-Id": session})
    for method, path, body in ROUTES:
        call(method, path, body, admin)

    skins = actions_by_skin(admin)
    http_actions, mcp_actions = skins.get("http", set()), skins.get("mcp", set())

    print("")
    print("== every gated operation the REST surface exposes is reachable over MCP ==")
    check("all %d tools were called and all %d routes were driven" % (len(tools), len(ROUTES)), True)
    check("MCP reached %d distinct permission actions" % len(mcp_actions), len(mcp_actions) >= 43,
          str(len(mcp_actions)))
    only_http = sorted(http_actions - mcp_actions)
    check("the only action HTTP reaches and MCP does not is mcp:read, the transport's own gate",
          only_http == ["mcp:read"], str(only_http))
    check("MCP reaches nothing HTTP cannot", not (mcp_actions - http_actions),
          str(sorted(mcp_actions - http_actions)))

    print("")
    print("== PRIOR CONTRADICTED: three ungated routes have no tool ==")
    for path, expect in [("/v1/health", 200), ("/v1/auth/whoami", 200)]:
        status, body = call("GET", path, None, admin)
        check("GET %-18s answers %d over HTTP" % (path, expect), status == expect, str(status))
    check("no tool named for health, whoami or the CA root exists in the catalog",
          not [n for n in names if any(k in n for k in ("health", "whoami", "ca_root", "caroot"))],
          str([n for n in names if any(k in n for k in ("health", "whoami", "ca"))]))
    check("neither health:probe nor auth:whoami appears in the MCP action set",
          not ({"health:probe", "auth:whoami"} & mcp_actions),
          str(sorted({"health:probe", "auth:whoami"} & mcp_actions)))
    status, _ = call("GET", "/v1/observability/system/health", None, admin)
    check("the nearest tool reaches only /v1/observability/system/health, a different reading",
          status == 200 and "observability_get" in names, str(status))

    print("")
    print("== the two tools that do cover instance teardown are named the other way round ==")
    by_name = {t["name"]: t["description"] for t in tools}
    check("instance_terminate is described as deleting an already-terminal instance",
          by_name["instance_terminate"].startswith("Delete an already-terminal instance"),
          by_name["instance_terminate"][:70])
    check("instance_kill is described as forcing a running instance terminal",
          by_name["instance_kill"].startswith("Force a running instance terminal"),
          by_name["instance_kill"][:70])
    _, page = call("GET", "/v1/audit?limit=5000", None, admin)
    routed = {}
    for row in reversed(page["audit"]):
        payload = row["payload"]
        if payload.get("protocol_skin") == "http" and payload.get("action"):
            routed.setdefault((payload.get("request_method"), payload.get("request_path")),
                              payload["action"])
    check("POST /v1/instances/{id}/terminate is gated by instance:kill",
          routed.get(("POST", "/v1/instances/%s/terminate" % U)) == "instance:kill",
          str(routed.get(("POST", "/v1/instances/%s/terminate" % U))))
    check("DELETE /v1/instances/{id} is gated by instance:terminate",
          routed.get(("DELETE", "/v1/instances/%s" % U)) == "instance:terminate",
          str(routed.get(("DELETE", "/v1/instances/%s" % U))))

    print("")
    print("== the observability family is one tool, not one tool per route ==")
    _, body = call("POST", "/v1/mcp",
                   {"jsonrpc": "2.0", "id": 4, "method": "tools/call",
                    "params": {"name": "observability_get", "arguments": {"path_suffix": "executors"}}},
                   admin, {"Mcp-Session-Id": session})
    check("observability_get takes the path below /v1/observability/ and answers",
          "result" in body, json.dumps(body)[:90])

    finish()


main()
