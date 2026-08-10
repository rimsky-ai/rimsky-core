import json
import os
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    print("HARNESS ERROR: set RIMSKY_IMAGE_TAG to the tag built from this tree")
    sys.exit(2)

STATE = {"base": None, "containers": [], "networks": [], "checks": [], "tmp": []}
SETTLED_FRAME_STATES = ("completed", "failed", "terminated")


def die(msg):
    print("HARNESS ERROR: " + msg)
    teardown()
    sys.exit(2)


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def image(name):
    ref = "%s:%s" % (name, TAG)
    if docker("image", "inspect", ref).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % ref)
    return ref


def free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def network(slug):
    name = "rimsky-exp-%s-%s" % (slug, uuid.uuid4().hex[:6])
    res = docker("network", "create", name)
    if res.returncode != 0:
        die("docker network create failed: " + res.stderr.strip())
    STATE["networks"].append(name)
    return name


def run_container(name, ref, net=None, alias=None, env=None, ports=None, volumes=None, cmd=None):
    args = ["run", "-d", "--name", name]
    if net:
        args += ["--network", net]
        args += ["--network-alias", alias or name]
    for host, cport in (ports or []):
        args += ["-p", "127.0.0.1:%d:%d" % (host, cport)]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    for spec in (volumes or []):
        args += ["-v", spec]
    args.append(ref)
    if cmd:
        args += list(cmd)
    res = docker(*args)
    if res.returncode != 0:
        die("docker run %s failed: %s" % (name, res.stderr.strip()))
    if name not in STATE["containers"]:
        STATE["containers"].append(name)
    return name


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def teardown():
    for name in STATE["containers"]:
        docker("rm", "-f", name)
    STATE["containers"] = []
    for net in STATE["networks"]:
        docker("network", "rm", net)
    STATE["networks"] = []


def call(method, path, body=None, headers=None, base=None):
    data = None if body is None else json.dumps(body).encode()
    hdrs = {"Content-Type": "application/json"}
    if headers:
        hdrs.update(headers)
    req = urllib.request.Request((base or STATE["base"]) + path, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            raw = resp.read().decode()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode()
        try:
            return exc.code, json.loads(raw)
        except ValueError:
            return exc.code, raw


def raw_post(url, body, headers=None):
    req = urllib.request.Request(url, data=body, headers=headers or {}, method="POST")
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()
    except urllib.error.URLError as exc:
        return 0, str(exc)


def boot_rimsky(slug, net=None, alias=None, config_path=None, env=None, volumes=None):
    port = free_port()
    name = "rimsky-exp-%s-api-%s" % (slug, uuid.uuid4().hex[:6])
    vols = list(volumes or [])
    if config_path:
        vols.append("%s:/etc/rimsky/rimsky.yml:ro" % config_path)
    run_container(name, image("rimsky-all-in-one"), net=net, alias=alias or "rimsky",
                  env=env, ports=[(port, 8080)], volumes=vols)
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                return name
        except Exception:
            pass
        time.sleep(0.3)


def deploy(spec):
    status, out = call("POST", "/v1/templates", {"spec": spec})
    if status not in (200, 201):
        die("template register rejected: %s %s" % (status, out))
    template_id = out["template_id"]
    status, out = call("POST", "/v1/templates/%s/deploy" % template_id, {})
    if status not in (200, 201):
        die("template deploy rejected: %s %s" % (status, out))
    return template_id


def new_instance(template_id):
    status, out = call("POST", "/v1/instances", {
        "template": template_id,
        "instance_key": "exp-" + uuid.uuid4().hex[:12],
        "target_agent": "audit-agent"})
    if status not in (200, 201):
        die("instance create rejected: %s %s" % (status, out))
    return out["instance_id"]


def send_message(iid, body=None):
    return call("POST", "/v1/instances/%s/messages" % iid,
                {} if body is None else body,
                {"Idempotency-Key": uuid.uuid4().hex})


def instance(iid):
    return call("GET", "/v1/instances/%s" % iid)[1]


def wait_subscriptions_active(iid):
    while True:
        d = instance(iid)
        subs = d.get("subscriptions") or []
        for s in subs:
            if s["state"] == "failed":
                die("publisher subscription %s failed: %s" % (s["publisher_name"], s.get("failure_reason")))
        if subs and all(s["state"] == "active" for s in subs):
            return subs
        time.sleep(0.25)


def messages(iid, query=""):
    return call("GET", "/v1/instances/%s/messages%s" % (iid, query))[1]["messages"]


def frames(iid):
    return call("GET", "/v1/instances/%s/frames" % iid)[1]["frames"]


def nodes(iid):
    out = call("GET", "/v1/instances/%s/nodes" % iid)[1]
    return out["nodes"] if isinstance(out, dict) else out


def node_runs(iid):
    return {n["node_type"]: n["run_summary"]["fresh_count"] + n["run_summary"]["failed_count"]
            for n in nodes(iid) if n.get("node_type")}


def wait_until(fn, note=""):
    while True:
        value = fn()
        if value:
            return value
        time.sleep(0.25)


def wait_node_ran(iid, node_type, count=1):
    return wait_until(lambda: node_runs(iid).get(node_type, 0) >= count)


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + str(detail)[:300]) if detail else ""))


