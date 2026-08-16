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

HERE = os.path.dirname(os.path.abspath(__file__))
TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    print("HARNESS ERROR: export RIMSKY_IMAGE_TAG=src-<tree hash> first")
    sys.exit(2)
SUFFIX = uuid.uuid4().hex[:6]
NET = "exp-assumption-sensor-ha-net-" + SUFFIX
PG = "exp-assumption-sensor-ha-pg-" + SUFFIX
RIMSKY = "exp-assumption-sensor-ha-rimsky-" + SUFFIX
REPLICA_A = "exp-assumption-sensor-ha-a-" + SUFFIX
REPLICA_B = "exp-assumption-sensor-ha-b-" + SUFFIX
STATE = {"base": None}
CHECKS = []
CONTAINERS = []
NETWORKS = []

CONFIG = """persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
executors: {}
publishers:
  "tick":
    endpoint: "cron-sensor:9081"
    protocols: ["publisher"]
  "beat-a":
    endpoint: "cron-sensor-a:9081"
    protocols: ["publisher"]
  "beat-b":
    endpoint: "cron-sensor-b:9081"
    protocols: ["publisher"]
"""

TICK_SPEC = {
    "name": "exp-assumption-sensor-ha-tick", "version": "1",
    "messages": [{"type": "cron/tick"}],
    "nodes": [{"type": "reactor", "kind": "attribute_passthrough",
               "subscribes": [{"node": "cron/tick", "type": "terminal/success",
                               "force_upstream_refresh": False}],
               "attributes": {"schema": {"type": "object",
                                         "properties": {"v": {"type": "integer", "default": 1}}}}}],
    "publishers": [{"name": "tick", "kind": "cron", "message_type": "cron/tick",
                    "config": {"cron": "* * * * *"}}],
}

