import json
import os
import subprocess

from harness import (STATE, boot, call, check, deploy, die, docker, finish, new_instance,
                     new_network, quiet, send_message, show, start_container, tmpdir)

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")

RIMSKY_YML = """persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers:
  content:
    endpoint: "grpc://content-producer:9500"
    tls: "off"
    write_semantics_allowed: ["sync"]
    protocols: ["claim_producer", "data_processing"]
named_locks: {}
executors: {}
"""

ALIAS = "materializer.dataset"


def sub(node_type, signal):
    return {"node": node_type, "type": signal, "force_upstream_refresh": False}


SPEC = {
    "name": "exp-asset-management",
    "version": "1",
    "nodes": [
        {
            "type": "materializer",
            "kind": "attribute_passthrough",
            "claim_producers": [{"name": "content", "selector": "datasets/items",
                                 "intent": "rw", "alias": "dataset", "lifetime": "durable"}],
            "error_types": {"acquire/unavailable": {"action": "give_up"}},
            "attributes": {"schema": {"type": "object", "properties": {
                "rows": {"type": "integer", "default": 3}}}},
        },
        {
            "type": "consumer",
            "kind": "attribute_passthrough",
            "subscribes": [sub("materializer", "terminal/success"),
                           sub("materializer", "attribute/rows/changed")],
            "attributes": {"schema": {"type": "object", "properties": {
                "read": {"type": "integer", "source": "{{nodes.materializer.attribute.rows}}"}}}},
        },
    ],
}


def cli(*args):
    res = subprocess.run([CLI, *args, "--endpoint", STATE["base"]],
                         capture_output=True, text=True)
    return res.returncode, res.stdout.strip(), res.stderr.strip()


def build_producer():
    bindir = tmpdir()
    arch = docker("version", "--format", "{{.Server.Arch}}").stdout.strip() or "arm64"
    env = dict(os.environ, GOOS="linux", GOARCH=arch, CGO_ENABLED="0")
    build = subprocess.run(["go", "build", "-o", os.path.join(bindir, "producer"), HERE],
                           capture_output=True, text=True, cwd=ROOT, env=env)
    if build.returncode != 0:
        die("producer build failed:\n" + build.stderr)
    return bindir


def main():
    bindir = build_producer()
    net = new_network()
    start_container("alpine:latest", ["/exp/producer", "-bind", "0.0.0.0:9500"],
                    network=net, alias="content-producer", mounts=[(bindir, "/exp")])
    work = tmpdir()
    cfg = os.path.join(work, "rimsky.yml")
    with open(cfg, "w") as fh:
        fh.write(RIMSKY_YML)
    boot(mounts=[(cfg, "/etc/rimsky/rimsky.yml")], network=net)

    iid = new_instance(deploy(SPEC))
    send_message(iid)
    tl = quiet(iid)
    show(tl)

    print("--- the operator lists the data assets the running instance produced")
    rc, out, err = cli("asset", "list", "--instance", iid, "-o", "json")
    print("    asset list -> rc=%s %s %s" % (rc, out[:400], err[:200]))
    assets = json.loads(out) if out else []
    check("the durable claim the node materialized is listed as an asset",
          rc == 0 and len(assets) == 1 and assets[0]["alias"] == ALIAS,
          json.dumps(assets)[:300])

    print("--- the operator sees the current version of the asset")
    rc, out, _ = cli("asset", "show", "--instance", iid, ALIAS, "-o", "json")
    detail = json.loads(out) if out else {}
    print("    asset show -> %s" % out[:400])
    check("the asset detail carries the version the producer committed",
          rc == 0 and detail.get("version_id") == "v1", json.dumps(detail)[:300])
    claim_id = detail.get("claim_id")

    print("--- the operator walks the version history")
    rc, out, _ = cli("asset", "versions", "--instance", iid, ALIAS, "-o", "json")
    versions = (json.loads(out) or {}).get("versions") if out else None
    print("    asset versions -> %s" % out[:400])
    check("the version history comes back from the producer",
          rc == 0 and versions and [v["version_id"] for v in versions] == ["v1"],
          json.dumps(versions)[:300])

    print("--- the operator walks the materialization audit")
    status, body = call("GET", "/v1/instances/%s/assets/%s/materialization-history" % (iid, ALIAS))
    history = (body or {}).get("materialization_history") or []
    print("    materialization-history -> %s" % json.dumps(body)[:400])
    check("the materialization audit records the claim's terminal resolution",
          status == 200 and len(history) >= 1
          and all(h["record_kind"] == "claim_terminal" for h in history),
          json.dumps(history)[:300])

    print("--- the operator traces the asset's lineage")
    rc, out, err = cli("asset", "lineage", "--instance", iid, ALIAS, "-o", "json")
    print("    asset lineage -> rc=%s %s %s" % (rc, out[:400], err[:200]))
    check("the lineage walk from the asset returns records", rc == 0 and out not in ("", "[]"),
          out[:200])

    materializing_run = history[0]["record"]["run_id"]
    consumer_node = [n["id"] for n in call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]
                     if n["node_type"] == "consumer"][0]
    status, body = call("GET", "/v1/lineage/claims/%s/descendants?depth=5" % claim_id)
    print("    claim descendants -> %s" % json.dumps(body)[:800])
    status2, body2 = call("GET", "/v1/lineage/runs/%s/descendants?depth=5" % materializing_run)
    print("    run descendants -> %s" % json.dumps(body2)[:800])
    status3, body3 = call("GET", "/v1/lineage/by-source/run/%s" % materializing_run)
    print("    by-source run -> %s" % json.dumps(body3)[:800])
    check("the forward walk from the asset's materializing run shows the downstream work that consumed it",
          status3 == 200 and consumer_node in json.dumps(body3),
          json.dumps(body3)[:300])
    check("the run-descendants walk names the consuming run too",
          status2 == 200 and consumer_node in json.dumps(body2), json.dumps(body2)[:300])
    check("the claim-side walk stays on the claim's own terminal records",
          status == 200 and claim_id in json.dumps(body), json.dumps(body)[:300])

    print("--- the operator retires the asset")
    rc, out, err = cli("asset", "delete", "--instance", iid, ALIAS)
    print("    asset delete -> rc=%s %s %s" % (rc, out, err))
    check("the retire call succeeds", rc == 0, "%s %s" % (out, err))
    rc, out, _ = cli("asset", "list", "--instance", iid, "-o", "json")
    check("the retired asset is gone from the listing", rc == 0 and json.loads(out or "[]") == [],
          out[:200])
    finish()


try:
    main()
finally:
    from harness import teardown
    teardown()
