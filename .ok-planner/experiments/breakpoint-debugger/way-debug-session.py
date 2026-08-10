import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import (boot, call, check, counter_node, deltas, deploy, finish,  # noqa: E402
                     new_instance, node_types, passthrough_node, quiet, send_message, show,
                     sub, teardown, wait_hits, wait_until)

SPEC = {
    "name": "exp-breakpoint-debugger",
    "version": "1",
    "nodes": [
        counter_node("emitter"),
        passthrough_node("worker",
                         [sub("emitter", "terminal/success"), sub("emitter", "attribute/count/changed")],
                         {"seen": {"type": "integer", "source": "{{nodes.emitter.attribute.count}}"},
                          "note": {"type": "string", "default": "unmodified"}}),
    ],
}


def hit_events(iid):
    return call("GET", "/v1/events?instance_id=%s&kind=breakpoint.hit" % iid)[1]["events"]


def main():
    boot()
    iid = new_instance(deploy(SPEC))

    status, bp = call("POST", "/v1/instances/%s/breakpoints" % iid,
                      {"matcher": {"node_type": "worker"}, "checkpoint": "before_dispatch",
                       "mode": "pause", "hit_ttl_seconds": 3600})
    check("a breakpoint installs on a live instance's before-dispatch checkpoint",
          status == 201 and bp.get("checkpoint") == "before_dispatch" and bp.get("mode") == "pause",
          json.dumps(bp))
    bp_id = bp["breakpoint_id"]
    listed = call("GET", "/v1/instances/%s/breakpoints" % iid)[1]["breakpoints"]
    check("the installed breakpoint is readable back off the instance",
          [b["breakpoint_id"] for b in listed] == [bp_id], json.dumps(listed))

    send_message(iid)
    hits = wait_hits(iid, 1)
    worker_node_id = [nid for nid, t in node_types(iid).items() if t == "worker"][0]
    check("the hit appears on the breakpoint-hits ledger, against the worker's node",
          hits[0]["breakpoint_id"] == bp_id
          and hits[0]["checkpoint"] == "before_dispatch"
          and hits[0]["node_run"]["node_id"] == worker_node_id,
          json.dumps({k: hits[0].get(k)
                      for k in ("hit_id", "breakpoint_id", "checkpoint", "node_run")}))
    check("the hit snapshot exposes the sealed dispatch bag",
          hits[0]["dispatch_context"]["merged_attributes"] == {"seen": 1, "note": "unmodified"},
          json.dumps(hits[0]["dispatch_context"]["merged_attributes"]))

    evs = wait_until(lambda: hit_events(iid))
    check("the same hit appears on the unified event log",
          len(evs) == 1 and evs[0]["payload"].get("breakpoint_id") == bp_id,
          json.dumps(evs[0]["payload"]))

    running = call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]
    check("the paused hit is holding the worker's run in flight",
          any(r["id"] == hits[0]["node_run_id"] and r["state"] == "running" for r in running),
          json.dumps([(r["id"], r["state"]) for r in running]))

    status, res = call("POST", "/v1/instances/%s/breakpoints/%s/resume" % (iid, bp_id),
                       {"hit_id": hits[0]["hit_id"], "overlay": {"note": "overlaid-by-operator"}})
    check("the paused hit resumes with an attribute overlay",
          status == 200 and res.get("resumed") is True and res.get("first_resume") is True,
          json.dumps(res))

    tl = quiet(iid)
    show(tl)
    worker_deltas = deltas(tl, "worker")
    check("the re-fired dispatch carries the overlay the operator supplied",
          worker_deltas and worker_deltas[0].get("note") == "overlaid-by-operator",
          json.dumps(worker_deltas))
    check("the overlay changed only what it named",
          worker_deltas and worker_deltas[0].get("seen") == 1, json.dumps(worker_deltas))

    ledger = call("GET", "/v1/instances/%s/breakpoint-hits" % iid)[1]["hits"]
    check("the ledger still holds the hit before the breakpoint is deleted",
          len(ledger) == 1, json.dumps([h["hit_id"] for h in ledger]))

    status, _ = call("DELETE", "/v1/instances/%s/breakpoints/%s" % (iid, bp_id))
    check("deleting the breakpoint answers no-content", status == 204, str(status))
    listed = call("GET", "/v1/instances/%s/breakpoints" % iid)[1]["breakpoints"]
    check("the deleted breakpoint is gone from the instance", listed == [], json.dumps(listed))
    ledger = call("GET", "/v1/instances/%s/breakpoint-hits" % iid)[1]["hits"]
    check("deleting the breakpoint cascade-cleared its hits",
          ledger == [], json.dumps([h["hit_id"] for h in ledger]))
    evs = hit_events(iid)
    check("the event-log record of the hit outlives the deleted breakpoint",
          len(evs) == 1, json.dumps([e["kind"] for e in evs]))
    finish()


try:
    main()
finally:
    teardown()