BEAT_SPEC = {
    "name": "exp-assumption-sensor-ha-beat", "version": "1",
    "messages": [{"type": "cron/beat-a"}, {"type": "cron/beat-b"}],
    "nodes": [{"type": "idle", "kind": "attribute_passthrough",
               "attributes": {"schema": {"type": "object",
                                         "properties": {"v": {"type": "integer", "default": 1}}}}}],
    "publishers": [
        {"name": "beat-a", "kind": "cron", "message_type": "cron/beat-a",
         "config": {"cron": "* * * * *"}},
        {"name": "beat-b", "kind": "cron", "message_type": "cron/beat-b",
         "config": {"cron": "* * * * *"}},
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


def run_detached(name, ref, env=None, publish=None, mounts=None, aliases=None):
    docker("rm", "-f", name)
    args = ["run", "-d", "--name", name, "--network", NET]
    for alias in aliases or []:
        args += ["--network-alias", alias]
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


def messages(iid, mtype):
    rows = call("GET", "/v1/instances/%s/messages" % iid)[1]["messages"]
    return [m for m in rows if m["type"] == mtype]


def windows(iid, mtype):
    seen = {}
    for m in messages(iid, mtype):
        seen.setdefault(m["payload"]["fire_at"], []).append(m)
    return seen


def wait_until(fn, alive=()):
    while True:
        value = fn()
        if value:
            return value
        for name in alive:
            if not running(name):
                die("container %s stopped while the run was waiting:\n%s" % (name, logs(name)[-1200:]))
        time.sleep(0.5)


def subscription_id(iid, publisher_name):
    for sub in (call("GET", "/v1/instances/%s" % iid)[1].get("subscriptions") or []):
        if sub["publisher_name"] == publisher_name:
            return sub["id"]
    return None


def replica_holding(sub_id):
    for name in (REPLICA_A, REPLICA_B):
        if sub_id in logs(name):
            return name
    return None


cfgdir = tempfile.mkdtemp()
os.chmod(cfgdir, 0o777)
cfg = os.path.join(cfgdir, "rimsky.yml")
with open(cfg, "w") as fh:
    fh.write(CONFIG)

docker("network", "rm", NET)
if docker("network", "create", NET).returncode != 0:
    die("docker network create failed")
NETWORKS.append(NET)
run_detached(PG, "postgres:16-alpine", aliases=["state-db"],
             env={"POSTGRES_USER": "u", "POSTGRES_PASSWORD": "p", "POSTGRES_DB": "sensorstate"})
while docker("exec", PG, "pg_isready", "-h", PG, "-U", "u", "-d", "sensorstate").returncode != 0:
    if not running(PG):
        die("postgres exited:\n" + logs(PG)[-1000:])
    time.sleep(0.3)

sensor_env = {"RIMSKY_SENSOR_CRON_PORT": "9081",
              "RIMSKY_CONTROL_API_URL": "http://rimsky:8080",
              "RIMSKY_SENSOR_CRON_STATE_DSN":
                  "postgres://u:p@state-db:5432/sensorstate?sslmode=disable"}
run_detached(REPLICA_A, image("rimsky-sensor-cron"), env=sensor_env,
             aliases=["cron-sensor", "cron-sensor-a"])
run_detached(REPLICA_B, image("rimsky-sensor-cron"), env=sensor_env,
             aliases=["cron-sensor", "cron-sensor-b"])

api_port = free_port()
run_detached(RIMSKY, image("rimsky-all-in-one"), publish=[(api_port, 8080)],
             mounts=[(cfg, "/etc/rimsky/rimsky.yml")], aliases=["rimsky"])
STATE["base"] = "http://127.0.0.1:%d" % api_port
while True:
    if not running(RIMSKY):
        die("rimsky exited during boot:\n" + logs(RIMSKY)[-1500:])
    if call("GET", "/v1/health")[0] == 200:
        break
    time.sleep(0.3)

print("== one cron subscription, two replicas behind one endpoint ==")
tick_iid = new_instance(deploy(TICK_SPEC))
beat_iid = new_instance(deploy(BEAT_SPEC))
tick_sub = wait_until(lambda: subscription_id(tick_iid, "tick"),
                      alive=(RIMSKY, REPLICA_A, REPLICA_B, PG))
holder = wait_until(lambda: replica_holding(tick_sub),
                    alive=(RIMSKY, REPLICA_A, REPLICA_B, PG))
standby = REPLICA_B if holder == REPLICA_A else REPLICA_A
check("the control API mounts the subscription on exactly one replica",
      tick_sub not in logs(standby),
      "holder %s; the other replica never sees the subscription"
      % ("A" if holder == REPLICA_A else "B"))

wait_until(lambda: len(windows(tick_iid, "cron/tick")) >= 2,
           alive=(RIMSKY, REPLICA_A, REPLICA_B, PG))
closed = sorted(windows(tick_iid, "cron/tick"))[:-1]
check("each closed window produced exactly one message",
      all(len(windows(tick_iid, "cron/tick")[w]) == 1 for w in closed),
      json.dumps({w: len(windows(tick_iid, "cron/tick")[w]) for w in closed}))
check("the standby replica never took the subscription up",
      tick_sub not in logs(standby),
      "the standby holds no watch for it")

print("")
print("== the replica holding the subscription is stopped ==")
before = set(windows(tick_iid, "cron/tick"))
beats_before = set(windows(beat_iid, "cron/beat-a")) | set(windows(beat_iid, "cron/beat-b"))
docker("stop", holder)
def beats_since_stop():
    return sorted((set(windows(beat_iid, "cron/beat-a"))
                   | set(windows(beat_iid, "cron/beat-b"))) - beats_before)


def failover_settled():
    ticks = set(windows(tick_iid, "cron/tick")) - before
    if ticks:
        return ("resumed", sorted(ticks))
    if len(beats_since_stop()) >= 2:
        return ("silent", [])
    return None


outcome, resumed_windows = wait_until(failover_settled, alive=(RIMSKY, PG))
check("the surviving replica keeps its own subscriptions firing",
      bool(beats_since_stop()), "beat windows after the stop: %s" % beats_since_stop())
check("the subscription is taken up again and keeps firing without the stopped replica",
      outcome == "resumed", "windows after the stop: %s" % resumed_windows)
check("and it is the surviving replica that now holds it",
      tick_sub in logs(standby), "the standby's log now carries the subscription id")
after = set(windows(tick_iid, "cron/tick"))
minutes = sorted(after)
gaps = [(minutes[i], minutes[i + 1]) for i in range(len(minutes) - 1)
        if (time.mktime(time.strptime(minutes[i + 1], "%Y-%m-%dT%H:%M:%SZ"))
            - time.mktime(time.strptime(minutes[i], "%Y-%m-%dT%H:%M:%SZ"))) != 60]
check("no minute of the schedule was skipped across the failover", not gaps,
      "windows: %s" % minutes)

print("")
print("== both replicas made to hold the same subscription ==")
docker("start", holder)
while not running(holder):
    time.sleep(0.2)
docker("restart", standby)
wait_until(lambda: "sensor-cron.state_recovered" in logs(standby) and
                   "sensor-cron.state_recovered" in logs(holder),
           alive=(RIMSKY, PG))
check("a restarted replica recovers the subscription from the shared state store",
      "sensor-cron.state_recovered" in logs(standby) and "sensor-cron.state_recovered" in logs(holder),
      "both processes now hold the same watch")
seen_now = set(windows(tick_iid, "cron/tick"))
wait_until(lambda: len(set(windows(tick_iid, "cron/tick")) - seen_now) >= 2,
           alive=(RIMSKY, REPLICA_A, REPLICA_B, PG))
fresh = sorted(set(windows(tick_iid, "cron/tick")) - seen_now)[:-1]
check("with two processes firing the same window the instance still receives one message",
      all(len(windows(tick_iid, "cron/tick")[w]) == 1 for w in fresh),
      json.dumps({w: len(windows(tick_iid, "cron/tick")[w]) for w in fresh}))

finish()
