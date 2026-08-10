import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import (boot, call, check, counter_node, deltas, deploy, finish,  # noqa: E402
                     live_runs, new_instance, passthrough_node, quiet, send_message,
                     show, starts, sub, teardown, timeline, wait_hits, wait_until)

SPEC = {
    "name": "exp-operator-invalidate-inflight",
    "version": "1",
    "nodes": [
        counter_node("emitter"),
        passthrough_node("worker",
                         [sub("emitter", "terminal/success"),
                          sub("emitter", "attribute/count/changed")],
                         {"seen": {"type": "integer", "source": "{{nodes.emitter.attribute.count}}"}}),
    ],
}


def main():
    boot()
    iid = new_instance(deploy(SPEC))
    bp = call("POST", "/v1/instances/%s/breakpoints" % iid,
              {"matcher": {"node_type": "worker"}, "checkpoint": "before_dispatch",
               "mode": "pause", "hit_ttl_seconds": 3600})[1]["breakpoint_id"]
    send_message(iid)
    hits = wait_hits(iid, 1)
    in_flight = hits[0]["node_run_id"]
    check("the worker has one run in flight when the operator acts",
          [(r["id"], r["state"]) for r in live_runs(iid)] == [(in_flight, "running")],
          json.dumps([(r["id"], r["state"]) for r in live_runs(iid)]))

    status, out = call("POST", "/v1/instances/%s/debug/override" % iid,
                       {"action": "invalidate_node", "node_type": "worker"})
    check("the operator's invalidate of the in-flight node is accepted",
          status == 200 and out.get("runs_mutated") == 1, "%s %s" % (status, json.dumps(out)))

    queued = wait_until(lambda: [r for r in live_runs(iid) if r["id"] != in_flight] or None)
    check("the invalidate produced a queued run rather than being dropped",
          len(queued) == 1 and queued[0]["state"] in ("pending", "stale"),
          json.dumps([(r["id"], r["state"]) for r in live_runs(iid)]))
    held = [r for r in live_runs(iid) if r["id"] == in_flight]
    check("the run already in flight was left alone, not cancelled or restarted",
          len(held) == 1 and held[0]["state"] == "running",
          json.dumps([(r["id"], r["state"]) for r in held]))
    check("nothing has dispatched the queued run while the first is still in flight",
          len(starts(timeline(iid), "worker")) == 1,
          json.dumps([r["seq"] for r in starts(timeline(iid), "worker")]))

    call("POST", "/v1/instances/%s/breakpoints/%s/resume" % (iid, bp), {"hit_id": hits[0]["hit_id"]})
    hits = wait_hits(iid, 2)
    check("the queued run dispatches only after the in-flight one settles",
          hits[1]["node_run_id"] == queued[0]["id"],
          "in-flight %s, queued %s, second hit %s"
          % (in_flight, queued[0]["id"], hits[1]["node_run_id"]))
    call("POST", "/v1/instances/%s/breakpoints/%s/resume" % (iid, bp), {"hit_id": hits[1]["hit_id"]})

    tl = quiet(iid)
    show(tl)
    worker_starts = starts(tl, "worker")
    first_completed = [r for r in tl if r["node"] == "worker" and r["kind"] == "work_completed"][0]
    check("the work already in flight ran to completion — the operator's action was not destructive",
          worker_starts[0]["seq"] < first_completed["seq"] < worker_starts[1]["seq"],
          "start=%s complete=%s next start=%s"
          % (worker_starts[0]["seq"], first_completed["seq"], worker_starts[1]["seq"]))
    worker_deltas = deltas(tl, "worker")
    check("both the in-flight run and the operator's re-run reached success",
          len(worker_deltas) == 2 and all(d == {"seen": 1} for d in worker_deltas),
          json.dumps(worker_deltas))
    finish()


try:
    main()
finally:
    teardown()
