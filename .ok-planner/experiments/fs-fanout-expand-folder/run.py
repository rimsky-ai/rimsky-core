import json
import os
import subprocess
import sys
import urllib.request

from harness import (boot, call, check, deploy, finish, free_port, new_instance, nodes,
                     quiet, send_message, show, tmpdir)

FS_CONFIG = """root: /workspace
host: 127.0.0.1
grpc_port: 9200
http_port: 9210
sweep_interval_seconds: 60
pick_policies:
  "@batches":
    root: "batches"
    on_commit: recycle
    on_give_up: recycle
    visibility_timeout_seconds: 60
    sync_strategy: on_open
"""

FOLDERS = {
    "b1": ["alpha-1.txt", "beta-1.txt", "gamma-1.txt"],
    "b2": ["alpha-2.txt", "beta-2.txt", "gamma-2.txt"],
}


def spec(url):
    return {
        "name": "exp-fs-fanout-expand-folder",
        "version": "1",
        "nodes": [{
            "type": "per-file",
            "executor": "verifier-http",
            "claim_producers": [{"name": "claim-producer-filesystem", "selector": "@batches",
                                 "intent": "rw", "alias": "folder"}],
            "error_types": {"acquire/unavailable": {"action": "give_up"}},
            "fan_out": {
                "claim": "folder",
                "partition_request": json.dumps({"expand_folder": {"filter": "*.txt"}}),
                "parallelism": 3,
                "error_policy": {"kind": "best_effort"},
            },
            "attributes": {"schema": {"type": "object", "properties": {
                "url": {"type": "string", "source": url + "/{{child.partition_key}}"}}}},
        }],
    }


def get(url):
    with urllib.request.urlopen(url) as resp:
        return json.loads(resp.read().decode())


def main():
    here = os.path.dirname(os.path.abspath(__file__))
    work = tmpdir()
    ws = os.path.join(work, "workspace")
    for folder, files in FOLDERS.items():
        os.makedirs(os.path.join(ws, "batches", folder))
        for name in files:
            with open(os.path.join(ws, "batches", folder, name), "w") as fh:
                fh.write(name)
        with open(os.path.join(ws, "batches", folder, "notes.md"), "w") as fh:
            fh.write("not a txt file")
    cfg = os.path.join(work, "fs.yml")
    with open(cfg, "w") as fh:
        fh.write(FS_CONFIG)

    port = free_port()
    server = subprocess.Popen([sys.executable, os.path.join(here, "peak_server.py"), str(port)])
    try:
        while True:
            try:
                get("http://127.0.0.1:%d/seen" % port)
                break
            except Exception:
                pass
        boot(env={"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/etc/rimsky/fs.yml"},
             mounts=[(cfg, "/etc/rimsky/fs.yml"), (ws, "/workspace")])

        iid = new_instance(deploy(spec("http://host.docker.internal:%d/file" % port)))
        send_message(iid)
        tl = quiet(iid)
        show(tl)

        handles = call("GET", "/v1/observability/claim-handles?instance_id=%s" % iid)[1]["claim_handles"]
        parents = [h for h in handles if not h.get("parent_claim_handle_id")]
        check("exactly one folder was picked from the store as the node's claim", len(parents) == 1,
              json.dumps([h["claim_scope_data"] for h in handles]))
        picked = [f for f in FOLDERS if parents and parents[0]["claim_scope_data"].endswith("/" + f)]
        check("the picked claim is one of the store's folders", len(picked) == 1,
              json.dumps([h["claim_scope_data"] for h in parents]))
        want = sorted(FOLDERS[picked[0]]) if picked else []
        other = sorted(sum((v for k, v in FOLDERS.items() if k != picked[0]), [])) if picked else []
        print("    picked folder: %s -> %s" % (picked, want))

        counts = [r["payload"].get("sub_scope_descriptor_count") for r in tl
                  if "sub_scope_descriptor_count" in json.dumps(r["payload"])]
        check("the producer's split returned one sub-scope per matching file in the picked folder",
              counts == [len(want)], json.dumps(counts))

        keys = []
        for r in tl:
            ck = r["payload"].get("child_keys")
            if ck:
                keys = sorted(ck)
        check("the sub-claims are keyed by the picked folder's matching files",
              keys == want, json.dumps(keys))
        check("the non-matching file in the picked folder was not fanned out",
              "notes.md" not in json.dumps(keys), json.dumps(keys))
        check("no file of the folder that was not picked was fanned out",
              not [n for n in other if n in json.dumps(tl)], json.dumps(other))

        node = [n for n in nodes(iid) if n["node_type"] == "per-file"][0]
        check("the parent and one clone per file all settled fresh",
              node["run_summary"]["fresh_count"] == len(want) + 1
              and node["run_summary"]["failed_count"] == 0,
              json.dumps(node["run_summary"]))

        seen = get("http://127.0.0.1:%d/seen" % port)
        check("one work unit ran per file, each addressed to its own file",
              seen["paths"] == sorted("/file/" + n for n in want), json.dumps(seen["paths"]))
        check("the work units ran at the same time rather than one after another",
              seen["peak"] == len(want), json.dumps(seen))
        finish()
    finally:
        server.kill()


try:
    main()
finally:
    from harness import teardown
    teardown()
