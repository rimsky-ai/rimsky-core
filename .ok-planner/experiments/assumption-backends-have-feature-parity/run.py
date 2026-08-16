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
NET = "exp-assumption-parity-net-" + SUFFIX
PG = "exp-assumption-parity-pg-" + SUFFIX
STATE = {"base": None}
CHECKS = []
CONTAINERS = []
NETWORKS = []
SETTLED = ("completed", "failed", "terminated")

SPEC = {
    "name": "exp-assumption-parity", "version": "1",
    "messages": [{"type": "parity/ping",
                  "body_schema": {"type": "object", "properties": {"n": {"type": "integer"}}}}],
    "nodes": [
        {"type": "trigger", "kind": "loop_counter",
         "attributes": {"schema": {"type": "object", "properties": {
             "max": {"type": "integer", "default": 3}, "count": {"type": "integer"}}}}},
        {"type": "announcer", "sends_message": "parity/ping",
         "subscribes": [{"node": "trigger", "type": "attribute/count/changed",
                         "force_upstream_refresh": False}],
         "attributes": {"schema": {"type": "object",
                                   "properties": {"n": {"type": "integer", "default": 7}}}}},
        {"type": "listener", "kind": "attribute_passthrough",
         "subscribes": [{"node": "parity/ping", "type": "terminal/success",
                         "force_upstream_refresh": False}],
         "attributes": {"schema": {"type": "object",
                                   "properties": {"seen": {"type": "integer", "default": 1}}}}},
    ],
}

SQLITE_CFG = ("persistence:\n  driver: sqlite\n  sqlite:\n    path: /var/lib/rimsky/state.db\n"
              "claim_producers: {}\nnamed_locks: {}\nexecutors: {}\n")


def postgres_cfg(blob=None):
    body = ("persistence:\n  driver: postgres\n  postgres:\n"
            "    dsn: postgres://u:p@%s:5432/rimsky?sslmode=disable\n" % PG)
    if blob:
        body += "  blob:\n    backend: %s\n    spill_threshold_bytes: 64\n" % blob
    body += "claim_producers: {}\nnamed_locks: {}\nexecutors: {}\n"
    return body


def sqlite_cfg(blob=None):
    body = "persistence:\n  driver: sqlite\n  sqlite:\n    path: /var/lib/rimsky/state.db\n"
    if blob:
        body += "  blob:\n    backend: %s\n    spill_threshold_bytes: 64\n" % blob
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


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def running(name):
    return docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() == "true"


def write_cfg(body):
    d = tempfile.mkdtemp()
    os.chmod(d, 0o777)
    path = os.path.join(d, "rimsky.yml")
    with open(path, "w") as fh:
        fh.write(body)
    return path


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


def boot(name, cfg_path, network=None):
    docker("rm", "-f", name)
    port = free_port()
    args = ["run", "-d", "--name", name, "-p", "127.0.0.1:%d:8080" % port,
            "-v", "%s:/etc/rimsky/rimsky.yml:ro" % cfg_path]
    if network:
        args += ["--network", network]
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


def boot_to_exit(name, cfg_path, network=None):
    docker("rm", "-f", name)
    args = ["run", "--name", name, "-v", "%s:/etc/rimsky/rimsky.yml:ro" % cfg_path]
    if network:
        args += ["--network", network]
    args.append(IMAGE)
    res = docker(*args)
    CONTAINERS.append(name)
    return res.returncode, res.stdout + res.stderr


def keys_of(value, path=""):
    out = set()
    if isinstance(value, dict):
        for k, v in value.items():
            out.add(path + "/" + k)
            out |= keys_of(v, path + "/" + k)
    elif isinstance(value, list):
        for item in value:
            out |= keys_of(item, path + "[]")
    return out


