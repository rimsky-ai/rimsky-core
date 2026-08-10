import json
import os

from harness import (STATE, boot, call, check, deltas, die, finish, new_instance, quiet,
                     register, deploy, send_message, show, tmpdir)

FS_CONFIG = """root: /workspace
host: 127.0.0.1
grpc_port: 9200
http_port: 9210
sweep_interval_seconds: 60
"""


def spec(directive, name, selector="data/inbox"):
    return {
        "name": name,
        "version": "1",
        "nodes": [{
            "type": "reader",
            "kind": "attribute_passthrough",
            "claim_producers": [{
                "name": "claim-producer-filesystem",
                "selector": selector,
                "intent": "rw",
                "alias": "ds",
            }],
            "error_types": {"acquire/unavailable": {"action": "give_up"}},
            "attributes": {"schema": {"type": "object", "properties": {
                "seen_scope": {"type": "string", "source": directive}}}},
        }],
    }


def main():
    work = tmpdir()
    os.makedirs(os.path.join(work, "data", "inbox"))
    cfg = os.path.join(work, "fs.yml")
    with open(cfg, "w") as fh:
        fh.write(FS_CONFIG)
    boot(env={"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/etc/rimsky/fs.yml"},
         mounts=[(cfg, "/etc/rimsky/fs.yml"), (os.path.join(work, "data"), "/workspace/data")])

    print("--- the canonical spelling resolves to the live claim's claim-scope bytes")
    iid = new_instance(deploy(spec("{{claim.ds.claim_scope}}", "exp-claim-scope-canonical")))
    send_message(iid)
    tl = quiet(iid)
    show(tl)
    got = deltas(tl, "reader")
    check("the node dispatched and settled with the canonical directive resolved",
          got == [{"seen_scope": "data/inbox"}],
          json.dumps(got))

    handles = call("GET", "/v1/observability/claim-handles?instance_id=%s" % iid)[1]["claim_handles"]
    check("the resolved value is the scope the ledger recorded for the live claim",
          [h["claim_scope_data"] for h in handles] == ["data/inbox"],
          json.dumps([h["claim_scope_data"] for h in handles]))

    print("--- with a non-canonical selector the value follows the claim, not the template text")
    iid2 = new_instance(deploy(spec("{{claim.ds.claim_scope}}", "exp-claim-scope-noncanonical",
                                    selector="./data/inbox/")))
    send_message(iid2)
    tl2 = quiet(iid2)
    got2 = deltas(tl2, "reader")
    check("the directive resolves to the producer's canonical claim-scope bytes, not the selector text",
          got2 == [{"seen_scope": "data/inbox"}], json.dumps(got2))

    print("--- the abbreviated spelling is refused at registration")
    status, out = register(spec("{{claim.ds.scope}}", "exp-claim-scope-abbrev"))
    print("    register -> %s %s" % (status, json.dumps(out)[:400]))
    check("registering the abbreviated spelling is rejected", status >= 400, str(status))
    body = json.dumps(out)
    check("the rejection names the directive and the spellings it admits",
          "claim.ds.scope" in body and "claim_scope" in body, body[:300])

    print("--- the same template with the canonical spelling registers")
    status, _ = register(spec("{{claim.ds.claim_scope}}", "exp-claim-scope-canonical-again"))
    check("the canonical spelling registers on the same template shape", status in (200, 201), str(status))
    finish()


try:
    main()
finally:
    from harness import teardown
    teardown()
