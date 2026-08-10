import json
import os
import re
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import (boot, check, counter_node, deltas, deploy, endpoint_log,  # noqa: E402
                     finish, live_runs, new_instance, new_network, send_message, show,
                     start_endpoint, sub, teardown, timeline, wait_until)

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
SUBNET = "172.31.97.0/24"
ENDPOINT_SOURCE = os.path.join(HERE, "rate-limited-endpoint.py")

RUNNABLE_SUFFIXES = (".sh", ".py", ".md", ".yml", ".yaml")
PARK_WORDS = re.compile(r"\bpark", re.IGNORECASE)


def spec(endpoint_host):
    return {
        "name": "exp-bundled-park-resume",
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


def shipped_files():
    res = subprocess.run(["git", "-C", ROOT, "ls-files"], capture_output=True, text=True)
    return [p for p in res.stdout.splitlines()
            if p.endswith(RUNNABLE_SUFFIXES) and not p.startswith(".ok-planner/")]


def park_recipes(paths):
    hits = []
    for rel in paths:
        full = os.path.join(ROOT, rel)
        try:
            with open(full, "r", errors="replace") as fh:
                text = fh.read()
        except OSError:
            continue
        if PARK_WORDS.search(text) and "resume" in text.lower() \
                and ("rimsky " in text or "curl " in text) \
                and rel.endswith((".sh", ".py")):
            hits.append(rel)
    return hits


def main():
    paths = shipped_files()
    recipes = park_recipes(paths)
    check("the tree ships a self-contained, copy-runnable park-then-resume recipe",
          recipes != [],
          "searched %d committed runnable files outside the planner estate; found %s"
          % (len(paths), json.dumps(recipes)))
    readme = open(os.path.join(ROOT, "README.md"), errors="replace").read()
    check("the README points an evaluator at a park-then-resume recipe to run",
          bool(re.search(r"park.{0,80}(demo|recipe|walkthrough)", readme, re.IGNORECASE)),
          "no park recipe named in the README")

    network = new_network(SUBNET)
    endpoint, log_base = start_endpoint(network, ENDPOINT_SOURCE, env={"RETRY_AFTER": "2"})
    boot(env={"RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST": SUBNET}, network=network)
    iid = new_instance(deploy(spec(endpoint)))
    send_message(iid)

    wait_until(lambda: [r for r in live_runs(iid) if r["state"] == "parked"])
    park = [r for r in timeline(iid) if r["kind"] == "transient/park"][0]
    check("the bundled http-node executor parks a node on a real rate-limited answer",
          park["payload"]["tags"] == ["rate_limited"], json.dumps(park["payload"]))

    wait_until(lambda: [r for r in timeline(iid) if r["kind"] == "parked_resume_started"])
    tl = wait_until(lambda: timeline(iid)
                    if [d for d in deltas(timeline(iid), "worker") if d] else None)
    show(tl)
    check("the parked node resumes on its own retry schedule and completes",
          any(r["node"] == "worker" and r["kind"] == "terminal/success" for r in tl),
          json.dumps(deltas(tl, "worker")))
    reqs = [r for r in endpoint_log(log_base) if r["path"].startswith("/work")]
    check("the resumed work reached the upstream a second time",
          len(reqs) == 2, json.dumps([r["n"] for r in reqs]))
    finish()


try:
    main()
finally:
    teardown()
