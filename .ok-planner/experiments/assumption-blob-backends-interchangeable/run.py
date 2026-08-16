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
BIG = "x" * 4000
STATE = {"base": None}
CHECKS = []
CONTAINERS = []
SETTLED = ("completed", "failed", "terminated")

SPEC = {
    "name": "exp-assumption-blob-backends", "version": "1",
    "nodes": [{"type": "holder", "kind": "attribute_passthrough",
               "attributes": {"schema": {"type": "object", "properties": {
                   "payload": {"type": "string", "default": BIG},
                   "v": {"type": "integer", "default": 1}}}}}],
}


def config(backend):
    body = ("persistence:\n  driver: sqlite\n  sqlite:\n    path: /var/lib/rimsky/state.db\n"
            "  blob:\n    backend: %s\n    spill_threshold_bytes: 64\n" % backend)
    if backend == "filesystem":
        body += "    filesystem:\n      root: /var/lib/rimsky/blobs\n"
    body += "claim_producers: {}\nnamed_locks: {}\nexecutors: {}\n"
    return body


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
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + str(detail)[:300]) if detail else ""))


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


def boot(name, state_dir, cfg_path):
    docker("rm", "-f", name)
    port = free_port()
    res = docker("run", "-d", "--name", name, "-p", "127.0.0.1:%d:8080" % port,
                 "-v", "%s:/var/lib/rimsky" % state_dir,
                 "-v", "%s:/etc/rimsky/rimsky.yml:ro" % cfg_path, IMAGE)
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


def write_config(backend):
    path = os.path.join(tempfile.mkdtemp(), "rimsky.yml")
    os.chmod(os.path.dirname(path), 0o777)
    with open(path, "w") as fh:
        fh.write(config(backend))
    return path


def deploy(spec):
    status, out = call("POST", "/v1/templates", {"spec": spec})
    if status not in (200, 201):
        die("template register rejected: %s %s" % (status, out))
    tid = out["template_id"]
    status, out = call("POST", "/v1/templates/%s/deploy" % tid, {})
    if status not in (200, 201):
        die("template deploy rejected: %s %s" % (status, out))
    return tid


def new_instance(tid):
    status, out = call("POST", "/v1/instances", {
        "template": tid, "instance_key": "exp-" + uuid.uuid4().hex[:12],
        "target_agent": "audit-agent"})
    if status not in (200, 201):
        die("instance create rejected: %s %s" % (status, out))
    return out["instance_id"]


def quiet(iid):
    while True:
        frames = call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]
        live = call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]
        if frames and all(f["state"] in SETTLED for f in frames) and not live:
            return
        time.sleep(0.25)


def node_id(iid):
    for n in call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]:
        if n["node_type"] == "holder":
            return n["id"]
    die("holder node not found")


if docker("image", "inspect", IMAGE).returncode != 0:
    die("image %s is not present locally; build it with: make core-images" % IMAGE)
state = tempfile.mkdtemp()
os.chmod(state, 0o777)

print("== a value written with the filesystem backend ==")
fs_container = "exp-assumption-blob-fs-" + SUFFIX
boot(fs_container, state, write_config("filesystem"))
iid = new_instance(deploy(SPEC))
call("POST", "/v1/instances/%s/messages" % iid, {}, {"Idempotency-Key": uuid.uuid4().hex})
quiet(iid)
nid = node_id(iid)
status, out = call("GET", "/v1/nodes/%s" % nid)
value = (out or {}).get("latest_attributes", {}).get("payload", "")
check("the attribute reads back whole while its own backend is configured",
      status == 200 and value == BIG, "status %s, %d characters" % (status, len(value or "")))
blobs = []
for dirpath, _, filenames in os.walk(os.path.join(state, "blobs")):
    blobs += [os.path.join(dirpath, f) for f in filenames]
check("the value was spilled to the backend's own root, not kept in the database",
      len(blobs) >= 1, "%d file(s) under the blob root" % len(blobs))

print("")
print("== the same database, opened with a different backend ==")
docker("stop", fs_container)
inline_container = "exp-assumption-blob-inline-" + SUFFIX
boot(inline_container, state, write_config("inline"))
status, out = call("GET", "/v1/nodes/%s" % nid)
check("reading the same node now fails rather than returning the value",
      status != 200, "status %s" % status)
check("and the failure names the handle's backend and the one now configured",
      "filesystem" in json.dumps(out) and "inline" in json.dumps(out),
      json.dumps(out)[:260])

print("")
print("== and with the in-process backend ==")
docker("stop", inline_container)
memory_container = "exp-assumption-blob-memory-" + SUFFIX
boot(memory_container, state, write_config("memory"))
status, out = call("GET", "/v1/nodes/%s" % nid)
check("the in-process backend cannot read it either",
      status != 200 and "filesystem" in json.dumps(out) and "memory" in json.dumps(out),
      "status %s | %s" % (status, json.dumps(out)[:200]))

print("")
print("== put back the way it was ==")
docker("stop", memory_container)
back_container = "exp-assumption-blob-back-" + SUFFIX
boot(back_container, state, write_config("filesystem"))
status, out = call("GET", "/v1/nodes/%s" % nid)
value = (out or {}).get("latest_attributes", {}).get("payload", "")
check("with the original backend configured again the value is whole",
      status == 200 and value == BIG,
      "status %s, %d characters" % (status, len(value or "")))

finish()
