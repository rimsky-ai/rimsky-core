import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import (boot, call, check, counter_node, deltas, deploy, finish,  # noqa: E402
                     live_runs, new_instance, new_network, node_types, passthrough_node,
                     send_message, show, start_endpoint, sub, teardown, timeline,
                     wait_until)

SUBNET = "172.31.91.0/24"
ENDPOINT_SOURCE = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                               "rate-limited-endpoint.py")


def spec(endpoint_host):
    return {
        "name": "exp-debug-channel-paused",
        "version": "1",
        "nodes": [
            counter_node("emitter"),
            passthrough_node("knob", [sub("emitter", "terminal/success")],
                             {"tick": {"type": "integer", "default": 7}}),
            {"type": "worker", "executor": "http-node",
             "subscribes": [sub("emitter", "terminal/success"),
                            sub("emitter", "attribute/count/changed")],
             "attributes": {"schema": {"type": "object", "properties": {
                 "url": {"type": "string", "default": "http://%s:8000/work" % endpoint_host},
                 "method": {"type": "string", "default": "POST"},
                 "seen": {"type": "integer", "source": "{{nodes.emitter.attribute.count}}"}}}}},
        ],
    }


def override(iid, body):
    return call("POST", "/v1/instances/%s/debug/override" % iid, body)


def dispatches(iid, node_type):
    return [r for r in timeline(iid) if r["node"] == node_type and r["kind"] == "work_started"]


def main():
    network = new_network(SUBNET)
    endpoint, _ = start_endpoint(network, ENDPOINT_SOURCE)
    boot(env={"RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST": SUBNET}, network=network)
    iid = new_instance(deploy(spec(endpoint)))
    send_message(iid)
    wait_until(lambda: [r for r in live_runs(iid) if r["state"] == "parked"])
    wait_until(lambda: len(deltas(timeline(iid), "knob")) >= 1)
    worker = [nid for nid, t in node_types(iid).items() if t == "worker"][0]
    check("the instance is mid-frame with a node in flight and no breakpoint installed",
          call("GET", "/v1/instances/%s/breakpoints" % iid)[1]["breakpoints"] == [],
          json.dumps([(r["id"], r["state"]) for r in live_runs(iid)]))

    status, out = override(iid, {"action": "invalidate_node", "node_type": "knob"})
    check("the debug channel is shut on an instance that has not entered debug mode",
          status == 409 and sorted(out.get("states") or []) == ["breakpoint", "paused"],
          "%s %s" % (status, json.dumps(out)))
    status, out = override(iid, {"action": "set_attribute", "node_type": "worker",
                                 "attribute_key": "injected", "attribute_value": "denied"})
    check("an attribute-value override is refused on the same shut channel",
          status == 409, "%s %s" % (status, json.dumps(out)))
    attrs = call("GET", "/v1/nodes/%s" % worker)[1]["latest_attributes"]
    check("the refused override left the node's attribute values untouched",
          "injected" not in attrs, json.dumps(attrs))

    call("POST", "/v1/instances/%s/pause" % iid, {})
    status, out = override(iid, {"action": "set_attribute", "node_type": "worker",
                                 "attribute_key": "injected",
                                 "attribute_value": "set-by-operator"})
    check("entering pause opens the channel to an attribute-value override",
          status == 200 and out.get("gate_state") == "paused" and out.get("runs_mutated") >= 1,
          "%s %s" % (status, json.dumps(out)))
    attrs = call("GET", "/v1/nodes/%s" % worker)[1]["latest_attributes"]
    check("the overridden value is what the node's attributes now read back as",
          attrs.get("injected") == "set-by-operator", json.dumps(attrs))

    before = len(dispatches(iid, "knob"))
    status, out = override(iid, {"action": "invalidate_node", "node_type": "knob"})
    check("the same open channel accepts an override-invalidate of a named node",
          status == 200 and out.get("gate_state") == "paused" and out.get("runs_mutated") == 1,
          "%s %s" % (status, json.dumps(out)))
    check("no work ran while the instance was still paused",
          len(dispatches(iid, "knob")) == before, str(before))

    call("POST", "/v1/instances/%s/resume" % iid, {})
    wait_until(lambda: len(dispatches(iid, "knob")) > before)
    check("the override-invalidated node ran again once the instance resumed",
          len(dispatches(iid, "knob")) == before + 1,
          "%d dispatches before, %d after" % (before, len(dispatches(iid, "knob"))))

    show(timeline(iid))
    finish()


try:
    main()
finally:
    teardown()
