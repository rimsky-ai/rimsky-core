import json
import os

from harness import (boot, check, deploy, finish, new_instance, quiet, register,
                     send_message, show, tmpdir, nodes)

FS_CONFIG = """root: /workspace/absent
host: 127.0.0.1
grpc_port: 9200
http_port: 9210
sweep_interval_seconds: 60
"""

PRODUCER_CLASS = "fs/root_unavailable"
FAMILY_CLASS = "acquire/producer_error"


def spec(name, error_types):
    return {
        "name": name,
        "version": "1",
        "nodes": [{
            "type": "worker",
            "kind": "attribute_passthrough",
            "claim_producers": [{
                "name": "claim-producer-filesystem",
                "selector": "data",
                "intent": "rw",
                "alias": "ds",
            }],
            "error_types": error_types,
            "attributes": {"schema": {"type": "object", "properties": {
                "v": {"type": "integer", "default": 1}}}},
        }],
    }


def run(name, error_types):
    iid = new_instance(deploy(spec(name, error_types)))
    send_message(iid)
    tl = quiet(iid)
    show(tl)
    node = [n for n in nodes(iid) if n["node_type"] == "worker"][0]
    classes = [r["payload"].get("error_class") for r in tl if r["kind"].startswith("terminal/error")]
    return node, tl, classes


def main():
    work = tmpdir()
    os.makedirs(os.path.join(work, "present"))
    cfg = os.path.join(work, "fs.yml")
    with open(cfg, "w") as fh:
        fh.write(FS_CONFIG)
    boot(env={"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/etc/rimsky/fs.yml"},
         mounts=[(cfg, "/etc/rimsky/fs.yml"), (os.path.join(work, "present"), "/workspace/present")])

    print("--- the producer classifies the acquisition failure it is asked to route")
    node, tl, classes = run("exp-class-routing-specific-pass", {
        "acquire/unavailable": {"action": "give_up"},
        FAMILY_CLASS: {"action": "give_up"},
        PRODUCER_CLASS: {"action": "pass"}})
    check("the acquisition failure carries the producer's own error class",
          classes and classes[0] == PRODUCER_CLASS, json.dumps(classes))
    check("the policy keyed on the producer's class is the one that ran",
          node["run_summary"]["fresh_count"] == 1 and node["run_summary"]["failed_count"] == 0,
          json.dumps(node["run_summary"]))

    print("--- the same failure under the generic acquire-family key alone")
    node, tl, classes = run("exp-class-routing-family-pass", {
        "acquire/unavailable": {"action": "give_up"},
        FAMILY_CLASS: {"action": "pass"}})
    check("with no producer-class entry the generic acquire-family key routes the same failure",
          node["run_summary"]["fresh_count"] == 1 and node["run_summary"]["failed_count"] == 0,
          json.dumps(node["run_summary"]))
    check("the emitted signal still carries the most specific class",
          classes and classes[0] == PRODUCER_CLASS, json.dumps(classes))

    print("--- the producer class outranks the generic key when both are declared")
    node, tl, classes = run("exp-class-routing-specific-wins", {
        "acquire/unavailable": {"action": "pass"},
        FAMILY_CLASS: {"action": "pass"},
        PRODUCER_CLASS: {"action": "give_up"}})
    check("the producer-class entry decides, not the generic key sitting beside it",
          node["run_summary"]["failed_count"] == 1 and node["run_summary"]["fresh_count"] == 0,
          json.dumps(node["run_summary"]))

    print("--- a template routing the producer's class registers without a vocabulary warning")
    status, out = register(spec("exp-class-routing-vocabulary", {
        "acquire/unavailable": {"action": "give_up"},
        PRODUCER_CLASS: {"action": "pass"}}))
    warnings = json.dumps((out or {}).get("validation_warnings") or [])
    check("the producer's declared class is in the vocabulary the validator range-checks against",
          status in (200, 201) and PRODUCER_CLASS not in warnings,
          "%s %s" % (status, warnings[:200]))

    print("--- an undeclared class is accepted but flagged")
    status, out = register(spec("exp-class-routing-undeclared", {
        "acquire/unavailable": {"action": "give_up"},
        "fs/not_a_declared_class": {"action": "pass"}}))
    warnings = json.dumps((out or {}).get("validation_warnings") or [])
    check("a class no peer declares registers with a warning naming it",
          status in (200, 201) and "fs/not_a_declared_class" in warnings,
          "%s %s" % (status, warnings[:300]))
    finish()


try:
    main()
finally:
    from harness import teardown
    teardown()
