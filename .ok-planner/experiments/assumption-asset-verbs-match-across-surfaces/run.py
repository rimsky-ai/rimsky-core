SLUG = "assumption-asset-verbs-match-across-surfaces"

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

CLI_VERBS = ["list", "show", "versions", "delete", "lineage"]


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
        return status, (json.loads(text) if text else None), text
    except ValueError:
        return status, text, text


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
    return subprocess.run([CLI, *args], capture_output=True, text=True, env=env)


def finish():
    docker("rm", "-f", NAME)
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


def mcp_session():
    status, _, headers = raw("POST", "/v1/mcp", {
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {"protocolVersion": "2025-06-18", "capabilities": {},
                   "clientInfo": {"name": "probe", "version": "1"}}})
    if status != 200 or not headers.get("Mcp-Session-Id"):
        die("MCP initialize failed: %s" % status)
    return headers["Mcp-Session-Id"]


def rpc(session, method, params=None):
    payload = {"jsonrpc": "2.0", "id": 7, "method": method}
    if params is not None:
        payload["params"] = params
    return call("POST", "/v1/mcp", payload, None, {"Mcp-Session-Id": session})[1]


def main():
    boot()
    _, out, _ = call("POST", "/v1/templates", {"spec": {
        "name": "asset-probe", "version": "1",
        "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}})
    template_id = out["template_id"]
    call("POST", "/v1/templates/%s/deploy" % template_id, {})
    _, out, _ = call("POST", "/v1/instances", {"template": template_id, "instance_key": "asset-1",
                                               "target_agent": "audit-agent"})
    instance = out["instance_id"]
    alias = "w.files"

    print("== the CLI surface ==")
    usage = cli("asset").stderr + cli("asset").stdout
    check("`rimsky asset` names five subcommands",
          all(("<" + "|".join(CLI_VERBS) + ">") in usage for _ in [0]), usage.strip()[:110])
    for verb in CLI_VERBS:
        res = cli("asset", verb, "--endpoint", STATE["base"], "--instance", instance, alias)
        check("rimsky asset %-9s is a real verb" % verb,
              "unknown subcommand" not in (res.stderr + res.stdout),
              (res.stderr or res.stdout).strip().replace("\n", " ")[:90])
    res = cli("asset", "materialization-history", "--endpoint", STATE["base"], "--instance", instance, alias)
    check("rimsky asset materialization-history is NOT a verb",
          'unknown subcommand "materialization-history"' in res.stderr, res.stderr.strip()[:90])

    print("")
    print("== the REST surface ==")
    for label, method, path in [
            ("assets", "GET", "/v1/instances/%s/assets" % instance),
            ("assets/{alias}", "GET", "/v1/instances/%s/assets/%s" % (instance, alias)),
            ("assets/{alias}/versions", "GET", "/v1/instances/%s/assets/%s/versions" % (instance, alias)),
            ("assets/{alias}/materialization-history", "GET",
             "/v1/instances/%s/assets/%s/materialization-history" % (instance, alias)),
            ("assets/{alias} (DELETE)", "DELETE", "/v1/instances/%s/assets/%s" % (instance, alias))]:
        status, _, text = call(method, path)
        check("%-40s is a mounted route" % label,
              "404 page not found" not in text, "%s %s" % (status, text.strip()[:50]))
    status, _, text = call("GET", "/v1/instances/%s/assets/%s/lineage" % (instance, alias))
    check("assets/{alias}/lineage                   is NOT a route",
          status == 404 and "404 page not found" in text, "%s %s" % (status, text.strip()[:50]))

    print("")
    print("== the MCP surface ==")
    session = mcp_session()
    tools = sorted(t["name"] for t in rpc(session, "tools/list")["result"]["tools"])
    asset_tools = [t for t in tools if t.startswith("asset")]
    check("the asset tools are exactly five, and materialization history is one of them",
          asset_tools == ["asset_delete", "asset_get", "asset_list",
                          "asset_materialization_history", "asset_versions"],
          ",".join(asset_tools))
    check("there is no asset_lineage tool", "asset_lineage" not in tools, ",".join(asset_tools))
    lineage_tools = [t for t in tools if t.startswith("lineage_")]
    check("the lineage tools that exist are claim- and run-shaped, not asset-shaped",
          lineage_tools == ["lineage_claim_ancestors", "lineage_claim_descendants", "lineage_get",
                            "lineage_prune", "lineage_run_ancestors", "lineage_run_descendants"],
          ",".join(lineage_tools))
    called = rpc(session, "tools/call", {"name": "asset_materialization_history",
                                         "arguments": {"id": instance, "alias": alias}})
    check("asset_materialization_history answers as a tool", "result" in called,
          json.dumps(called)[:100])

    print("")
    print("== PRIOR CONTRADICTED: the three surfaces do not carry the same five operations ==")
    operations = ["list", "get", "versions", "delete", "materialization-history", "lineage"]
    cli_has = {"list": True, "get": True, "versions": True, "delete": True,
               "materialization-history": False, "lineage": True}
    rest_has = {"list": True, "get": True, "versions": True, "delete": True,
                "materialization-history": True, "lineage": False}
    mcp_has = {"list": "asset_list" in tools, "get": "asset_get" in tools,
               "versions": "asset_versions" in tools, "delete": "asset_delete" in tools,
               "materialization-history": "asset_materialization_history" in tools,
               "lineage": "asset_lineage" in tools}
    for op in operations:
        print("      %-24s CLI %-3s REST %-3s MCP %s" % (
            op, "yes" if cli_has[op] else "no",
            "yes" if rest_has[op] else "no", "yes" if mcp_has[op] else "no"))
    uneven = [op for op in operations
              if len({cli_has[op], rest_has[op], mcp_has[op]}) != 1]
    check("two of the six asset operations are missing from a surface",
          uneven == ["materialization-history", "lineage"], ",".join(uneven))
    check("lineage is CLI-only", cli_has["lineage"] and not rest_has["lineage"]
          and not mcp_has["lineage"])
    check("materialization history is REST and MCP only",
          not cli_has["materialization-history"] and rest_has["materialization-history"]
          and mcp_has["materialization-history"])
    res = cli("asset", "lineage", "--endpoint", STATE["base"], "--instance", instance, alias)
    check("the CLI verb is a client-side composition — it fails on the asset lookup first",
          "/assets/" in (res.stderr + res.stdout),
          (res.stderr or res.stdout).strip().replace("\n", " ")[:110])

    finish()


main()
