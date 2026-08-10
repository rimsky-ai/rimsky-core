import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import (boot, call, check, counter_node, deploy, finish, live_runs,  # noqa: E402
                     new_instance, node_types, passthrough_node, quiet, send_message,
                     show, sub, teardown, timeline, wait_hits, wait_until)

SPEC = {
    "name": "exp-debug-channel-breakpoint",
    "version": "1",
    "nodes": [
        counter_node("emitter"),
        passthrough_node("knob", [sub("emitter", "terminal/success")],
                         {"tick": {"type": "integer", "default": 7}}),
        passthrough_node("worker",
                         [sub("emitter", "terminal/success"),
                          sub("emitter", "attribute/count/changed")],
                         {"seen": {"type": "integer", "source": "{{nodes.emitter.attribute.count}}"}}),
    ],
}


def override(iid, body):
    return call("POST", "/v1/instances/%s/debug/override" % iid, body)


def dispatches(iid, node_type):
    return [r for r in timeline(iid) if r["node"] == node_type and r["kind"] == "work_started"]


def main():
    boot()
    iid = new_instance(deploy(SPEC))
    bp = call("POST", "/v1/instances/%s/breakpoints" % iid,
              {"matcher": {"node_type": "worker"}, "checkpoint": "before_dispatch",
               "mode": "pause", "hit_ttl_seconds": 3600})[1]["breakpoint_id"]
    send_message(iid)
    hits = wait_hits(iid, 1)
    worker = [nid for nid, t in node_types(iid).items() if t == "worker"][0]
    check("the instance sits at an unresumed pause-mode breakpoint hit and is not paused",
          call("GET", "/v1/instances/%s" % iid)[1]["paused"] is False
          and hits[0]["mode"] == "pause",
          json.dumps({"paused": call("GET", "/v1/instances/%s" % iid)[1]["paused"],
                      "mode": hits[0]["mode"]}))

    status, out = override(iid, {"action": "set_attribute", "node_type": "worker",
                                 "attribute_key": "injected",
                                 "attribute_value": "set-at-breakpoint"})
    check("the breakpoint hit is itself an entry into debug mode for attribute override",
          status == 200 and out.get("gate_state") == "breakpoint" and out.get("runs_mutated") >= 1,
          "%s %s" % (status, json.dumps(out)))
    attrs = call("GET", "/v1/nodes/%s" % worker)[1]["latest_attributes"]
    check("the overridden value reads back off the node under inspection",
          attrs.get("injected") == "set-at-breakpoint", json.dumps(attrs))

    before = len(dispatches(iid, "knob"))
    status, out = override(iid, {"action": "invalidate_node", "node_type": "knob"})
    check("override-invalidate is open at the same hit",
          status == 200 and out.get("gate_state") == "breakpoint" and out.get("runs_mutated") == 1,
          "%s %s" % (status, json.dumps(out)))

    call("POST", "/v1/instances/%s/breakpoints/%s/resume" % (iid, bp), {"hit_id": hits[0]["hit_id"]})
    wait_until(lambda: len(dispatches(iid, "knob")) > before)
    check("the override-invalidated node ran again after the hit was released",
          len(dispatches(iid, "knob")) == before + 1,
          "%d dispatches before, %d after" % (before, len(dispatches(iid, "knob"))))

    for h in wait_hits(iid, 2)[1:]:
        call("POST", "/v1/instances/%s/breakpoints/%s/resume" % (iid, bp), {"hit_id": h["hit_id"]})
    call("DELETE", "/v1/instances/%s/breakpoints/%s" % (iid, bp))
    tl = quiet(iid)
    show(tl)
    check("the instance is out of debug mode once the hit is released and it is not paused",
          call("GET", "/v1/instances/%s" % iid)[1]["paused"] is False and not live_runs(iid),
          json.dumps([(r["id"], r["state"]) for r in live_runs(iid)]))
    status, out = override(iid, {"action": "invalidate_node", "node_type": "knob"})
    check("the channel is shut again the moment debug mode ends",
          status == 409 and sorted(out.get("states") or []) == ["breakpoint", "paused"],
          "%s %s" % (status, json.dumps(out)))
    finish()


try:
    main()
finally:
    teardown()
