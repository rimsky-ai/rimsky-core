import json
import os

from harness import (call, check, deploy, finish, live_runs, new_instance, quiet,
                     send_message, show, tmpdir, boot)

FS_CONFIG = """root: /workspace
host: 127.0.0.1
grpc_port: 9200
http_port: 9210
sweep_interval_seconds: 60
"""

UNREACHABLE = "http://127.0.0.1:9/refused"


def sub(node_type, signal):
    return {"node": node_type, "type": signal, "force_upstream_refresh": False}


def spec():
    return {
        "name": "exp-held-abandon-cascades-abandoned",
        "version": "1",
        "nodes": [
            {
                "type": "acquirer",
                "kind": "attribute_passthrough",
                "claim_producers": [{"name": "claim-producer-filesystem", "selector": "data",
                                     "intent": "rw", "alias": "ds"}],
                "error_types": {"acquire/unavailable": {"action": "give_up"}},
                "attributes": {"schema": {"type": "object", "properties": {
                    "v": {"type": "integer", "default": 1}}}},
            },
            {
                "type": "co-holder",
                "executor": "verifier-http",
                "holds": {"ds": {"from": "acquirer"}},
                "subscribes": [sub("acquirer", "terminal/success")],
                "error_types": {"verifier/network_error": {"action": "give_up"}},
                "attributes": {"schema": {"type": "object", "properties": {
                    "url": {"type": "string", "default": UNREACHABLE}}}},
            },
            {
                "type": "watcher-exact",
                "kind": "attribute_passthrough",
                "subscribes": [sub("acquirer", "terminal/error/abandoned")],
                "attributes": {"schema": {"type": "object", "properties": {
                    "v": {"type": "integer", "default": 1}}}},
            },
            {
                "type": "watcher-family",
                "kind": "attribute_passthrough",
                "subscribes": [sub("acquirer", "terminal/error/*")],
                "attributes": {"schema": {"type": "object", "properties": {
                    "v": {"type": "integer", "default": 1}}}},
            },
            {
                "type": "watcher-success",
                "kind": "attribute_passthrough",
                "subscribes": [sub("acquirer", "terminal/success")],
                "attributes": {"schema": {"type": "object", "properties": {
                    "v": {"type": "integer", "default": 1}}}},
            },
        ],
    }


def main():
    work = tmpdir()
    os.makedirs(os.path.join(work, "data"))
    cfg = os.path.join(work, "fs.yml")
    with open(cfg, "w") as fh:
        fh.write(FS_CONFIG)
    boot(env={"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/etc/rimsky/fs.yml"},
         mounts=[(cfg, "/etc/rimsky/fs.yml"), (os.path.join(work, "data"), "/workspace/data")])

    iid = new_instance(deploy(spec()))
    send_message(iid)
    tl = quiet(iid)
    show(tl)

    abandons = [r for r in tl if r["kind"] == "claim_resolution.abandon"]
    check("the held work's failure rolled the claim back with an abandon",
          len(abandons) == 1,
          json.dumps([r["kind"] for r in tl if "claim_resolution" in r["kind"]]))

    signals = [r for r in tl if r["node"] == "acquirer" and r["kind"].startswith("terminal/")]
    check("the acquirer emits exactly the abandoned-error signal",
          [r["kind"] for r in signals] == ["terminal/error/abandoned"],
          json.dumps([r["kind"] for r in signals]))

    for node in ("watcher-exact", "watcher-family"):
        starts = [r for r in tl if r["node"] == node and r["kind"] == "work_started"]
        check("the subscriber on %s saw the abandoned signal fire" % node,
              len(starts) == 1, json.dumps([(r["seq"], r["kind"]) for r in tl if r["node"] == node]))
        check("%s ran only at the moment the held work was abandoned" % node,
              starts and abandons and starts[0]["seq"] > abandons[0]["seq"],
              "abandon seq=%s start seq=%s" % (abandons[0]["seq"] if abandons else None,
                                               starts[0]["seq"] if starts else None))

    check("the success subscriber never ran, so the rollback is not reported as success",
          not [r for r in tl if r["node"] == "watcher-success" and r["kind"] == "work_started"],
          json.dumps([(r["seq"], r["kind"]) for r in tl if r["node"] == "watcher-success"]))
    finish()


try:
    main()
finally:
    from harness import teardown
    teardown()