def drive():
    status, out = call("POST", "/v1/templates", {"spec": SPEC})
    if status not in (200, 201):
        die("template register rejected: %s %s" % (status, out))
    tid = out["template_id"]
    status, out = call("POST", "/v1/templates/%s/deploy" % tid, {})
    if status not in (200, 201):
        die("template deploy rejected: %s %s" % (status, out))
    status, out = call("POST", "/v1/instances", {
        "template": tid, "instance_key": "parity-1", "target_agent": "audit-agent"})
    if status not in (200, 201):
        die("instance create rejected: %s %s" % (status, out))
    iid = out["instance_id"]
    call("POST", "/v1/instances/%s/messages" % iid, {}, {"Idempotency-Key": uuid.uuid4().hex})
    call("POST", "/v1/instances/%s/messages" % iid, {"type": "parity/ping", "payload": {"n": 42}},
         {"Idempotency-Key": uuid.uuid4().hex})
    while True:
        frames = call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]
        live = call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]
        if frames and all(f["state"] in SETTLED for f in frames) and not live:
            break
        time.sleep(0.25)
    types = {n["id"]: n["node_type"] for n in call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]}
    events = call("GET", "/v1/events?instance_id=%s&limit=1000" % iid)[1]["events"]
    observation = {
        "template_id": tid,
        "event_pairs": sorted("%s|%s" % (types.get(e.get("node_id"), ""), e["kind"]) for e in events),
        "node_runs": sorted("%s|%s" % (n["node_type"], json.dumps(n.get("run_summary"), sort_keys=True))
                            for n in call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]),
        "frame_states": sorted(f["state"] for f in call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]),
        "message_types": sorted(m["type"] for m in call("GET", "/v1/instances/%s/messages" % iid)[1]["messages"]),
        "route_keys": {
            "instance": sorted(keys_of(call("GET", "/v1/instances/%s" % iid)[1])),
            "nodes": sorted(keys_of(call("GET", "/v1/instances/%s/nodes" % iid)[1])),
            "frames": sorted(keys_of(call("GET", "/v1/instances/%s/frames" % iid)[1])),
            "events": sorted(keys_of(call("GET", "/v1/events?instance_id=%s&limit=5" % iid)[1])),
            "summary": sorted(keys_of(call("GET", "/v1/observability/system/summary")[1])),
            "health": sorted(keys_of(call("GET", "/v1/observability/system/health")[1])),
        },
    }
    return observation


if docker("image", "inspect", IMAGE).returncode != 0:
    die("image %s is not present locally; build it with: make core-images" % IMAGE)
docker("network", "rm", NET)
if docker("network", "create", NET).returncode != 0:
    die("docker network create failed")
NETWORKS.append(NET)
res = docker("run", "-d", "--name", PG, "--network", NET,
             "-e", "POSTGRES_USER=u", "-e", "POSTGRES_PASSWORD=p", "-e", "POSTGRES_DB=rimsky",
             "postgres:16-alpine")
CONTAINERS.append(PG)
if res.returncode != 0:
    die("postgres failed to start: " + res.stderr.strip())
while docker("exec", PG, "pg_isready", "-h", PG, "-U", "u", "-d", "rimsky").returncode != 0:
    if not running(PG):
        die("postgres exited:\n" + logs(PG)[-1000:])
    time.sleep(0.3)

print("== the same template, driven identically on each driver ==")
sqlite_container = "exp-assumption-parity-sqlite-" + SUFFIX
boot(sqlite_container, write_cfg(sqlite_cfg()), network=NET)
sqlite_run = drive()
sqlite_log = logs(sqlite_container)
postgres_container = "exp-assumption-parity-postgres-" + SUFFIX
boot(postgres_container, write_cfg(postgres_cfg()), network=NET)
postgres_run = drive()
postgres_log = logs(postgres_container)

check("the same template hashes to the same id on both drivers",
      sqlite_run["template_id"] == postgres_run["template_id"],
      sqlite_run["template_id"][:24] + " / " + postgres_run["template_id"][:24])
check("the run produces the same events on both drivers",
      sqlite_run["event_pairs"] == postgres_run["event_pairs"],
      "%d events each, %d distinct kinds" % (len(sqlite_run["event_pairs"]),
                                             len(set(sqlite_run["event_pairs"]))))
check("the nodes, frames and messages settle the same way on both",
      sqlite_run["node_runs"] == postgres_run["node_runs"]
      and sqlite_run["frame_states"] == postgres_run["frame_states"]
      and sqlite_run["message_types"] == postgres_run["message_types"],
      json.dumps(sqlite_run["node_runs"])[:220])
differing = [name for name in sqlite_run["route_keys"]
             if sqlite_run["route_keys"][name] != postgres_run["route_keys"][name]]
check("six read routes answer with the same shape on both drivers",
      not differing, "routes differing: %s" % differing)

print("")
print("== what the drivers say about themselves ==")
check("only the SQLite deployment warns it is not supported for production",
      "not supported for production" in sqlite_log and "not supported for production" not in postgres_log,
      next((l[-160:] for l in sqlite_log.splitlines() if "not supported for production" in l), ""))

print("")
print("== a persistence setting only one driver has ==")
code, text = boot_to_exit("exp-assumption-parity-sqlite-lo-" + SUFFIX,
                          write_cfg(sqlite_cfg("pg-largeobject")), network=NET)
check("the pg-largeobject blob backend stops a SQLite deployment at boot",
      code != 0 and "pg-largeobject" in text and "postgres driver" in text,
      next((l.strip()[-180:] for l in text.splitlines() if "pg-largeobject" in l), text[:180]))
lo_container = "exp-assumption-parity-postgres-lo-" + SUFFIX
boot(lo_container, write_cfg(postgres_cfg("pg-largeobject")), network=NET)
check("and the same setting is accepted by a Postgres deployment",
      call("GET", "/v1/health")[0] == 200, "the deployment came up on pg-largeobject")

finish()
