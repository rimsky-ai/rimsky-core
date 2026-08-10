import json
import os

from harness import (boot, call, check, deploy, finish, new_instance, nodes, quiet,
                     register, send_message, show, timeline, tmpdir, deltas)

FS_CONFIG = """root: /workspace
host: 127.0.0.1
grpc_port: 9200
http_port: 9210
sweep_interval_seconds: 60
pick_policies:
  "@single":
    root: "single"
    on_commit: recycle
    on_give_up: recycle
    visibility_timeout_seconds: 60
    sync_strategy: on_open
  "@batch":
    root: "batch"
    on_commit: recycle
    on_give_up: recycle
    visibility_timeout_seconds: 60
    sync_strategy: on_open
"""

DIRECTIVE_FIELD = "{{claim.q.payload.folder}}"
DIRECTIVE_ROOT = "{{claim.q.payload}}"

BATCH_FOLDERS = ["w1", "w2", "w3", "w4"]


def attrs():
    return {"schema": {"type": "object", "properties": {
        "folder": {"type": "string", "source": DIRECTIVE_FIELD},
        "whole": {"type": "object", "source": DIRECTIVE_ROOT}}}}


def spec():
    return {
        "name": "exp-sub-claim-payload-substitution",
        "version": "1",
        "nodes": [
            {
                "type": "direct",
                "kind": "attribute_passthrough",
                "claim_producers": [{"name": "claim-producer-filesystem", "selector": "@single",
                                     "intent": "rw", "alias": "q"}],
                "error_types": {"acquire/unavailable": {"action": "give_up"}},
                "attributes": attrs(),
            },
            {
                "type": "fanned",
                "kind": "attribute_passthrough",
                "claim_producers": [{"name": "claim-producer-filesystem", "selector": "@batch",
                                     "intent": "rw", "alias": "q"}],
                "error_types": {"acquire/unavailable": {"action": "give_up"}},
                "fan_out": {
                    "claim": "q",
                    "partition_request": json.dumps({"batch_pick": {"max_items": 3}}),
                    "parallelism": 3,
                    "error_policy": {"kind": "best_effort"},
                },
                "attributes": attrs(),
            },
        ],
    }


def main():
    work = tmpdir()
    ws = os.path.join(work, "workspace")
    os.makedirs(os.path.join(ws, "single", "only"))
    for name in BATCH_FOLDERS:
        os.makedirs(os.path.join(ws, "batch", name))
    cfg = os.path.join(work, "fs.yml")
    with open(cfg, "w") as fh:
        fh.write(FS_CONFIG)
    boot(env={"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/etc/rimsky/fs.yml"},
         mounts=[(cfg, "/etc/rimsky/fs.yml"), (ws, "/workspace")])

    iid = new_instance(deploy(spec()))
    send_message(iid)
    tl = quiet(iid)
    show(tl)

    print("--- the standard claim directive on a regular Open'd claim")
    direct = deltas(tl, "direct")
    check("the payload field path resolves to the producer's per-claim data",
          direct == [{"folder": "only", "whole": {"folder": "only"}}], json.dumps(direct))

    print("--- the same directive text in a fan-out child's context")
    keys = []
    for r in tl:
        ck = r["payload"].get("child_keys")
        if ck:
            keys = sorted(ck)
    fanned = [d for d in deltas(tl, "fanned") if d]
    check("one sub-claim was opened per popped item", len(keys) == 3, json.dumps(keys))
    got = sorted(d["folder"] for d in fanned if "folder" in d)
    check("each child resolved the payload of its own sub-claim",
          got == keys, "children=%s sub-claims=%s" % (json.dumps(got), json.dumps(keys)))
    check("the whole-payload directive resolves to the same object in a child",
          all(d.get("whole") == {"folder": d.get("folder")} for d in fanned if "folder" in d),
          json.dumps(fanned))
    check("no child reused another child's payload", len(set(got)) == len(got), json.dumps(got))

    print("--- the two contexts resolve the same path the same way")
    shapes = {json.dumps(sorted(d.keys())) for d in fanned + direct if d}
    check("the resolved attribute shape is identical in both contexts",
          shapes == {json.dumps(["folder", "whole"])}, json.dumps(sorted(shapes)))

    node = [n for n in nodes(iid) if n["node_type"] == "fanned"][0]
    check("the fan-out parent and every clone settled fresh",
          node["run_summary"]["failed_count"] == 0 and node["run_summary"]["fresh_count"] == 4,
          json.dumps(node["run_summary"]))
    finish()


try:
    main()
finally:
    from harness import teardown
    teardown()
