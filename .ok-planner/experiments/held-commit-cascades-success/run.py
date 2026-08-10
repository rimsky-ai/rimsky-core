import json
import os
import subprocess
import sys
import urllib.request

from harness import (STATE, boot, call, check, deploy, finish, free_port, live_runs,
                     new_instance, quiet, send_message, show, timeline, tmpdir, wait_until)

FS_CONFIG = """root: /workspace
host: 127.0.0.1
grpc_port: 9200
http_port: 9210
sweep_interval_seconds: 60
"""


def sub(node_type, signal):
    return {"node": node_type, "type": signal, "force_upstream_refresh": False}


def spec(gate_url):
    return {
        "name": "exp-held-commit-cascades-success",
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
                "attributes": {"schema": {"type": "object", "properties": {
                    "url": {"type": "string", "default": gate_url}}}},
            },
            {
                "type": "watcher",
                "kind": "attribute_passthrough",
                "subscribes": [sub("acquirer", "terminal/success")],
                "attributes": {"schema": {"type": "object", "properties": {
                    "v": {"type": "integer", "default": 1}}}},
            },
        ],
    }


def get(url):
    with urllib.request.urlopen(url) as resp:
        return json.loads(resp.read().decode())


def post(url):
    req = urllib.request.Request(url, data=b"{}", headers={"Content-Type": "application/json"},
                                 method="POST")
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read().decode())


def runs_of(iid, node_type):
    types = {n["id"]: n["node_type"] for n in call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]}
    return [r for r in live_runs(iid) if types.get(r["node_id"]) == node_type]


def main():
    here = os.path.dirname(os.path.abspath(__file__))
    work = tmpdir()
    os.makedirs(os.path.join(work, "data"))
    cfg = os.path.join(work, "fs.yml")
    with open(cfg, "w") as fh:
        fh.write(FS_CONFIG)

    port = free_port()
    gate = subprocess.Popen([sys.executable, os.path.join(here, "gate_server.py"), str(port)])
    try:
        while True:
            try:
                get("http://127.0.0.1:%d/arrived" % port)
                break
            except Exception:
                pass
        boot(env={"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/etc/rimsky/fs.yml"},
             mounts=[(cfg, "/etc/rimsky/fs.yml"), (os.path.join(work, "data"), "/workspace/data")])

        gate_url = "http://host.docker.internal:%d/hold" % port
        iid = new_instance(deploy(spec(gate_url)))
        send_message(iid)

        print("--- while the held work is in flight the acquirer is held and no success has reached the watcher")
        wait_until(lambda: get("http://127.0.0.1:%d/arrived" % port)["arrived"])
        acquirer_runs = wait_until(lambda: runs_of(iid, "acquirer"))
        check("the acquirer's run is held while its co-holder's work is in flight",
              [r["state"] for r in acquirer_runs] == ["held"],
              json.dumps([r["state"] for r in acquirer_runs]))
        tl_mid = timeline(iid)
        check("the acquirer has emitted no success signal yet",
              not [r for r in tl_mid if r["node"] == "acquirer" and r["kind"] == "terminal/success"],
              json.dumps([r["kind"] for r in tl_mid if r["node"] == "acquirer"]))
        check("the non-member watcher has no run at the provisional held moment",
              runs_of(iid, "watcher") == [],
              json.dumps([(r["state"]) for r in runs_of(iid, "watcher")]))

        print("--- releasing the held work commits the claim and the success reaches the watcher")
        post("http://127.0.0.1:%d/release" % port)
        tl = quiet(iid)
        show(tl)
        acquirer_success = [r for r in tl if r["node"] == "acquirer" and r["kind"] == "terminal/success"]
        check("the acquirer emits its success signal once the held work has committed",
              len(acquirer_success) == 1, json.dumps([r["seq"] for r in acquirer_success]))
        commits = [r for r in tl if r["kind"] == "claim_resolution.commit"]
        check("the held claim resolved with a commit", len(commits) == 1,
              json.dumps([r["kind"] for r in tl if "claim_resolution" in r["kind"]]))
        check("the success signal follows the commit in the event log",
              commits and acquirer_success and acquirer_success[0]["seq"] > commits[0]["seq"],
              "commit seq=%s success seq=%s" % (commits[0]["seq"] if commits else None,
                                                acquirer_success[0]["seq"] if acquirer_success else None))
        watcher_start = [r for r in tl if r["node"] == "watcher" and r["kind"] == "work_started"]
        check("the watcher ran, and only after the commit",
              len(watcher_start) == 1 and commits and watcher_start[0]["seq"] > commits[0]["seq"],
              json.dumps([(r["seq"], r["kind"]) for r in tl if r["node"] == "watcher"]))
        finish()
    finally:
        gate.kill()


try:
    main()
finally:
    from harness import teardown
    teardown()
