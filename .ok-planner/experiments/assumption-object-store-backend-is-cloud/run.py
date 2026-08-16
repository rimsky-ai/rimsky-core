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
SUFFIX = uuid.uuid4().hex[:6]
NET = "exp-assumption-object-store-net-" + SUFFIX
SENSOR = "exp-assumption-object-store-sensor-" + SUFFIX
RIMSKY = "exp-assumption-object-store-rimsky-" + SUFFIX
STATE = {"base": None}
CHECKS = []
CONTAINERS = []
NETWORKS = []

CLOUD = ["s3", "gcs", "azure"]

CONFIG = ("persistence:\n  driver: sqlite\n  sqlite:\n    path: /var/lib/rimsky/state.db\n"
          "claim_producers: {}\nnamed_locks: {}\nexecutors: {}\npublishers:\n"
          + "".join('  "drop-%s":\n    endpoint: "object-sensor:9083"\n    protocols: ["publisher"]\n' % name
                    for name in CLOUD + ["filesystem"]))

SPEC = {
    "name": "exp-assumption-object-store-backend", "version": "1",
    "messages": [{"type": "drop/%s" % name} for name in CLOUD + ["filesystem"]],
    "nodes": [{"type": "handler", "kind": "attribute_passthrough",
               "subscribes": [{"node": "drop/filesystem", "type": "terminal/success",
                               "force_upstream_refresh": False}],
               "attributes": {"schema": {"type": "object",
                                         "properties": {"v": {"type": "integer", "default": 1}}}}}],
    "publishers": [{"name": "drop-%s" % name, "kind": "object-store",
                    "message_type": "drop/%s" % name,
                    "config": {"backend": name, "bucket": "inbox", "prefix": "in/",
                               "poll_interval": "1s", "watermark_field": "name"}}
                   for name in CLOUD + ["filesystem"]],
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
    for net in NETWORKS:
        docker("network", "rm", net)
    del NETWORKS[:]


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


def image(name):
    ref = "%s:%s" % (name, TAG)
    if docker("image", "inspect", ref).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images" % ref)
    return ref


def run_detached(name, ref, env=None, publish=None, mounts=None, alias=None):
    docker("rm", "-f", name)
    args = ["run", "-d", "--name", name, "--network", NET, "--network-alias", alias or name]
    for host_port, guest_port in (publish or []):
        args += ["-p", "127.0.0.1:%d:%d" % (host_port, guest_port)]
    for host, guest in (mounts or []):
        args += ["-v", "%s:%s" % (host, guest)]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(ref)
    res = docker(*args)
    CONTAINERS.append(name)
    if res.returncode != 0:
        die("docker run %s failed: %s" % (name, res.stderr.strip()))
    return name


def running(name):
    return docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() == "true"


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


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


def wait_until(fn):
    while True:
        value = fn()
        if value:
            return value
        time.sleep(0.3)


def subs(iid):
    return {s["publisher_name"]: s
            for s in (call("GET", "/v1/instances/%s" % iid)[1].get("subscriptions") or [])}


def settle(iid, name):
    while True:
        state = (subs(iid).get(name) or {}).get("state")
        if state in ("active", "failed"):
            return state
        text = logs(RIMSKY)
        if ('"publisher_name":"%s"' % name) in text and "subscribe_failed" in text:
            return "refused"
        time.sleep(0.3)


root = tempfile.mkdtemp()
os.chmod(root, 0o777)
os.makedirs(os.path.join(root, "inbox", "in"))
os.chmod(os.path.join(root, "inbox"), 0o777)
os.chmod(os.path.join(root, "inbox", "in"), 0o777)
cfgdir = tempfile.mkdtemp()
os.chmod(cfgdir, 0o777)
cfg = os.path.join(cfgdir, "rimsky.yml")
with open(cfg, "w") as fh:
    fh.write(CONFIG)

docker("network", "rm", NET)
if docker("network", "create", NET).returncode != 0:
    die("docker network create failed")
NETWORKS.append(NET)

run_detached(SENSOR, image("rimsky-sensor-object-store"), alias="object-sensor",
             mounts=[(root, "/data")],
             env={"RIMSKY_SENSOR_OBJECT_STORE_PORT": "9083",
                  "RIMSKY_CONTROL_API_URL": "http://rimsky:8080",
                  "RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT": "/data"})
wait_until(lambda: "backend registered" in logs(SENSOR))
registered = [json.loads(l)["backend"] for l in logs(SENSOR).splitlines()
              if "backend registered" in l]
print("== what the sensor can service ==")
check("the object-store sensor registers only a local filesystem backend",
      registered == ["filesystem"], "registered backends: %s" % registered)

both = "exp-assumption-object-store-both-" + SUFFIX
run_detached(both, image("rimsky-sensor-object-store"), alias="object-sensor-both",
             mounts=[(root, "/data")],
             env={"RIMSKY_SENSOR_OBJECT_STORE_PORT": "9083",
                  "RIMSKY_CONTROL_API_URL": "http://rimsky:8080",
                  "RIMSKY_SENSOR_OBJECT_STORE_FS_ROOT": "/data",
                  "RIMSKY_SENSOR_OBJECT_STORE_ENABLE_MEMORY_BACKEND": "1"})
wait_until(lambda: len([l for l in logs(both).splitlines() if "backend registered" in l]) >= 2)
everything = sorted(json.loads(l)["backend"] for l in logs(both).splitlines()
                    if "backend registered" in l)
check("with every backend switch turned on the build offers a local directory and an in-process map",
      everything == ["filesystem", "memory"], "registered backends: %s" % everything)

api_port = free_port()
run_detached(RIMSKY, image("rimsky-all-in-one"), publish=[(api_port, 8080)],
             mounts=[(cfg, "/etc/rimsky/rimsky.yml")], alias="rimsky")
STATE["base"] = "http://127.0.0.1:%d" % api_port
while True:
    if not running(RIMSKY):
        die("rimsky exited during boot:\n" + logs(RIMSKY)[-1500:])
    if call("GET", "/v1/health")[0] == 200:
        break
    time.sleep(0.3)

status, out = call("POST", "/v1/templates", {"spec": SPEC})
if status not in (200, 201):
    die("template register rejected: %s %s" % (status, out))
tid = out["template_id"]
status, out = call("POST", "/v1/templates/%s/deploy" % tid, {})
if status not in (200, 201):
    die("template deploy rejected: %s %s" % (status, out))
status, out = call("POST", "/v1/instances", {
    "template": tid, "instance_key": "exp-" + uuid.uuid4().hex[:12], "target_agent": "audit-agent"})
if status not in (200, 201):
    die("instance create rejected: %s %s" % (status, out))
iid = out["instance_id"]

print("")
print("== a bucket named on each cloud store ==")
for name in CLOUD:
    state = settle(iid, "drop-%s" % name)
    refusal = next((l for l in logs(RIMSKY).splitlines()
                    if ("drop-%s" % name) in l and "not serviceable by this build" in l), "")
    check("a publisher naming %s never mounts, and the refusal names what the build can service" % name,
          state != "active" and "registered backends: filesystem" in refusal,
          "state %s | %s" % (state, refusal[-200:] if refusal else "no refusal logged"))

print("")
print("== the one backend the sensor does service ==")
check("the filesystem publisher mounts live", settle(iid, "drop-filesystem") == "active",
      json.dumps({k: v["state"] for k, v in subs(iid).items()}))
with open(os.path.join(root, "inbox", "in", "alpha.txt"), "w") as fh:
    fh.write("payload")
arrival = wait_until(lambda: next(
    (m for m in call("GET", "/v1/instances/%s/messages" % iid)[1]["messages"]
     if m["type"] == "drop/filesystem"), None))
check("a file deposited under the environment's own root is what reaches the graph",
      arrival["payload"]["backend"] == "filesystem" and arrival["payload"]["object_name"] == "in/alpha.txt",
      json.dumps(arrival["payload"])[:220])

finish()
