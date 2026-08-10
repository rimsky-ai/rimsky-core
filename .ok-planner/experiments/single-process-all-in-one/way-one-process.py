import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import (boot, call, check, deploy, docker, finish, logs,  # noqa: E402
                     new_instance, node_types, passthrough_node, quiet, send_message,
                     show, teardown, wait_until)

SPEC = {
    "name": "exp-single-process-all-in-one",
    "version": "1",
    "nodes": [passthrough_node("worker", None, {"v": {"type": "integer", "default": 3}})],
}

ROLE_BINARIES = ("rimsky-scheduler", "rimsky-supervisor", "rimsky-control-api", "rimsky-migrate")


def process_table(container):
    res = docker("top", container)
    return [line for line in res.stdout.splitlines()[1:] if line.strip()]


def main():
    container, base = boot()
    table = process_table(container)
    rimsky_procs = [line for line in table if "rimsky" in line]
    check("the all-in-one container runs exactly one rimsky process",
          len(rimsky_procs) == 1, "\n".join(table))
    check("that one process is the multi-role entrypoint itself",
          rimsky_procs and "rimsky-entrypoint" in rimsky_procs[0], "\n".join(rimsky_procs))
    children = [line for line in rimsky_procs if any(b in line for b in ROLE_BINARIES)]
    check("no per-role child process was spawned beside it",
          children == [], "\n".join(children))
    selected = [ln for ln in logs(container).splitlines() if "selected roles" in ln]
    check("the entrypoint reports it took all three roles",
          len(selected) == 1
          and all(r in selected[0]
                  for r in ("rimsky-scheduler", "rimsky-supervisor", "rimsky-control-api")),
          selected[0] if selected else "(no selected-roles line)")

    health = call("GET", "/v1/health")[1]
    check("the control-api role is serving out of that process",
          health.get("status") == "ok", json.dumps(health))
    check("the supervisor role is registered out of that same process",
          len(health.get("supervisors") or []) == 1, json.dumps(health.get("supervisors")))

    iid = new_instance(deploy(SPEC))
    send_message(iid)
    tl = quiet(iid)
    show(tl)
    worker = [nid for nid, t in node_types(iid).items() if t == "worker"][0]
    check("the scheduler role dispatched work from that same process, and it settled",
          call("GET", "/v1/nodes/%s" % worker)[1]["run_summary"]["fresh_count"] == 1,
          json.dumps(call("GET", "/v1/nodes/%s" % worker)[1]["run_summary"]))
    check("the process count did not change once the roles had all done work",
          len([line for line in process_table(container) if "rimsky" in line]) == 1,
          "\n".join(process_table(container)))
    finish()


try:
    main()
finally:
    teardown()
