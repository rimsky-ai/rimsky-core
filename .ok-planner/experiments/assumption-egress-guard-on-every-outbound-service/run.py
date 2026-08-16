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
NET = "exp-assumption-egress-net-" + SUFFIX
PG = "exp-assumption-egress-pg-" + SUFFIX
SINK = "exp-assumption-egress-sink-" + SUFFIX
STATE = {"base": None}
CHECKS = []
CONTAINERS = []
NETWORKS = []
SETTLED = ("completed", "failed", "terminated")

HTTP_NODE_URL = "http://%s:8000/probe/http-node" % SINK
VERIFIER_URL = "http://%s:8000/probe/verifier-http" % SINK
LINEAGE_URL = "http://%s:8000/probe/openlineage" % SINK

SPEC = {
    "name": "exp-assumption-egress",
    "version": "1",
    "nodes": [
        {"type": "dial-http-node", "executor": "http-node",
         "attributes": {"schema": {"type": "object", "properties": {
             "url": {"type": "string", "default": HTTP_NODE_URL},
             "method": {"type": "string", "default": "GET"}}}}},
        {"type": "dial-verifier", "executor": "verifier-http",
         "attributes": {"schema": {"type": "object", "properties": {
             "url": {"type": "string", "default": VERIFIER_URL},
             "expected_status": {"type": "integer", "default": 200}}}}},
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


def run_detached(name, ref, env=None, publish=None, mounts=None, network=None, command=None):
    docker("rm", "-f", name)
    args = ["run", "-d", "--name", name]
    if network:
        args += ["--network", network, "--network-alias", name]
    for host_port, guest_port in (publish or []):
        args += ["-p", "127.0.0.1:%d:%d" % (host_port, guest_port)]
    for host, guest in (mounts or []):
        args += ["-v", "%s:%s:ro" % (host, guest)]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(ref)
    args += command or []
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


def sink_requests(sink_port):
    try:
        with urllib.request.urlopen("http://127.0.0.1:%d/_log" % sink_port, timeout=30) as resp:
            return json.loads(resp.read().decode())["requests"]
    except Exception as exc:
        die("sink log unreadable: " + str(exc))


def boot_rimsky(name, cfg, env=None):
    port = free_port()
    full = {"RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST": "127.0.0.1"}
    full.update(env or {})
    run_detached(name, image("rimsky-all-in-one"), env=full, publish=[(port, 8080)],
                 mounts=[(cfg, "/etc/rimsky/rimsky.yml")], network=NET)
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        if not running(name):
            die("rimsky container exited during boot:\n" + logs(name)[-2000:])
        if call("GET", "/v1/health")[0] == 200:
            return name
        time.sleep(0.3)


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


def send_message(iid):
    return call("POST", "/v1/instances/%s/messages" % iid, {},
                {"Idempotency-Key": uuid.uuid4().hex})


def node_types(iid):
    return {n["id"]: n["node_type"] for n in call("GET", "/v1/instances/%s/nodes" % iid)[1]["nodes"]}


def timeline(iid):
    types = node_types(iid)
    rows = call("GET", "/v1/observability/events?instance_id=%s&limit=500" % iid)[1]["events"] or []
    return [{"seq": e["id"], "node": types.get(e.get("node_id"), ""), "kind": e["kind"],
             "payload": e["payload"] or {}} for e in sorted(rows, key=lambda r: r["id"])]


def quiet(iid):
    while True:
        frames = call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]
        live = call("GET", "/v1/observability/node-runs?instance_id=%s" % iid)[1]["node_runs"]
        if frames and all(f["state"] in SETTLED for f in frames) and not live:
            return timeline(iid)
        time.sleep(0.25)


def terminal(tl, node):
    rows = [r for r in tl if r["node"] == node and r["kind"].startswith("terminal/")]
    return rows[-1] if rows else None


def drive(tid):
    iid = new_instance(tid)
    send_message(iid)
    return quiet(iid)


docker("network", "rm", NET)
if docker("network", "create", NET).returncode != 0:
    die("docker network create failed")
NETWORKS.append(NET)

run_detached(PG, "postgres:16-alpine", network=NET,
             env={"POSTGRES_USER": "u", "POSTGRES_PASSWORD": "p", "POSTGRES_DB": "rimsky"})
while docker("exec", PG, "pg_isready", "-h", PG, "-U", "u", "-d", "rimsky").returncode != 0:
    if not running(PG):
        die("postgres exited:\n" + logs(PG)[-1000:])
    time.sleep(0.3)

sink_port = free_port()
run_detached(SINK, "python:3.12-alpine", network=NET, publish=[(sink_port, 8000)],
             mounts=[(os.path.join(HERE, "sink.py"), "/srv/sink.py")],
             command=["python3", "/srv/sink.py"])
while True:
    try:
        urllib.request.urlopen("http://127.0.0.1:%d/_log" % sink_port, timeout=30).read()
        break
    except Exception:
        if not running(SINK):
            die("sink exited:\n" + logs(SINK)[-1000:])
        time.sleep(0.2)
sink_ip = docker("inspect", "-f",
                 "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", SINK).stdout.strip()

cfgdir = tempfile.mkdtemp()
os.chmod(cfgdir, 0o777)
cfg = os.path.join(cfgdir, "rimsky.yml")
with open(cfg, "w") as fh:
    fh.write("persistence:\n"
             "  driver: postgres\n"
             "  postgres:\n"
             "    dsn: postgres://u:p@%s:5432/rimsky?sslmode=disable\n"
             "claim_producers: {}\n"
             "named_locks: {}\n"
             "executors: {}\n" % PG)

print("== two bundled executors, one private-range URL, no allowlist ==")
check("the sink sits in a private range the guard blocks by default",
      sink_ip.startswith("172.") or sink_ip.startswith("10.") or sink_ip.startswith("192.168."),
      "sink address %s" % sink_ip)
boot_rimsky("exp-assumption-egress-rimsky-" + SUFFIX, cfg)
tid = deploy(SPEC)
tl = drive(tid)
for row in tl:
    if row["kind"].startswith("terminal/") or row["kind"] == "work_started":
        print("    %-5s %-18s %-24s %s" % (row["seq"], row["node"], row["kind"],
                                           json.dumps(row["payload"])[:150]))

http_term = terminal(tl, "dial-http-node")
check("the http-node dispatch is refused before it dials",
      http_term and http_term["kind"] != "terminal/success"
      and "egress" in json.dumps(http_term["payload"]),
      json.dumps(http_term["payload"])[:300] if http_term else "no terminal")
verifier_term = terminal(tl, "dial-verifier")
seen = sink_requests(sink_port)
paths = sorted(r["path"] for r in seen)
check("nothing from the guarded executor ever reached the sink",
      not [p for p in paths if p.startswith("/probe/http-node")], "sink saw: %s" % paths)
check("the verifier-http executor dialed the same private address and was answered",
      [p for p in paths if p.startswith("/probe/verifier-http")]
      and verifier_term is not None,
      "sink saw: %s | verifier terminal %s" % (paths, verifier_term["kind"] if verifier_term else "none"))

print("")
print("== the same http-node URL, with the operator allowlist set ==")
docker("rm", "-f", "exp-assumption-egress-rimsky-" + SUFFIX)
CONTAINERS.remove("exp-assumption-egress-rimsky-" + SUFFIX)
boot_rimsky("exp-assumption-egress-rimsky-open-" + SUFFIX, cfg,
            env={"RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST": "%s/32" % sink_ip})
tid = deploy(SPEC)
tl = drive(tid)
http_term = terminal(tl, "dial-http-node")
paths = sorted(r["path"] for r in sink_requests(sink_port))
check("allowlisting the range lets the guarded executor through, so the guard was the refusal",
      [p for p in paths if p.startswith("/probe/http-node")]
      and http_term and http_term["kind"] == "terminal/success",
      "sink saw: %s | http-node terminal %s" % (paths, http_term["kind"] if http_term else "none"))

print("")
print("== the openlineage subscriber, pointed at the same private address ==")
run_detached("exp-assumption-egress-openlineage-" + SUFFIX, image("rimsky-subscriber-openlineage"),
             network=NET,
             env={"RIMSKY_OPENLINEAGE_RIMSKY_DSN":
                      "postgres://u:p@%s:5432/rimsky?sslmode=disable" % PG,
                  "RIMSKY_OPENLINEAGE_BACKEND_URL": LINEAGE_URL,
                  "RIMSKY_OPENLINEAGE_POLL_INTERVAL": "1s"})
while True:
    posts = [r for r in sink_requests(sink_port)
             if r["method"] == "POST" and r["path"].startswith("/probe/openlineage")]
    if posts:
        break
    if not running("exp-assumption-egress-openlineage-" + SUFFIX):
        die("openlineage exited before posting:\n"
            + logs("exp-assumption-egress-openlineage-" + SUFFIX)[-1500:])
    time.sleep(0.3)
check("the subscriber posts lineage to a private-range backend with nothing in its way",
      bool(posts), "%d POSTs, first body %s" % (len(posts), posts[0]["body"][:120]))
metadata = "169.254.169.254"
first = "exp-assumption-egress-openlineage-" + SUFFIX
docker("rm", "-f", first)
CONTAINERS.remove(first)
meta_name = "exp-assumption-egress-openlineage-metadata-" + SUFFIX
run_detached(meta_name, image("rimsky-subscriber-openlineage"), network=NET,
             env={"RIMSKY_OPENLINEAGE_RIMSKY_DSN":
                      "postgres://u:p@%s:5432/rimsky?sslmode=disable" % PG,
                  "RIMSKY_OPENLINEAGE_BACKEND_URL": "http://%s/openlineage" % metadata,
                  "RIMSKY_OPENLINEAGE_POLL_INTERVAL": "1s"})
drive(tid)
while "openlineage.emit_failed" not in logs(meta_name):
    if not running(meta_name):
        die("openlineage exited before attempting an emit:\n" + logs(meta_name)[-1500:])
    time.sleep(0.3)
failed = [l for l in logs(meta_name).splitlines() if "openlineage.emit_failed" in l]
check("pointed at the cloud-metadata address, the subscriber tries the dial rather than refusing it",
      metadata in failed[0] and "egress:" not in failed[0],
      failed[0][:260])

finish()
