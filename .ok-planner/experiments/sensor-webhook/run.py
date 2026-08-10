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


import hashlib
import hmac

SLUG = "sensor-webhook"
HERE = os.path.dirname(os.path.abspath(__file__))
HMAC_SECRET = "sensor-webhook-shared-secret"
HEADER_SECRET = "header-mode-secret"

SPEC = {
    "name": "exp-sensor-webhook",
    "version": "1",
    "messages": [{"type": "hook/hmac"}, {"type": "hook/secret"}],
    "nodes": [{
        "type": "handler",
        "kind": "attribute_passthrough",
        "subscribes": [
            {"node": "hook/hmac", "type": "terminal/success", "force_upstream_refresh": False},
            {"node": "hook/secret", "type": "terminal/success", "force_upstream_refresh": False},
        ],
        "attributes": {"schema": {"type": "object", "properties": {"v": {"type": "integer", "default": 1}}}},
    }],
    "publishers": [
        {"name": "hook-hmac", "kind": "webhook", "message_type": "hook/hmac",
         "config": {"path_prefix": "/hooks/hmac", "idempotency_header": "X-Delivery-Id",
                    "auth": {"mode": "hmac", "secret": HMAC_SECRET,
                             "timestamp_header": "X-Rimsky-Timestamp",
                             "replay_window_seconds": 60}}},
        {"name": "hook-secret", "kind": "webhook", "message_type": "hook/secret",
         "config": {"path_prefix": "/hooks/secret",
                    "auth": {"mode": "secret_header", "header": "X-Token", "secret": HEADER_SECRET}}},
    ],
}


def by_type(iid, mtype):
    return sorted([m for m in messages(iid) if m["type"] == mtype], key=lambda m: m["received_at"])


def signed(body, ts, secret=HMAC_SECRET):
    mac = hmac.new(secret.encode(), (str(ts) + ".").encode() + body, hashlib.sha256)
    return "sha256=" + mac.hexdigest()


def main():
    net = network(SLUG)
    hook_port = free_port()
    sensor = "rimsky-exp-webhook-sensor-" + uuid.uuid4().hex[:6]
    run_container(sensor, image("rimsky-sensor-webhook"), net=net, alias="webhook-sensor",
                  ports=[(hook_port, 9184)], env={
                      "RIMSKY_SENSOR_WEBHOOK_PORT": "9084",
                      "RIMSKY_SENSOR_WEBHOOK_HTTP_PORT": "9184",
                      "RIMSKY_CONTROL_API_URL": "http://rimsky:8080",
                  })
    hook = "http://127.0.0.1:%d" % hook_port

    def health():
        try:
            return call("GET", "/health", base=hook)[0] == 200 or None
        except Exception:
            return None

    wait_until(health)
    check("the sensor serves a health route an external caller can reach", bool(health()))

    boot_rimsky(SLUG, net=net, alias="rimsky", config_path=os.path.join(HERE, "rimsky.yml"))
    tid = deploy(SPEC)
    iid = new_instance(tid)
    subs = wait_subscriptions_active(iid)
    check("both declared webhook subscriptions mount live on the instance",
          len(subs) == 2 and all(s["kind"] == "webhook" for s in subs),
          json.dumps(sorted((s["publisher_name"], s["state"]) for s in subs)))

    body = json.dumps({"event": "order.created", "id": 41}).encode()
    status, _ = raw_post(hook + "/hooks/secret", body,
                         {"Content-Type": "application/json", "X-Token": HEADER_SECRET})
    got = by_type(iid, "hook/secret")
    check("an authenticated POST is accepted on the route the subscription declared", status == 200, status)
    check("the POST has already become a message for the target instance when it returns",
          len(got) == 1 and got[0]["sender_kind"] == "publisher"
          and got[0]["payload"]["body"] == {"event": "order.created", "id": 41}
          and got[0]["payload"]["path"] == "/hooks/secret",
          json.dumps(got[0]["payload"]) if got else "no message")
    wait_node_ran(iid, "handler", 1)
    check("the inbound POST drove the subscribed node to run",
          node_runs(iid).get("handler", 0) >= 1, json.dumps(node_runs(iid)))

    status, _ = raw_post(hook + "/hooks/secret", body, {"Content-Type": "application/json"})
    check("a POST with no credential is refused", status == 401, status)
    status, _ = raw_post(hook + "/hooks/secret", body,
                         {"Content-Type": "application/json", "X-Token": "wrong"})
    check("a POST with the wrong credential is refused", status == 401, status)
    check("neither refused POST became a message", len(by_type(iid, "hook/secret")) == 1,
          json.dumps([m["payload"]["body"] for m in by_type(iid, "hook/secret")]))

    ts = int(time.time())
    hbody = json.dumps({"event": "order.shipped", "id": 42}).encode()
    status, _ = raw_post(hook + "/hooks/hmac", hbody, {
        "Content-Type": "application/json", "X-Rimsky-Timestamp": str(ts),
        "X-Rimsky-Signature": signed(hbody, ts), "X-Delivery-Id": "delivery-1"})
    hgot = by_type(iid, "hook/hmac")
    check("a signed POST is accepted and already a message when it returns",
          status == 200 and len(hgot) == 1
          and hgot[0]["payload"]["body"] == {"event": "order.shipped", "id": 42}
          and hgot[0]["payload"]["idempotency_key"] == "delivery-1",
          "%s %s" % (status, json.dumps(hgot[0]["payload"]) if hgot else ""))

    status, _ = raw_post(hook + "/hooks/hmac", hbody, {
        "Content-Type": "application/json", "X-Rimsky-Timestamp": str(ts),
        "X-Rimsky-Signature": signed(hbody, ts, "the-wrong-secret"), "X-Delivery-Id": "delivery-2"})
    check("a POST signed with the wrong secret is refused", status == 401, status)
    stale = int(time.time()) - 3600
    status, _ = raw_post(hook + "/hooks/hmac", hbody, {
        "Content-Type": "application/json", "X-Rimsky-Timestamp": str(stale),
        "X-Rimsky-Signature": signed(hbody, stale), "X-Delivery-Id": "delivery-3"})
    check("a correctly signed POST replayed outside the declared window is refused", status == 401, status)

    status, _ = raw_post(hook + "/hooks/hmac", hbody, {
        "Content-Type": "application/json", "X-Rimsky-Timestamp": str(int(time.time())),
        "X-Rimsky-Signature": signed(hbody, int(time.time())), "X-Delivery-Id": "delivery-1"})
    check("a redelivery carrying an already-seen delivery id is accepted but not re-sent",
          status == 200 and len(by_type(iid, "hook/hmac")) == 1,
          "%s %d" % (status, len(by_type(iid, "hook/hmac"))))

    status, _ = raw_post(hook + "/hooks/unbound", hbody, {"Content-Type": "application/json"})
    check("a POST to a path no subscription declared is refused", status == 404, status)
    check("every message on the instance came from the webhook sensor, none from polling",
          messages(iid) and all(m["sender_kind"] == "publisher" for m in messages(iid)),
          json.dumps(sorted(set(m["type"] for m in messages(iid)))))
    finish()


try:
    main()
finally:
    teardown()
