import json
import os
import re
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
NET = "exp-assumption-image-names-net-" + SUFFIX
SENSOR = "exp-assumption-image-names-sensor-" + SUFFIX
RIMSKY_PUB = "exp-assumption-image-names-publishers-" + SUFFIX
RIMSKY_SENS = "exp-assumption-image-names-sensors-" + SUFFIX
STATE = {"base": None}
CHECKS = []
CONTAINERS = []
NETWORKS = []

IMAGES = [
    "rimsky", "rimsky-all-in-one", "rimsky-host-agent-proxy", "rimsky-conformance",
    "rimsky-claim-producer-filesystem", "rimsky-claim-producer-postgres",
    "rimsky-sensor-cron", "rimsky-sensor-http", "rimsky-sensor-object-store",
    "rimsky-sensor-webhook", "rimsky-subscriber-openlineage",
    "rimsky-executor-http-node", "rimsky-executor-verifier-http",
    "rimsky-executor-verifier-shape-checks", "rimsky-executor-claude-agent",
]
KINDS = ["claim-producer", "executor", "sensor", "subscriber"]
SCHEME = re.compile(r"^rimsky-(%s)-(.+)$" % "|".join(KINDS))

SPEC = {
    "name": "exp-assumption-image-names", "version": "1",
    "messages": [{"type": "name/tick"}],
    "nodes": [{"type": "idle", "kind": "attribute_passthrough",
               "attributes": {"schema": {"type": "object",
                                         "properties": {"v": {"type": "integer", "default": 1}}}}}],
    "publishers": [{"name": "tick", "kind": "cron", "message_type": "name/tick",
                    "config": {"cron": "* * * * *"}}],
}

PUBLISHERS_CFG = ("persistence:\n  driver: sqlite\n  sqlite:\n    path: /var/lib/rimsky/state.db\n"
                  "claim_producers: {}\nnamed_locks: {}\nexecutors: {}\n"
                  'publishers:\n  "tick":\n    endpoint: "cron-sensor:9081"\n'
                  '    protocols: ["publisher"]\n')

SENSORS_CFG = ("persistence:\n  driver: sqlite\n  sqlite:\n    path: /var/lib/rimsky/state.db\n"
               "claim_producers: {}\nnamed_locks: {}\nexecutors: {}\n"
               'sensors:\n  "tick":\n    endpoint: "cron-sensor:9081"\n'
               '    protocols: ["sensor"]\n')


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


def write_cfg(body):
    d = tempfile.mkdtemp()
    os.chmod(d, 0o777)
    path = os.path.join(d, "rimsky.yml")
    with open(path, "w") as fh:
        fh.write(body)
    return path


def run_detached(name, ref, env=None, publish=None, mounts=None, alias=None):
    docker("rm", "-f", name)
    args = ["run", "-d", "--name", name, "--network", NET, "--network-alias", alias or name]
    for host_port, guest_port in (publish or []):
        args += ["-p", "127.0.0.1:%d:%d" % (host_port, guest_port)]
    for host, guest in (mounts or []):
        args += ["-v", "%s:%s:ro" % (host, guest)]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(ref)
    res = docker(*args)
    CONTAINERS.append(name)
    if res.returncode != 0:
        die("docker run %s failed: %s" % (name, res.stderr.strip()))
    return name


def run_to_exit(name, ref, mounts=None):
    docker("rm", "-f", name)
    args = ["run", "--name", name, "--network", NET]
    for host, guest in (mounts or []):
        args += ["-v", "%s:%s:ro" % (host, guest)]
    args.append(ref)
    res = docker(*args)
    CONTAINERS.append(name)
    return res.returncode, res.stdout + res.stderr


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


print("== the fifteen shipped image names ==")
for name in IMAGES:
    image(name)
following = [n for n in IMAGES if SCHEME.match(n)]
departing = [n for n in IMAGES if not SCHEME.match(n)]
check("all fifteen images of the shipped set are present to be read",
      len(IMAGES) == 15, "%d images" % len(IMAGES))
check("eleven of the fifteen follow rimsky-<kind>-<name> across all four service kinds",
      len(following) == 11
      and sorted(set(SCHEME.match(n).group(1) for n in following)) == sorted(KINDS),
      "kinds seen: %s" % sorted(set(SCHEME.match(n).group(1) for n in following)))
check("the other four carry no kind segment at all",
      departing == ["rimsky", "rimsky-all-in-one", "rimsky-host-agent-proxy", "rimsky-conformance"],
      "departing: %s" % departing)

print("")
print("== the word the configuration uses for the same thing ==")
docker("network", "rm", NET)
if docker("network", "create", NET).returncode != 0:
    die("docker network create failed")
NETWORKS.append(NET)
run_detached(SENSOR, image("rimsky-sensor-cron"), alias="cron-sensor",
             env={"RIMSKY_SENSOR_CRON_PORT": "9081",
                  "RIMSKY_CONTROL_API_URL": "http://rimsky:8080"})
api_port = free_port()
run_detached(RIMSKY_PUB, image("rimsky-all-in-one"), publish=[(api_port, 8080)],
             mounts=[(write_cfg(PUBLISHERS_CFG), "/etc/rimsky/rimsky.yml")], alias="rimsky")
STATE["base"] = "http://127.0.0.1:%d" % api_port
while True:
    if not running(RIMSKY_PUB):
        die("rimsky exited during boot:\n" + logs(RIMSKY_PUB)[-1500:])
    if call("GET", "/v1/health")[0] == 200:
        break
    time.sleep(0.3)
status, out = call("POST", "/v1/templates", {"spec": SPEC})
if status not in (200, 201):
    die("template register rejected: %s %s" % (status, out))
tid = out["template_id"]
call("POST", "/v1/templates/%s/deploy" % tid, {})
status, out = call("POST", "/v1/instances", {
    "template": tid, "instance_key": "exp-" + uuid.uuid4().hex[:12], "target_agent": "audit-agent"})
if status not in (200, 201):
    die("instance create rejected: %s %s" % (status, out))
iid = out["instance_id"]
while True:
    subs = call("GET", "/v1/instances/%s" % iid)[1].get("subscriptions") or []
    if subs and all(s["state"] in ("active", "failed") for s in subs):
        break
    if not running(RIMSKY_PUB) or not running(SENSOR):
        die("a container stopped while the subscription was mounting")
    time.sleep(0.3)
check("a sensor image is wired into a deployment under `publishers`, and mounts",
      subs[0]["state"] == "active" and subs[0]["kind"] == "cron",
      json.dumps([(s["publisher_name"], s["kind"], s["state"]) for s in subs]))

code, text = run_to_exit(RIMSKY_SENS, image("rimsky-all-in-one"),
                         mounts=[(write_cfg(SENSORS_CFG), "/etc/rimsky/rimsky.yml")])
check("the same wiring under `sensors` is not a key the configuration knows",
      code != 0 and "sensors" in text,
      next((l.strip()[-160:] for l in text.splitlines() if "sensors" in l), text[:160]))

finish()
