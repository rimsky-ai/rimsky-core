SLUG = "assumption-runtime-diagnostics-are-actionable"

import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG") or sys.exit("export RIMSKY_IMAGE_TAG=src-<tree hash> first")
IMAGE = "rimsky-all-in-one:" + TAG
PRODUCER_IMAGE = "rimsky-claim-producer-filesystem:" + TAG
NET = "exp-" + SLUG + "-net"
STACK = "exp-" + SLUG + "-stack"
PEER = "exp-" + SLUG + "-peer"
PROD = "exp-" + SLUG + "-producer"
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
WORK = tempfile.mkdtemp(prefix="rimsky-exp-diag-")
STATE = {"base": None, "checks": []}

RIMSKY_YML = """persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers:
  files:
    endpoint: "producer:9100"
    protocols: ["claim_producer"]
    write_semantics_allowed: ["sync"]
named_locks: {}
executors:
  "third-party":
    transport: grpc
    endpoint: "peer:9400"
    protocols: ["executor"]
"""

PRODUCER_YML = """root: /data
host: 0.0.0.0
grpc_port: 9100
http_port: 9110
"""

TEMPLATE = {"spec": {
    "name": "diag-actionable", "version": "1",
    "messages": [{"type": "never/sent", "body_schema": {"type": "object"}}],
    "nodes": [
        {"type": "trigger", "kind": "loop_counter",
         "attributes": {"schema": {"type": "object", "properties": {
             "max": {"type": "integer", "default": 1}, "count": {"type": "integer"}}}}},
        {"type": "holder", "executor": "third-party",
         "claim_producers": [{"name": "files", "selector": "notes/a.txt", "intent": "rw"}],
         "error_types": {"acquire/unavailable": {"action": "retry"}},
         "subscribes": [{"node": "never/sent", "type": "terminal/success",
                         "force_upstream_refresh": False}],
         "attributes": {"schema": {"type": "object", "properties": {
             "outcome": {"type": "string", "default": "park"},
             "echo": {"type": "string", "default": "held"}}}}},
        {"type": "receiver", "kind": "attribute_passthrough",
         "subscribes": [{"node": "trigger", "type": "terminal/success",
                         "force_upstream_refresh": False},
                        {"node": "holder", "type": "attribute/echo/changed",
                         "force_upstream_refresh": True}],
         "attributes": {"schema": {"type": "object", "properties": {
             "seen": {"type": "integer", "default": 1}}}}},
        {"type": "member", "executor": "third-party", "holds": {"files": {"from": "holder"}},
         "subscribes": [{"node": "holder", "type": "terminal/success",
                         "force_upstream_refresh": False}],
         "attributes": {"schema": {"type": "object", "properties": {
             "outcome": {"type": "string", "default": "ok"},
             "echo": {"type": "string", "default": "member"}}}}}]}}


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def cleanup():
    for name in (STACK, PEER, PROD):
        docker("rm", "-f", name)
    docker("network", "rm", NET)
    shutil.rmtree(WORK, ignore_errors=True)


def die(msg):
    print("HARNESS ERROR: " + msg)
    cleanup()
    sys.exit(2)


