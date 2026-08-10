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

SLUG = "sensor-http"
HERE = os.path.dirname(os.path.abspath(__file__))
URL = "http://poll-target:8000/data.json"
MISSING = "http://poll-target:8000/absent.json"

SPEC = {
    "name": "exp-sensor-http",
    "version": "1",
    "messages": [{"type": "http/obs"}, {"type": "http/filtered"},
                 {"type": "http/notfound"}, {"type": "http/blocked"}],
    "nodes": [{
        "type": "reactor",
        "kind": "attribute_passthrough",
        "subscribes": [{"node": "http/obs", "type": "terminal/success", "force_upstream_refresh": False}],
        "attributes": {"schema": {"type": "object", "properties": {"v": {"type": "integer", "default": 1}}}},
    }],
    "publishers": [
        {"name": "watch", "kind": "http", "message_type": "http/obs",
         "config": {"url": URL, "poll_interval": "1s"}},
        {"name": "watch-filtered", "kind": "http", "message_type": "http/filtered",
         "config": {"url": URL, "poll_interval": "1s",
                    "match": {"jsonpath": {"path": "status", "value": "ready"}}}},
        {"name": "watch-notfound", "kind": "http", "message_type": "http/notfound",
         "config": {"url": MISSING, "poll_interval": "1s"}},
        {"name": "watch-blocked", "kind": "http", "message_type": "http/blocked",
         "config": {"url": URL, "poll_interval": "1s"}},
    ],
}


def by_type(iid, mtype):
    return sorted([m for m in messages(iid) if m["type"] == mtype], key=lambda m: m["received_at"])


def write_body(root, doc):
    path = os.path.join(root, "data.json")
    tmp = path + ".tmp"
    with open(tmp, "w") as fh:
        fh.write(json.dumps(doc))
    os.replace(tmp, path)


def main():
    root = tempfile.mkdtemp(prefix="rimsky-exp-http-")
    STATE["tmp"].append(root)
    write_body(root, {"status": "warming", "n": 1})

    net = network(SLUG)
    run_container("rimsky-exp-http-pg-" + uuid.uuid4().hex[:6], "postgres:16-alpine", net=net, alias="state-db",
                  env={"POSTGRES_USER": "u", "POSTGRES_PASSWORD": "p", "POSTGRES_DB": "sensorstate"})
    pg = STATE["containers"][-1]
    while docker("exec", pg, "pg_isready", "-U", "u", "-d", "sensorstate").returncode != 0:
        time.sleep(0.3)

    run_container("rimsky-exp-http-target-" + uuid.uuid4().hex[:6], "python:3.12-alpine", net=net,
                  alias="poll-target", volumes=["%s:/srv:ro" % root],
                  cmd=["python3", "-m", "http.server", "8000", "--directory", "/srv"])

    private = "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
    sensor = "rimsky-exp-http-sensor-" + uuid.uuid4().hex[:6]
    run_container(sensor, image("rimsky-sensor-http"), net=net, alias="http-sensor", env={
        "RIMSKY_SENSOR_HTTP_PORT": "9082",
        "RIMSKY_CONTROL_API_URL": "http://rimsky:8080",
        "RIMSKY_SENSOR_HTTP_STATE_DSN": "postgres://u:p@state-db:5432/sensorstate?sslmode=disable",
        "RIMSKY_SENSOR_HTTP_EGRESS_ALLOWLIST": private,
    })
    guarded = "rimsky-exp-http-guarded-" + uuid.uuid4().hex[:6]
    run_container(guarded, image("rimsky-sensor-http"), net=net,
                  alias="http-sensor-guarded", env={
                      "RIMSKY_SENSOR_HTTP_PORT": "9082",
                      "RIMSKY_CONTROL_API_URL": "http://rimsky:8080",
                  })

    boot_rimsky(SLUG, net=net, alias="rimsky", config_path=os.path.join(HERE, "rimsky.yml"))
    tid = deploy(SPEC)

    iid = new_instance(tid)
    subs = wait_subscriptions_active(iid)
    check("all four declared HTTP poll subscriptions mount live on the instance",
          len(subs) == 4 and all(s["kind"] == "http" for s in subs),
          json.dumps([(s["publisher_name"], s["state"]) for s in subs]))

    first = wait_until(lambda: (by_type(iid, "http/obs") or [None])[0])
    check("polling a URL sends a message carrying the response the upstream returned",
          first["sender_kind"] == "publisher" and first["payload"]["status"] == 200
          and first["payload"]["url"] == URL and first["payload"]["body"] == {"status": "warming", "n": 1},
          json.dumps(first["payload"]))
    check("the message carries a hash of the body the poll observed",
          bool(first["payload"].get("body_hash")), json.dumps(first["payload"].get("body_hash")))
    wait_node_ran(iid, "reactor", 1)
    check("the poll message drove the subscribed node to run",
          node_runs(iid).get("reactor", 0) >= 1, json.dumps(node_runs(iid)))

    write_body(root, {"status": "warming", "n": 2})
    second = wait_until(lambda: (by_type(iid, "http/obs")[1] if len(by_type(iid, "http/obs")) > 1 else None))
    check("a changed body sends a further message with a different body hash",
          second["payload"]["body"] == {"status": "warming", "n": 2}
          and second["payload"]["body_hash"] != first["payload"]["body_hash"],
          json.dumps(second["payload"]["body"]))

    iid2 = new_instance(tid)
    wait_subscriptions_active(iid2)
    wait_until(lambda: (by_type(iid2, "http/obs") or [None])[0])
    check("an unchanged body does not re-send while the poller keeps polling",
          len(by_type(iid, "http/obs")) == 2,
          json.dumps([m["payload"]["body"] for m in by_type(iid, "http/obs")]))

    docker("restart", sensor)
    iid3 = new_instance(tid)
    wait_subscriptions_active(iid3)
    wait_until(lambda: (by_type(iid3, "http/obs") or [None])[0])
    check("a restarted sensor polling the same unchanged body still does not re-send",
          len(by_type(iid, "http/obs")) == 2 and len(by_type(iid2, "http/obs")) == 1,
          "instance1=%d instance2=%d" % (len(by_type(iid, "http/obs")), len(by_type(iid2, "http/obs"))))

    write_body(root, {"status": "ready", "n": 3})
    filt = wait_until(lambda: (by_type(iid, "http/filtered") or [None])[0])
    check("a subscription filtered on the response body sends only the body that satisfies the filter",
          filt["payload"]["body"]["status"] == "ready" and len(by_type(iid, "http/filtered")) == 1,
          json.dumps([m["payload"]["body"] for m in by_type(iid, "http/filtered")]))
    wait_until(lambda: len(by_type(iid, "http/obs")) >= 3 or None)
    check("the unfiltered subscription on the same URL sent every changed body",
          len(by_type(iid, "http/obs")) == 3,
          json.dumps([m["payload"]["body"] for m in by_type(iid, "http/obs")]))
    check("a URL that never answers with success sends nothing while the successful one sends three",
          len(by_type(iid, "http/notfound")) == 0, json.dumps(by_type(iid, "http/notfound")))
    check("a private-network poll target is unreachable until the operator allowlists it",
          len(by_type(iid, "http/blocked")) == 0
          and "poll_dial_failed" in logs(guarded),
          json.dumps(by_type(iid, "http/blocked")))
    finish()


try:
    main()
finally:
    teardown()
    for d in STATE["tmp"]:
        shutil.rmtree(d, ignore_errors=True)