def finish():
    teardown()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


import shutil
import tempfile

SLUG = "subscriber-lineage-receiver"
HERE = os.path.dirname(os.path.abspath(__file__))
NAMESPACE = "audit-governance-platform"
TOKEN = "receiver-bearer-token"
CHECKS = {"type": "array", "default": [{"kind": "row_count_absolute", "config": {"min": 0}}]}

SPEC = {
    "name": "exp-subscriber-lineage-receiver",
    "version": "1",
    "messages": [{"type": "ops/kick"}],
    "nodes": [
        {"type": "producer", "executor": "verifier-shape-checks",
         "subscribes": [{"node": "ops/kick", "type": "terminal/success", "force_upstream_refresh": False}],
         "claim_producers": [{"name": "claim-producer-filesystem", "selector": "data",
                              "intent": "rw", "alias": "held"}],
         "error_types": {"acquire/unavailable": {"action": "give_up"}},
         "attributes": {"schema": {"type": "object", "properties": {
             "checks": CHECKS, "verifier_rows": {"type": "integer", "default": 0}}}}},
        {"type": "consumer", "kind": "attribute_passthrough",
         "subscribes": [{"node": "producer", "type": "terminal/success", "force_upstream_refresh": False},
                        {"node": "producer", "type": "attribute/verifier_rows/changed",
                         "force_upstream_refresh": False}],
         "attributes": {"schema": {"type": "object", "properties": {
             "seen": {"type": "integer", "source": "{{nodes.producer.attribute.verifier_rows}}"}}}}},
    ],
}