def call(method, path, body=None):
    hdrs = {"Content-Type": "application/json", "Idempotency-Key": uuid.uuid4().hex}
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(STATE["base"] + path, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            text, status = resp.read().decode(), resp.status
    except urllib.error.HTTPError as exc:
        text, status = exc.read().decode(), exc.code
    try:
        return status, (json.loads(text) if text else None), text
    except ValueError:
        return status, text, text


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def cli(*args):
    env = dict(os.environ, HOME=WORK)
    env.pop("RIMSKY_API_KEY", None)
    env.pop("RIMSKY_CONTROL_API_URL", None)
    return subprocess.run([CLI, *args], capture_output=True, text=True, env=env)


def finish():
    cleanup()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


def build_peer():
    for image in (IMAGE, PRODUCER_IMAGE):
        if docker("image", "inspect", image).returncode != 0:
            die("image %s is not present locally; build it with: make core-images service-images test-images" % image)
    src = os.path.join(WORK, "peer")
    shutil.copytree(os.path.join(HERE, "peer"), src)
    with open(os.path.join(src, "go.mod.tmpl")) as fh:
        mod = fh.read().replace("RIMSKY_PROTOCOLS_PATH", os.path.join(ROOT, "lib", "protocols"))
    with open(os.path.join(src, "go.mod"), "w") as fh:
        fh.write(mod)
    os.remove(os.path.join(src, "go.mod.tmpl"))
    arch = docker("info", "--format", "{{.Architecture}}").stdout.strip()
    arch = {"aarch64": "arm64", "x86_64": "amd64"}.get(arch, arch)
    env = dict(os.environ, GOWORK="off", GOOS="linux", GOARCH=arch, CGO_ENABLED="0",
               GOFLAGS="-mod=mod", GOPROXY="off", GOSUMDB="off")
    out = os.path.join(WORK, "peer-linux")
    res = subprocess.run(["go", "build", "-o", out, "."], cwd=src, capture_output=True,
                         text=True, env=env)
    if res.returncode != 0:
        die("peer build failed:\n" + res.stderr)
    return out


def boot(peer_bin):
    os.makedirs(os.path.join(WORK, "fsdata", "notes"))
    with open(os.path.join(WORK, "fsdata", "notes", "a.txt"), "w") as fh:
        fh.write("content\n")
    for name, text in [("rimsky.yml", RIMSKY_YML), ("producer.yml", PRODUCER_YML)]:
        with open(os.path.join(WORK, name), "w") as fh:
            fh.write(text)
    docker("network", "create", NET)
    for name in (STACK, PEER, PROD):
        docker("rm", "-f", name)
    if docker("run", "-d", "--name", PEER, "--network", NET, "--network-alias", "peer",
              "-e", "PEER_PORT=9400", "-v", peer_bin + ":/peer:ro",
              "alpine:latest", "/peer").returncode != 0:
        die("peer container failed to start")
    if docker("run", "-d", "--name", PROD, "--network", NET, "--network-alias", "producer",
              "-e", "RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG=/etc/producer.yml",
              "-v", os.path.join(WORK, "producer.yml") + ":/etc/producer.yml:ro",
              "-v", os.path.join(WORK, "fsdata") + ":/data",
              PRODUCER_IMAGE).returncode != 0:
        die("producer container failed to start")
    port = free_port()
    if docker("run", "-d", "--name", STACK, "--network", NET, "-p", "127.0.0.1:%d:8080" % port,
              "-v", os.path.join(WORK, "rimsky.yml") + ":/etc/rimsky/rimsky.yml:ro",
              IMAGE).returncode != 0:
        die("stack container failed to start")
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                break
        except Exception:
            pass
        if docker("inspect", "-f", "{{.State.Running}}", STACK).stdout.strip() != "true":
            die("stack exited during boot:\n" + docker("logs", STACK).stdout + docker("logs", STACK).stderr)
        time.sleep(0.3)
    while call("GET", "/v1/observability/executors/third-party")[1].get(
            "peer", {}).get("reachability_status") != "reachable":
        time.sleep(0.3)
    while call("GET", "/v1/observability/claim-producers")[1][
            "claim_producers"][0]["reachability_status"] != "reachable":
        time.sleep(0.3)


def wedge():
    _, out, text = call("POST", "/v1/templates", TEMPLATE)
    if not isinstance(out, dict) or "template_id" not in out:
        die("template register: " + text)
    tpl = out["template_id"]
    call("POST", "/v1/templates/%s/deploy" % tpl, {})
    _, out, text = call("POST", "/v1/instances",
                        {"template": tpl, "instance_key": "diag-act-1", "params": {},
                         "target_agent": "diag-agent"})
    if not isinstance(out, dict) or "instance_id" not in out:
        die("instance create: " + text)
    instance = out["instance_id"]
    call("POST", "/v1/instances/%s/messages" % instance, {"type": ""})
    print("      waiting for the wedge to form (blocks until a node parks)")
    while True:
        rows = [r for r in call("GET", "/v1/admin/diagnostics/parked-nodes")[1]["parked_nodes"]
                if r["instance_id"] == instance]
        if rows:
            return instance, rows[0]["node_id"]
        time.sleep(0.3)


def parked_for(instance):
    return [r for r in call("GET", "/v1/admin/diagnostics/parked-nodes")[1]["parked_nodes"]
            if r["instance_id"] == instance]


def held_for(instance):
    return [f for f in call("GET", "/v1/admin/diagnostics/held-frames")[1]["frames"]
            if f["instance_id"] == instance]


def main():
    peer_bin = build_peer()
    boot(peer_bin)
    instance, parked_node = wedge()

    print("== the four diagnostics reads report a wedged instance ==")
    check("parked-nodes names the wedged node", len(parked_for(instance)) == 1,
          json.dumps(parked_for(instance)))
    frames = held_for(instance)
    check("held-frames reports one held frame", len(frames) == 1, json.dumps(frames)[:120])
    frame = frames[0]["frame_id"]
    print("      waiting for the receiver's wake dependency to be recorded")
    while not call("GET", "/v1/admin/diagnostics/wait-sets?frame=" + frame)[1]["wait_set"]:
        time.sleep(0.3)
    waitset = call("GET", "/v1/admin/diagnostics/wait-sets?frame=" + frame)[1]["wait_set"]
    check("wait-sets reports pending wake edges for that frame", len(waitset) >= 1, str(len(waitset)))
    status, outbox, _ = call("GET", "/v1/admin/diagnostics/producer-outbox")
    check("producer-outbox answers with a depth", status == 200 and "depth" in outbox,
          json.dumps({k: v for k, v in outbox.items() if k != "entries"}))

    print("")
    print("== PRIOR CONTRADICTED: no route un-parks the node the roster names ==")
    status, body, _ = call("POST", "/v1/nodes/%s/reset" % parked_node, {})
    check("node:reset refuses — a parked node is not a failed one",
          status == 409 and "failed terminal run" in json.dumps(body),
          "%s %s" % (status, json.dumps(body)))
    for path in ["/v1/nodes/%s/resume" % parked_node,
                 "/v1/nodes/%s/unpark" % parked_node,
                 "/v1/parked/%s/resume" % parked_node,
                 "/v1/instances/%s/parked/%s/resume" % (instance, parked_node),
                 "/v1/instances/%s/nodes/%s/resume" % (instance, parked_node)]:
        status, _, text = call("POST", path, {})
        check("POST %-58s is not a route" % path.replace(instance, "{id}").replace(parked_node, "{node}"),
              status == 404 and "404 page not found" in text, str(status))
    usage = cli("parked").stderr + cli("parked").stdout
    check("`rimsky parked` offers only list, no resume verb",
          "list" in usage and "resume" not in usage, usage.strip()[:90])

    print("")
    print("== the instance-level levers do not clear it either ==")
    status, _, _ = call("POST", "/v1/instances/%s/resume" % instance, {})
    check("POST /v1/instances/{id}/resume answers %s but the node stays parked" % status,
          len(parked_for(instance)) == 1, json.dumps(parked_for(instance)))
    status, body, _ = call("POST", "/v1/instances/%s/debug/override" % instance,
                           {"action": "invalidate_node", "node_type": "holder"})
    check("debug override is refused while the instance is neither paused nor at a breakpoint",
          status in (409, 422) or "debuggable" in json.dumps(body).lower(),
          "%s %s" % (status, json.dumps(body)[:90]))
    call("POST", "/v1/instances/%s/pause" % instance, {})
    status, body, _ = call("POST", "/v1/instances/%s/debug/override" % instance,
                           {"action": "invalidate_node", "node_type": "holder"})
    check("with the instance paused the override applies (%s)" % status,
          status == 200, json.dumps(body)[:110])
    check("and the node is still parked afterwards", len(parked_for(instance)) == 1,
          json.dumps(parked_for(instance)))
    call("POST", "/v1/instances/%s/resume" % instance, {})

    print("")
    print("== nor is there a route for the other three findings ==")
    for label, method, path in [
            ("held frame release", "POST", "/v1/instances/%s/frames/%s/release" % (instance, frame)),
            ("held frame cancel", "POST", "/v1/instances/%s/frames/%s/cancel" % (instance, frame)),
            ("outbox retry", "POST", "/v1/admin/diagnostics/producer-outbox/retry"),
            ("outbox drain", "POST", "/v1/admin/producer-outbox/drain"),
            ("wait-set clear", "POST", "/v1/admin/diagnostics/wait-sets/clear"),
            ("wait-set delete", "DELETE", "/v1/admin/diagnostics/wait-sets?frame=" + frame)]:
        status, _, text = call(method, path, {} if method == "POST" else None)
        check("%-20s has no route (%s)" % (label, method), status in (404, 405), str(status))

    print("")
    print("== the one lever that clears the board is demolition ==")
    status, _, _ = call("POST", "/v1/instances/%s/terminate" % instance, {})
    check("POST /v1/instances/{id}/terminate is accepted", status == 200, str(status))
    print("      waiting until the instance reports a termination time")
    while not call("GET", "/v1/instances/" + instance)[1].get("terminated_at"):
        time.sleep(0.3)
    check("once terminated, the park roster no longer names the instance",
          parked_for(instance) == [], json.dumps(parked_for(instance)))
    check("and the held-frame roster no longer names it either",
          held_for(instance) == [], json.dumps(held_for(instance)))

    finish()


main()
