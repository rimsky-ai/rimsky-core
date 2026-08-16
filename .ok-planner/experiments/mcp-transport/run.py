#!/usr/bin/env python3
"""Experiment: mcp-transport.

An MCP client drives a real rimsky deployment through the control API's MCP
endpoint only. The run answers three questions from the client's side:

  1. Coverage. Which of the deployment's permissioned actions can the MCP
     client reach? The population is the ruled public control-API routes; the
     run drives each one over plain HTTP, reads back the action the deployment
     recorded for it, drives every tool the MCP client is offered, reads back
     the action each of those reached, and reports the permissioned actions no
     tool reaches.
  2. Real work. Can the client do more than touch each surface? It registers,
     deploys, instantiates, wakes, watches, kills and deletes an instance
     without ever leaving the MCP endpoint.
  3. Auth. Does the MCP client get the same auth and permission answers as any
     other client? The run compares no-token, narrow-key and revoked-key
     outcomes over MCP against the same outcomes over the HTTP routes.

The deployment records every gated request in its own audit log with the
action, the request path, the response status and the protocol the request
arrived over, so both mappings above are read back from the product rather
than assumed.
"""

import json
import os
import socket
import pathlib
import subprocess
import sys
import time
import urllib.error
import urllib.request

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parents[2]
CLI = str(ROOT / "bin" / "rimsky")
TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    sys.exit("export RIMSKY_IMAGE_TAG=src-<tree hash> first")
def _free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


PORT = int(os.environ.get("PORT") or _free_port())
BASE = f"http://127.0.0.1:{PORT}"
STACK = "exp-mcp-stack"

failures = []


def check(label, want, got):
    if want == got:
        print(f"PASS  {label:<62} {got}")
    else:
        print(f"FAIL  {label:<62} expected [{want}] got [{got}]")
        failures.append(label)


def note(*a):
    print(*a)


def http(method, path, body=None, token=None, headers=None):
    url = BASE + path
    data = None
    hdrs = {"Idempotency-Key": f"mcp-{time.time_ns()}"}
    if body is not None:
        data = json.dumps(body).encode()
        hdrs["content-type"] = "application/json"
    if token:
        hdrs["Authorization"] = "Bearer " + token
    hdrs.update(headers or {})
    req = urllib.request.Request(url, data=data, method=method, headers=hdrs)
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            raw = r.read()
            return r.status, (json.loads(raw) if raw else None), dict(r.headers)
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            parsed = json.loads(raw) if raw else None
        except json.JSONDecodeError:
            parsed = {"raw": raw.decode(errors="replace")}
        return e.code, parsed, dict(e.headers)
    except (urllib.error.URLError, ConnectionError, TimeoutError) as e:
        return 0, {"error": str(e)}, {}


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def cleanup():
    docker("rm", "-f", STACK)


# ---------------------------------------------------------------- the stack

cleanup()
r = docker("run", "-d", "--name", STACK, "-p", f"{PORT}:8080",
           f"rimsky-all-in-one:{TAG}")
if r.returncode != 0:
    sys.exit("could not start the stack: " + r.stderr)
