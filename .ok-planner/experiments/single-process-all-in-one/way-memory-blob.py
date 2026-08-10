import json
import os
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import (RIMSKY, boot, call, check, deploy, finish, new_instance,  # noqa: E402
                     node_types, passthrough_node, quiet, require_image, run_container,
                     send_message, show, teardown)

SPILL_THRESHOLD = 256
PAYLOAD = "rimsky-memory-blob-roundtrip/" * 300

CONFIG = """persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
  blob:
    backend: memory
    spill_threshold_bytes: %d
claim_producers: {}
named_locks: {}
executors: {}
""" % SPILL_THRESHOLD

SPEC = {
    "name": "exp-memory-blob",
    "version": "1",
    "nodes": [passthrough_node("worker", None, {"payload": {"type": "string", "default": PAYLOAD}})],
}


def write_config():
    path = os.path.join(tempfile.mkdtemp(), "rimsky.yml")
    with open(path, "w") as fh:
        fh.write(CONFIG)
    return path


def main():
    config = write_config()
    check("the payload under test is far past the configured spill threshold",
          len(PAYLOAD) > SPILL_THRESHOLD * 10,
          "%d bytes of payload, %d-byte threshold" % (len(PAYLOAD), SPILL_THRESHOLD))

    boot(mounts=[(config, "/etc/rimsky/rimsky.yml")])
    iid = new_instance(deploy(SPEC))
    send_message(iid)
    tl = quiet(iid)
    show(tl)
    worker = [nid for nid, t in node_types(iid).items() if t == "worker"][0]
    attrs = call("GET", "/v1/nodes/%s" % worker)[1]["latest_attributes"]
    check("the all-in-one deployment accepts the memory blob backend and runs work on it",
          [r for r in tl if r["kind"] == "terminal/success" and r["node"] == "worker"],
          json.dumps([r["kind"] for r in tl][-4:]))
    check("a spilled payload written by the supervisor role reads back whole through the control-api",
          attrs.get("payload") == PAYLOAD,
          "read back %d bytes, wrote %d" % (len(attrs.get("payload") or ""), len(PAYLOAD)))

    require_image(RIMSKY)
    _, res = run_container(RIMSKY, env={"RIMSKY_CONTROL_API_HOST": "0.0.0.0"},
                           mounts=[(config, "/etc/rimsky/rimsky.yml")],
                           command=["rimsky-control-api"], detach=False)
    out = res.stdout + res.stderr
    check("the same config in a single-role container is refused at startup",
          res.returncode != 0, "exit code %d" % res.returncode)
    check("the refusal names the memory backend and the single-process requirement",
          "memory backend is dev-only" in out and "single-process mode" in out,
          out.strip().splitlines()[-1] if out.strip() else "(no output)")
    finish()


try:
    main()
finally:
    teardown()
