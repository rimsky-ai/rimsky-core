import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import (boot, check, counter_node, deltas, deploy, endpoint_log,  # noqa: E402
                     finish, live_runs, new_instance, new_network, quiet, send_message,
                     show, start_endpoint, starts, sub, teardown, timeline, wait_until)

SUBNET = "172.31.98.0/24"
ENDPOINT_SOURCE = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                               "rate-limited-endpoint.py")


def spec(endpoint_host):
    return {
        "name": "exp-resume-timer",
        "version": "1",
        "nodes": [
            counter_node("emitter"),
            {"type": "worker", "executor": "http-node",
             "subscribes": [sub("emitter", "terminal/success"),
                            sub("emitter", "attribute/count/changed")],
             "attributes": {"schema": {"type": "object", "properties": {
                 "url": {"type": "string", "default": "http://%s:8000/work" % endpoint_host},
                 "method": {"type": "string", "default": "POST"},
                 "seen": {"type": "integer",
                          "source": "{{nodes.emitter.attribute.count}}"}}}}},
        ],
    }


def work_requests(log_base):
    return [r for r in endpoint_log(log_base) if r["path"].startswith("/work")]


def main():
    network = new_network(SUBNET)
    endpoint, log_base = start_endpoint(network, ENDPOINT_SOURCE, env={"RETRY_AFTER": "2"})
    boot(env={"RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST": SUBNET}, network=network)
    iid = new_instance(deploy(spec(endpoint)))

    send_message(iid)
    wait_until(lambda: [r for r in live_runs(iid) if r["state"] == "parked"])
    parked_run = [r for r in live_runs(iid) if r["state"] == "parked"][0]["id"]
    reqs = wait_until(lambda: work_requests(log_base) or None)
    check("the executor was dispatched with the upstream value of its own moment",
          reqs[0]["body"] == {"seen": 1}, json.dumps(reqs[0]))

    wake = wait_until(lambda: [r for r in timeline(iid) if r["kind"] == "parked_resume_started"])
    check("nothing upstream moved, so the park resumed on its own retry schedule",
          wake[0]["payload"].get("resume_reason") != "upstream_cascade",
          json.dumps(wake[0]["payload"]))

    tl = quiet(iid)
    show(tl)
    reqs = work_requests(log_base)
    check("the resumed executor saw the same substituted values it parked with",
          len(reqs) == 2 and reqs[1]["body"] == reqs[0]["body"],
          json.dumps([r["body"] for r in reqs]))
    resumed_dispatch = starts(tl, "worker")[1]["payload"]["dispatch_id"] \
        if len(starts(tl, "worker")) > 1 else None
    check("the work that ran after the park is the parked unit of work continuing",
          resumed_dispatch == parked_run,
          "parked run %s, run that executed after the wake %s" % (parked_run, resumed_dispatch))
    check("the park and its resume were one unit of work, settling once",
          len(starts(tl, "worker")) == 2 and len(deltas(tl, "worker")) == 1,
          "%d dispatches, %d terminals" % (len(starts(tl, "worker")), len(deltas(tl, "worker"))))
    finish()


try:
    main()
finally:
    teardown()