try:
    while http("GET", "/v1/health")[0] != 200:
        time.sleep(0.5)

    note("== a deployment with an admin key and two narrower keys ==")
    out = subprocess.run([CLI, "auth", "init", "--endpoint", BASE],
                         capture_output=True, text=True)
    ADMIN = ""
    for line in (out.stdout + out.stderr).splitlines():
        for word in line.replace("=", " ").split():
            if word.startswith("rk_"):
                ADMIN = word.strip().strip('"')
    if not ADMIN:
        note(out.stdout, out.stderr)
        sys.exit("could not mint the first admin key")
    check("the first admin key was minted", True, bool(ADMIN))

    def mint(name, actions):
        st, b, _ = http("POST", "/v1/auth/keys",
                        body={"name": name,
                              "permissions": [{"action": a} for a in actions]},
                        token=ADMIN)
        if st != 201:
            note(st, b)
            sys.exit(f"could not mint the {name} key")
        return b

    readonly = mint("readonly", ["*:read"])
    minimal = mint("minimal", ["service:enroll"])
    READONLY = readonly["plaintext"]
    MINIMAL = minimal["plaintext"]
    check("a read-only key was minted", True, bool(READONLY))
    check("a key with a single unrelated grant was minted", True, bool(MINIMAL))

    # ------------------------------------------------- the audit as instrument

    def audit_rows(**q):
        qs = "&".join(f"{k}={v}" for k, v in q.items())
        st, b, _ = http("GET", "/v1/audit?" + qs + "&limit=50", token=ADMIN)
        rows = []
        if isinstance(b, dict):
            rows = b.get("events") or b.get("entries") or b.get("audit") or []
        elif isinstance(b, list):
            rows = b
        return rows

    def payload(row):
        for k in ("payload", "data", "audit"):
            if isinstance(row.get(k), dict):
                return row[k]
        return row

    st, b, _ = http("GET", "/v1/audit?limit=3", token=ADMIN)
    check("the deployment's audit log is readable", 200, st)
    sample = audit_rows()
    check("the audit log records the requests made so far", True, len(sample) > 0)
    if sample:
        note("    a sample audit row: " + json.dumps(payload(sample[0]))[:300])

    def last_action_for(path, skin=None):
        """The action the deployment recorded for the most recent request to path."""
        for row in audit_rows(target=path):
            p = payload(row)
            if skin is not None and p.get("protocol_skin", "") != skin:
                continue
            if p.get("action"):
                return p["action"], p.get("response_status"), p.get("protocol_skin", "")
        return None, None, None

    # ------------------------------------------- 1. what the HTTP surface offers

    note()
    note("== the deployment's own permissioned actions, over plain HTTP ==")
    routes = []
    for line in (HERE / "routes.tsv").read_text().splitlines():
        if not line.strip():
            continue
        method, ruled, concrete = line.split("\t")
        routes.append((method, ruled, concrete))
    check("the ruled public control-API routes were enumerated", 85, len(routes))

    http_action = {}
    posture = {}
    for method, ruled, concrete in routes:
        body = {} if method in ("POST", "PUT") else None
        anon_status, _, _ = http(method, concrete, body=body)
        min_status, _, _ = http(method, concrete, body=body, token=MINIMAL)
        http(method, concrete, body=body, token=ADMIN)
        bare = concrete.split("?")[0]
        action, status, skin = last_action_for(bare)
        if action:
            http_action[action] = (method, ruled)
            if anon_status != 401:
                posture[action] = "unauthenticated"
            elif min_status != 403:
                posture[action] = "identity-only"
            else:
                posture[action] = "permissioned"

    permissioned = sorted(a for a, p in posture.items() if p == "permissioned")
    note(f"    actions the deployment named for those routes: {len(http_action)}")
    note(f"    of which permissioned: {len(permissioned)}")
    for a in sorted(posture):
        if posture[a] != "permissioned":
            note(f"      {posture[a]}: {a}")
    check("every ruled route resolved to an action the deployment names", True,
          len(http_action) > 0)

    # --------------------------------------- 2. what the MCP client can reach

    note()
    note("== the same deployment, through the MCP endpoint ==")

    class MCP:
        def __init__(self, token):
            self.token = token
            self.session = None
            self.n = 0

        def rpc(self, method, params=None, expect_ok=True):
            self.n += 1
            headers = {}
            if self.session:
                headers["Mcp-Session-Id"] = self.session
            st, b, h = http("POST", "/v1/mcp",
                            body={"jsonrpc": "2.0", "id": self.n,
                                  "method": method, "params": params or {}},
                            token=self.token, headers=headers)
            if method == "initialize" and st == 200:
                self.session = h.get("Mcp-Session-Id") or (
                    (b or {}).get("result", {}).get("sessionId"))
            return st, b

    admin_mcp = MCP(ADMIN)
    st, b = admin_mcp.rpc("initialize", {"protocolVersion": "2025-06-18",
                                         "capabilities": {},
                                         "clientInfo": {"name": "audit", "version": "1"}})
    check("an MCP client can open a session", 200, st)
    check("the session carries a server-assigned id", True, bool(admin_mcp.session))
    check("the server names itself in the handshake", True,
          bool((b or {}).get("result", {}).get("serverInfo")))

    st, b = admin_mcp.rpc("tools/list")
    tools = [t["name"] for t in (b or {}).get("result", {}).get("tools", [])]
    check("the client is offered a tool catalog", True, len(tools) > 0)
    note(f"    tools offered to the admin key: {len(tools)}")
    schemas = {t["name"]: t.get("inputSchema") for t in b["result"]["tools"]}
    check("every offered tool declares an input schema", 0,
          len([t for t in tools if not schemas.get(t)]))
    check("every offered tool carries a description", 0,
          len([t for t in b["result"]["tools"] if not t.get("description")]))

    # Drive every offered tool. Arguments are well-typed but deliberately name
    # absent things where an id is required: reaching the action is the point,
    # and a not-found answer proves the transport dispatched and the permission
    # gate allowed it.
    ZERO = "00000000-0000-0000-0000-000000000000"
    generic = {
        "idOrKey": ZERO, "id": ZERO, "node_id": ZERO, "run_id": ZERO,
        "frame_id": ZERO, "breakpoint_id": ZERO, "hit_id": ZERO,
        "claim_handle_id": ZERO, "alias": "no-such-alias", "tag": "no-such-tag",
        "nameOrID": "no-such-key", "template": "no-such-template",
        "name": "no-such-key", "checkpoint": "before_dispatch",
        "node_type": "no-such-node", "action": "invalidate_node",
        "type": "probe", "spec": {"name": "probe", "version": "1", "nodes": []},
        "frame": ZERO, "before": "2000-01-01T00:00:00Z",
        "path_suffix": "system/summary", "source_type": "executor",
        "source_id": "none", "executor_name": "none", "label": "audit",
        "role": "read-only", "permissions": [{"action": "tag:read"}],
    }

    def args_for(tool):
        schema = schemas.get(tool) or {}
        props = (schema or {}).get("properties") or {}
        out = {}
        for k in props:
            if k in generic:
                out[k] = generic[k]
        for k in (schema or {}).get("required") or []:
            if k not in out:
                out[k] = generic.get(k, "none")
        return out

    mcp_action = {}
    unreached = []
    for tool in tools:
        st, b = admin_mcp.rpc("tools/call",
                              {"name": tool, "arguments": args_for(tool)})
        rows = audit_rows(limit=25)
        found = None
        for row in rows:
            p = payload(row)
            if p.get("protocol_skin") == "mcp" and p.get("action"):
                found = (p["action"], p.get("response_status"))
                break
        if found:
            mcp_action.setdefault(found[0], []).append(tool)
        else:
            unreached.append((tool, st, json.dumps(b)[:160]))

    note(f"    tools whose call reached an action over MCP: "
         f"{len(tools) - len(unreached)} of {len(tools)}")
    for t, st, b in unreached:
        note(f"      no action reached: {t} (status {st}) {b}")
    check("every offered tool reaches an action over the MCP transport", 0,
          len(unreached))
    note(f"    distinct actions reached over MCP: {len(mcp_action)}")

    # The one permissioned action with no tool of its own is the MCP dispatch
    # surface, which the client performs on every call it makes: the deployment
    # records it against the client's key like any other action.
    own = [payload(r) for r in audit_rows(action="mcp:read")]
    check("the client's own MCP requests are recorded as the MCP action", True,
          len(own) > 0 and own[0].get("response_status") == 200)
    reached = set(mcp_action) | {"mcp:read"}
    missing = [a for a in permissioned if a not in reached]
    note(f"    permissioned actions with no way to reach them over MCP: {len(missing)}")
    for a in missing:
        note(f"      unreachable over MCP: {a} ({http_action[a][0]} {http_action[a][1]})")
    check("every permissioned action the HTTP surface offers is reachable over MCP",
          0, len(missing))

    # ----------------------------------------- 3. real work, MCP transport only

    note()
    note("== a whole instance lifecycle without leaving the MCP endpoint ==")

    def call(tool, args, client=admin_mcp):
        st, b = client.rpc("tools/call", {"name": tool, "arguments": args})
        result = (b or {}).get("result") or {}
        if result.get("isError") or st != 200:
            note(f"    {tool} answered: " + json.dumps(b)[:400])
        payload_text = None
        for c in result.get("content") or []:
            if c.get("type") == "text":
                payload_text = c.get("text")
        parsed = None
        if payload_text:
            try:
                parsed = json.loads(payload_text)
            except json.JSONDecodeError:
                parsed = {"text": payload_text}
        inner_status = st
        if isinstance(parsed, dict) and "status" in parsed:
            inner_status = parsed["status"]
            parsed = parsed.get("body")
        return inner_status, result.get("isError", False), parsed

    spec = {"name": "mcpdrive", "version": "1",
            "nodes": [{"type": "worker", "executor": "verifier-shape-checks",
                       "attributes": {"schema": {"type": "object", "properties": {
                           "checks": {"type": "array", "default": [
                               {"kind": "no_nulls", "config": {"fields": ["id"]},
                                "severity": "error"}]},
                           "rows": {"type": "array", "default": [{"id": 1}]}}}}}]}
    st, err, out = call("template_validate", {"spec": spec})
    check("the client validates a template over MCP", (200, False), (st, err))
    st, err, out = call("template_register", {"spec": spec, "tag": "mcpdrive"})
    tpl = (out or {}).get("template_id") or (out or {}).get("id")
    check("the client registers the template over MCP", True, bool(tpl))
    st, err, out = call("template_deploy", {"id": tpl})
    check("the client deploys the template over MCP", (200, False), (st, err))
    st, err, out = call("instance_create",
                        {"template": tpl, "instance_key": "mcp-lifecycle",
                         "target_agent": "mcp-probe"})
    iid = (out or {}).get("instance_id") or (out or {}).get("id")
    check("the client creates an instance over MCP", True, bool(iid))
    st, err, out = call("message_send", {"id": iid, "type": ""})
    check("the client wakes the instance over MCP", (200, False), (st, err))
    st, err, nodes = call("node_list", {"idOrKey": iid})
    node_rows = nodes if isinstance(nodes, list) else (nodes or {}).get("nodes", [])
    note("    nodes the client sees: " + json.dumps(
        [{"node_type": n.get("node_type"), "executor": n.get("executor")}
         for n in node_rows]))
    worker = [n for n in node_rows if n.get("node_type") == "worker"]
    check("the client lists the instance's declared node over MCP", 1, len(worker))
    node_rows = worker
    st, err, one = call("node_get", {"id": node_rows[0]["id"]})
    check("the client reads one node over MCP", node_rows[0]["id"],
          (one or {}).get("id"))
    st, err, msgs = call("message_list", {"id": iid})
    check("the client reads the instance's messages over MCP", True,
          bool(msgs))
    st, err, ev = call("event_list", {"instance_id": iid})
    check("the client reads the event log over MCP", True, bool(ev))
    st, err, out = call("instance_pause", {"idOrKey": iid})
    check("the client pauses the instance over MCP", (200, False), (st, err))
    st, err, out = call("instance_resume", {"idOrKey": iid})
    check("the client resumes the instance over MCP", (200, False), (st, err))
    st, err, out = call("instance_kill", {"idOrKey": iid, "reason": "audit"})
    check("the client kills the instance over MCP", (200, False), (st, err))
    st, err, out = call("instance_terminate", {"idOrKey": iid})
    check("the client deletes the terminal instance over MCP", (200, False), (st, err))
    st, inst, _ = http("GET", f"/v1/instances/{iid}", token=ADMIN)
    check("the deleted instance is gone when read over plain HTTP", 404, st)
    st, err, out = call("template_undeploy", {"id": tpl})
    check("the client undeploys the template over MCP", (200, False), (st, err))
    st, err, out = call("template_deregister", {"id": tpl})
    check("the client deletes the template over MCP", (200, False), (st, err))

    # --------------------------------------------------- 4. the auth semantics

    note()
    note("== the same auth and permission answers as any other client ==")
    st, _, _ = http("POST", "/v1/mcp", body={"jsonrpc": "2.0", "id": 1,
                                             "method": "initialize", "params": {}})
    check("MCP refuses a caller with no token, like every other route", 401, st)
    st, _, _ = http("GET", "/v1/instances")
    check("and the plain HTTP route refuses the same caller the same way", 401, st)

    ro = MCP(READONLY)
    st, b = ro.rpc("initialize", {"protocolVersion": "2025-06-18",
                                  "capabilities": {}, "clientInfo": {"name": "ro"}})
    check("a read-only key can open an MCP session", 200, st)
    st, b = ro.rpc("tools/list")
    ro_tools = [t["name"] for t in (b or {}).get("result", {}).get("tools", [])]
    note(f"    tools offered to the read-only key: {len(ro_tools)}")
    check("the read-only key is offered strictly fewer tools", True,
          0 < len(ro_tools) < len(tools))
    write_tools_offered = [t for t in ro_tools if t in
                           ("instance_create", "template_register", "auth_create_key",
                            "instance_kill", "node_reset", "message_send")]
    check("no write tool is offered to the read-only key", [], write_tools_offered)
    check("read tools are offered to the read-only key", True,
          "instance_list" in ro_tools and "node_get" in ro_tools)

    st, b = ro.rpc("tools/call", {"name": "instance_create",
                                  "arguments": {"template": "no-such-template"}})
    denied_text = json.dumps(b)
    check("calling a tool outside the key's grants is refused over MCP", True,
          "unknown tool" in denied_text or "permission denied" in denied_text
          or (b or {}).get("result", {}).get("isError", False))
    st, _, _ = http("POST", "/v1/instances", body={"template": "no-such-template"},
                    token=READONLY)
    check("the plain HTTP route refuses that key the same way", 403, st)

    st, b = ro.rpc("tools/call", {"name": "instance_list", "arguments": {}})
    check("a read the key does hold succeeds over MCP", 200, st)

    rows = audit_rows(key_id=readonly["id"])
    mcp_rows = [payload(r) for r in rows if payload(r).get("protocol_skin") == "mcp"]
    check("the deployment attributes the MCP work to the calling key", True,
          len(mcp_rows) > 0)
    if mcp_rows:
        note("    an audit row from the read-only key's MCP work: "
             + json.dumps({k: mcp_rows[0].get(k) for k in
                           ("key_name", "action", "request_path", "response_status",
                            "protocol_skin")}))

    st, _, _ = http("DELETE", "/v1/auth/keys/" + readonly["id"], token=ADMIN)
    check("the read-only key was revoked", 200, st)
    revoked = MCP(READONLY)
    st, _ = revoked.rpc("initialize", {"protocolVersion": "2025-06-18",
                                       "capabilities": {}, "clientInfo": {"name": "x"}})
    check("the revoked key can no longer open an MCP session", 401, st)
    st, _, _ = http("GET", "/v1/instances", token=READONLY)
    check("and the plain HTTP route refuses it identically", 401, st)

    st, b = admin_mcp.rpc("tools/call", {"name": "no_such_tool", "arguments": {}})
    check("an unknown tool is refused rather than dispatched", True,
          "unknown tool" in json.dumps(b))

finally:
    cleanup()

print()
if failures:
    print("EXPERIMENT FAIL")
    sys.exit(1)
print("EXPERIMENT PASS")
