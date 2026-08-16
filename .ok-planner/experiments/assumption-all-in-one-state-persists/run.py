import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    print("HARNESS ERROR: export RIMSKY_IMAGE_TAG=src-<tree hash> first")
    sys.exit(2)
IMAGE = "rimsky-all-in-one:" + TAG
SUFFIX = uuid.uuid4().hex[:6]
STATE = {"base": None}
CHECKS = []
CONTAINERS = []
SETTLED = ("completed", "failed", "terminated")
KEY = "persisted-instance"

SPEC = {
    "name": "exp-assumption-state-persists", "version": "1",
    "messages": [{"type": "state/ping"}],
    "nodes": [
        {"type": "trigger", "kind": "loop_counter",
         "attributes": {"schema": {"type": "object", "properties": {
             "max": {"type": "integer", "default": 3}, "count": {"type": "integer"}}}}},
        {"type": "listener", "kind": "attribute_passthrough",
         "subscribes": [{"node": "state/ping", "type": "terminal/success",
                         "force_upstream_refresh": False}],
         "attributes": {"schema": {"type": "object",
                                   "properties": {"seen": {"type": "integer", "default": 1}}}}},
    ],
}


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def die(msg):
    print("HARNESS ERROR: " + msg)
    teardown()
    sys.exit(2)


def teardown():
    for name in list(CONTAINERS):
        docker("rm", "-f", name)
    del CONTAINERS[:]


def free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def check(label, ok, detail=""):
    CHECKS.append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + str(detail)[:320]) if detail else ""))


def finish():
    teardown()
    failed = [c for c in CHECKS if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(CHECKS), len(failed)))
    print("RESULT: " + ("FAIL" if failed else "PASS"))
    sys.exit(1 if failed else 0)


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def running(name):
    return docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() == "true"


def call(method, path, body=None, headers=None):
    data = None if body is None else json.dumps(body).encode()
    hdrs = {"Content-Type": "application/json"}
    if headers:
        hdrs.update(headers)
    req = urllib.request.Request(STATE["base"] + path, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read().decode()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode()
        try:
            return exc.code, json.loads(raw)
        except ValueError:
            return exc.code, raw
    except Exception as exc:
        return 0, str(exc)


def boot(name, state_dir=None):
    docker("rm", "-f", name)
    port = free_port()
    args = ["run", "-d", "--name", name, "-p", "127.0.0.1:%d:8080" % port]
    if state_dir:
        args += ["-v", "%s:/var/lib/rimsky" % state_dir]
    args.append(IMAGE)
    res = docker(*args)
    CONTAINERS.append(name)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        if not running(name):
            die("container %s exited during boot:\n%s" % (name, logs(name)[-1500:]))
        if call("GET", "/v1/health")[0] == 200:
            return name
        time.sleep(0.3)


def survey(iid):
    events = call("GET", "/v1/events?instance_id=%s&limit=1000" % iid)[1]["events"]
    return {
        "templates": sorted(t["id"] for t in call("GET", "/v1/templates")[1]["templates"]),
        "instances": sorted((i["id"], i["instance_key"])
                            for i in call("GET", "/v1/instances")[1]["instances"]),
        "event_kinds": sorted(e["kind"] for e in events),
        "messages": sorted(m["type"] for m in
                           call("GET", "/v1/instances/%s/messages" % iid)[1]["messages"]),
        "node_runs": sorted("%s|%s" % (n["node_type"], json.dumps(n["run_summary"], sort_keys=True))
                            for n in call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]),
    }


if docker("image", "inspect", IMAGE).returncode != 0:
    die("image %s is not present locally; build it with: make core-images" % IMAGE)
state = tempfile.mkdtemp()
os.chmod(state, 0o777)

print("== a deployment with some history in it ==")
first = boot("exp-assumption-state-first-" + SUFFIX, state)
status, out = call("POST", "/v1/templates", {"spec": SPEC})
if status not in (200, 201):
    die("template register rejected: %s %s" % (status, out))
tid = out["template_id"]
call("POST", "/v1/templates/%s/deploy" % tid, {})
status, out = call("POST", "/v1/instances", {
    "template": tid, "instance_key": KEY, "target_agent": "audit-agent"})
if status not in (200, 201):
    die("instance create rejected: %s %s" % (status, out))
iid = out["instance_id"]
call("POST", "/v1/instances/%s/messages" % iid, {}, {"Idempotency-Key": uuid.uuid4().hex})
call("POST", "/v1/instances/%s/messages" % iid, {"type": "state/ping"},
     {"Idempotency-Key": uuid.uuid4().hex})
while True:
    frames = call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]
    live = call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]
    if frames and all(f["state"] in SETTLED for f in frames) and not live:
        break
    if not running(first):
        die("container exited mid-run:\n" + logs(first)[-1500:])
    time.sleep(0.25)
before = survey(iid)
check("the deployment holds a template, an instance and a run history",
      before["templates"] == [tid] and before["instances"] == [(iid, KEY)]
      and len(before["event_kinds"]) > 5,
      "%d events over %d kinds" % (len(before["event_kinds"]), len(set(before["event_kinds"]))))
check("the state file is on the mounted volume", os.path.exists(os.path.join(state, "state.db")),
      "files: %s" % sorted(os.listdir(state)))

print("")
print("== the container is destroyed and replaced, volume kept ==")
docker("rm", "-f", first)
CONTAINERS.remove(first)
boot("exp-assumption-state-second-" + SUFFIX, state)
after = survey(iid)
check("the template is still registered under the same id", after["templates"] == before["templates"],
      json.dumps(after["templates"])[:120])
check("the instance is still there under the same id and key",
      after["instances"] == before["instances"], json.dumps(after["instances"])[:160])
check("the event history came back whole", after["event_kinds"] == before["event_kinds"],
      "%d events before, %d after" % (len(before["event_kinds"]), len(after["event_kinds"])))
check("the messages and the per-node run counts came back with it",
      after["messages"] == before["messages"] and after["node_runs"] == before["node_runs"],
      json.dumps(after["node_runs"])[:200])

print("")
print("== the same image with no volume mounted ==")
boot("exp-assumption-state-unmounted-" + SUFFIX)
check("a container with nothing mounted starts with no templates and no instances",
      call("GET", "/v1/templates")[1]["templates"] == []
      and call("GET", "/v1/instances")[1]["instances"] == [],
      "the mount is what carried the history")

finish()
