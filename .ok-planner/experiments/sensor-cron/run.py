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


SLUG = "sensor-cron"
HERE = os.path.dirname(os.path.abspath(__file__))
CRON = "* * * * *"

SPEC = {
    "name": "exp-sensor-cron",
    "version": "1",
    "messages": [{"type": "tick/fire"}],
    "nodes": [{
        "type": "reactor",
        "kind": "attribute_passthrough",
        "subscribes": [{"node": "tick/fire", "type": "terminal/success", "force_upstream_refresh": False}],
        "attributes": {"schema": {"type": "object", "properties": {
            "v": {"type": "integer", "default": 1}}}},
    }],
    "publishers": [{"name": "tick", "kind": "cron", "config": {"cron": CRON}, "message_type": "tick/fire"}],
}


def publisher_messages(iid):
    return [m for m in messages(iid, "?sender_kind=publisher")]


def next_minute_boundary():
    return int(time.time() // 60 + 1) * 60


def iso(ts):
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(ts))


def main():
    net = network(SLUG)
    run_container("rimsky-exp-cron-pg-" + uuid.uuid4().hex[:6], "postgres:16-alpine", net=net, alias="state-db",
                  env={"POSTGRES_USER": "u", "POSTGRES_PASSWORD": "p", "POSTGRES_DB": "sensorstate"})
    pg = STATE["containers"][-1]
    while docker("exec", pg, "pg_isready", "-U", "u", "-d", "sensorstate").returncode != 0:
        time.sleep(0.3)

    sensor = "rimsky-exp-cron-sensor-" + uuid.uuid4().hex[:6]
    run_container(sensor, image("rimsky-sensor-cron"), net=net, alias="cron-sensor", env={
        "RIMSKY_SENSOR_CRON_PORT": "9081",
        "RIMSKY_CONTROL_API_URL": "http://rimsky:8080",
        "RIMSKY_SENSOR_CRON_STATE_DSN": "postgres://u:p@state-db:5432/sensorstate?sslmode=disable",
    })
    boot_rimsky(SLUG, net=net, alias="rimsky", config_path=os.path.join(HERE, "rimsky.yml"))

    tid = deploy(SPEC)

    iid = new_instance(tid)
    subs = wait_subscriptions_active(iid)
    check("the template's cron publisher mounts a live subscription on the instance",
          len(subs) == 1 and subs[0]["kind"] == "cron" and subs[0]["message_type"] == "tick/fire",
          json.dumps(subs))

    msgs = wait_until(lambda: publisher_messages(iid) or None)
    m = msgs[0]
    check("the cron sensor sends a message with no operator action and no external scheduler",
          m["sender_kind"] == "publisher" and m["type"] == "tick/fire", json.dumps(m))
    check("the sent message echoes the cron expression the operator declared",
          m["payload"].get("cron") == CRON, json.dumps(m["payload"]))
    fire_at = m["payload"].get("fire_at", "")
    check("the firing lands on the schedule the expression names (a whole minute)",
          fire_at.endswith(":00Z") and m["payload"].get("missed_windows") == 0, fire_at)
    check("no operator message was ever posted to this instance",
          all(x["sender_kind"] != "operator" for x in messages(iid)),
          json.dumps([x["sender_kind"] for x in messages(iid)]))
    wait_node_ran(iid, "reactor", 1)
    check("the cron message drove the subscribed node to run", node_runs(iid).get("reactor", 0) >= 1,
          json.dumps(node_runs(iid)))

    while time.gmtime().tm_sec >= 15:
        time.sleep(0.5)
    iid2 = new_instance(tid)
    wait_subscriptions_active(iid2)
    window = next_minute_boundary()
    docker("stop", sensor)
    while time.time() <= window + 3:
        time.sleep(0.5)
    restart_at = time.time()
    docker("start", sensor)
    want = iso(window)

    def recovered():
        for x in publisher_messages(iid2):
            if x["payload"].get("fire_at") == want and x["received_at"] > iso(restart_at):
                return x
        return None

    rec = wait_until(recovered)
    check("a sensor restarted after a missed window fires for the window it had recorded",
          rec["payload"]["fire_at"] == want, json.dumps(rec["payload"]))
    check("the recovered firing was sent after the restart, not before the stop",
          rec["received_at"] > iso(restart_at), "%s > %s" % (rec["received_at"], iso(restart_at)))
    wait_node_ran(iid2, "reactor", 1)
    check("the recovered firing drove the subscribed node to run",
          node_runs(iid2).get("reactor", 0) >= 1, json.dumps(node_runs(iid2)))
    finish()


try:
    main()
finally:
    teardown()