def main():
    root = tempfile.mkdtemp(prefix="rimsky-exp-openlineage-")
    STATE["tmp"].append(root)
    os.makedirs(os.path.join(root, "data"))
    with open(os.path.join(root, "cp.yml"), "w") as fh:
        fh.write("root: /workspace\nhost: 127.0.0.1\ngrpc_port: 9200\n"
                 "http_port: 9210\nsweep_interval_seconds: 60\n")

    net = network(SLUG)
    dsn = "postgres://u:p@rimsky-db:5432/rimsky?sslmode=disable"
    pg = "rimsky-exp-ol-pg-" + uuid.uuid4().hex[:6]
    run_container(pg, "postgres:16-alpine", net=net, alias="rimsky-db",
                  env={"POSTGRES_USER": "u", "POSTGRES_PASSWORD": "p", "POSTGRES_DB": "rimsky"})
    while docker("exec", pg, "pg_isready", "-U", "u", "-d", "rimsky").returncode != 0:
        time.sleep(0.3)

    recv_port = free_port()
    run_container("rimsky-exp-ol-receiver-" + uuid.uuid4().hex[:6], "python:3.12-alpine", net=net,
                  alias="lineage-receiver", ports=[(recv_port, 8080)],
                  volumes=["%s/receiver.py:/srv/receiver.py:ro" % HERE],
                  cmd=["python3", "/srv/receiver.py"])
    recv = "http://127.0.0.1:%d" % recv_port

    def received():
        try:
            return call("GET", "/_events", base=recv)[1]
        except Exception:
            return None

    wait_until(lambda: received() is not None)
    check("an external lineage receiver is reachable and holding nothing yet", received() == [])

    boot_rimsky(SLUG, net=net, alias="rimsky", config_path=os.path.join(HERE, "rimsky.yml"),
                env={"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/etc/rimsky/cp.yml"},
                volumes=["%s/cp.yml:/etc/rimsky/cp.yml:ro" % root, "%s:/workspace:rw" % root])

    subscriber = "rimsky-exp-ol-subscriber-" + uuid.uuid4().hex[:6]
    run_container(subscriber, image("rimsky-subscriber-openlineage"), net=net, alias="openlineage", env={
        "RIMSKY_OPENLINEAGE_RIMSKY_DSN": dsn,
        "RIMSKY_OPENLINEAGE_BACKEND_URL": "http://lineage-receiver:8080",
        "RIMSKY_OPENLINEAGE_NAMESPACE": NAMESPACE,
        "RIMSKY_OPENLINEAGE_BEARER_TOKEN": TOKEN,
        "RIMSKY_OPENLINEAGE_POLL_INTERVAL": "1s",
        "RIMSKY_OPENLINEAGE_LAG_WINDOW": "1s",
    })
    check("the bundled subscriber starts from environment configuration alone",
          bool(wait_until(lambda: "openlineage.starting" in logs(subscriber) or None)),
          NAMESPACE)

    tid = deploy(SPEC)
    iid = new_instance(tid)
    send_message(iid, {"type": "ops/kick"})
    wait_until(lambda: node_runs(iid).get("consumer", 0) >= 1 or None)

    def jobs():
        return sorted(e["event"]["job"]["name"] for e in (received() or []))

    wait_until(lambda: ("claim-producer-filesystem.commit" in jobs()
                        and "consumer" in jobs() and "producer" in jobs()) or None)
    evs = received()
    check("run-lineage records reach the external receiver without a custom subscriber",
          len(evs) >= 3, json.dumps(jobs()))
    check("every delivered record is a well-formed OpenLineage run event",
          all(e["event"].get("eventType") and e["event"].get("eventTime")
              and e["event"].get("producer") and e["event"].get("schemaURL")
              and e["event"]["run"].get("runId") and e["event"]["job"].get("name")
              and e["path"] == "/api/v1/lineage" for e in evs),
          "%d events checked" % len(evs))
    check("every delivered record carries the namespace the operator configured",
          set(e["event"]["job"]["namespace"] for e in evs) == {NAMESPACE},
          json.dumps(sorted(set(e["event"]["job"]["namespace"] for e in evs))))
    check("every delivery carries the bearer credential the operator configured",
          set(e["authorization"] for e in evs) == {"Bearer " + TOKEN},
          json.dumps(sorted(set(e["authorization"] for e in evs))))

    node_events = [e["event"] for e in evs if e["event"]["job"]["name"] in ("producer", "consumer")]
    check("the run DAG surfaces as one job per graph node with rimsky run facets",
          len(node_events) >= 2
          and all(ev["run"]["facets"]["rimsky"].get("frame_id") for ev in node_events),
          json.dumps(sorted(ev["job"]["name"] for ev in node_events)))
    consumer_ev = [ev for ev in node_events if ev["job"]["name"] == "consumer"][0]
    refs = consumer_ev["run"]["facets"]["rimsky"].get("substitution_refs") or []
    check("the data lineage between the runs travels with the event",
          any(r.get("source_kind") == "run" for r in refs), json.dumps(refs))

    claim_events = [e["event"] for e in evs if e["event"]["job"]["name"].startswith("claim-producer-")]
    check("the claim the work committed surfaces as an output dataset",
          claim_events and all(ev["outputs"][0]["namespace"] == "claim-producer-filesystem"
                               for ev in claim_events),
          json.dumps([ev["job"]["name"] for ev in claim_events]))
    held = [ev for ev in node_events if ev.get("inputs")]
    check("the claim the work held surfaces as an input dataset",
          held and held[0]["inputs"][0]["namespace"] == "claim-producer-filesystem",
          json.dumps([ev["job"]["name"] for ev in held]))

    before = len(evs)
    ids_before = [e["event"]["run"]["runId"] + "|" + e["event"]["job"]["name"] for e in evs]
    docker("restart", subscriber)
    wait_until(lambda: logs(subscriber).count("openlineage.starting") >= 2 or None)
    iid2 = new_instance(tid)
    send_message(iid2, {"type": "ops/kick"})
    wait_until(lambda: node_runs(iid2).get("consumer", 0) >= 1 or None)
    wait_until(lambda: (len([e for e in (received() or [])
                             if e["event"]["job"]["name"] == "consumer"]) >= 2) or None)
    after = received()
    ids_after = [e["event"]["run"]["runId"] + "|" + e["event"]["job"]["name"] for e in after]
    check("a restarted subscriber delivers the new work and none of the old again",
          len(ids_after) == len(set(ids_after)) and ids_after[:before] == ids_before,
          "%d before, %d after, %d distinct" % (before, len(ids_after), len(set(ids_after))))
    finish()


try:
    main()
finally:
    teardown()
    for d in STATE["tmp"]:
        shutil.rmtree(d, ignore_errors=True)
