import json
import os
import socket
import subprocess
import sys
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
NET = "exp-assumption-sensor-auth-net-" + SUFFIX
SINK = "exp-assumption-sensor-auth-sink-" + SUFFIX
SECRET = "s3cret-probe-token"
HEADER = "X-Probe-Token"
POLL_URL = "http://%s:8000/poll/document" % SINK
STATE = {"base": None}
CHECKS = []
CONTAINERS = []
NETWORKS = []

SPEC = {
    "name": "exp-assumption-sensor-auth",
    "version": "1",
    "messages": [{"type": "poll/obs"}, {"type": "hook/authed"}, {"type": "hook/plain"}],
    "nodes": [{
        "type": "reactor",
        "kind": "attribute_passthrough",
        "subscribes": [{"node": "poll/obs", "type": "terminal/success", "force_upstream_refresh": False}],
        "attributes": {"schema": {"type": "object", "properties": {"v": {"type": "integer", "default": 1}}}},
    }],
    "publishers": [
        {"name": "poll-with-auth", "kind": "http", "message_type": "poll/obs",
         "config": {"url": POLL_URL, "poll_interval": "1s",
                    "auth": {"mode": "secret_header", "header": HEADER, "secret": SECRET}}},
        {"name": "hook-with-auth", "kind": "webhook", "message_type": "hook/authed",
         "config": {"path_prefix": "/authed",
                    "auth": {"mode": "secret_header", "header": HEADER, "secret": SECRET}}},
        {"name": "hook-without-auth", "kind": "webhook", "message_type": "hook/plain",
         "config": {"path_prefix": "/plain"}},
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


def run_detached(name, ref, env=None, publish=None, mounts=None, network=None,
                 alias=None, command=None):
    docker("rm", "-f", name)
    args = ["run", "-d", "--name", name]
    if network:
        args += ["--network", network, "--network-alias", alias or name]
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


def raw_post(url, body, headers=None):
    req = urllib.request.Request(url, data=body, headers=headers or {}, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()
    except Exception as exc:
        return 0, str(exc)


def sink_requests(port):
    with urllib.request.urlopen("http://127.0.0.1:%d/_log" % port, timeout=30) as resp:
        return json.loads(resp.read().decode())["requests"]


def wait_until(fn):
    while True:
        value = fn()
        if value:
            return value
        time.sleep(0.25)


docker("network", "rm", NET)
if docker("network", "create", NET).returncode != 0:
    die("docker network create failed")
NETWORKS.append(NET)

sink_port = free_port()
run_detached(SINK, "python:3.12-alpine", network=NET, publish=[(sink_port, 8000)],
             mounts=[(os.path.join(HERE, "sink.py"), "/srv/sink.py")],
             command=["python3", "/srv/sink.py"])
while True:
    try:
        sink_requests(sink_port)
        break
    except Exception:
        if not running(SINK):
            die("sink exited:\n" + logs(SINK)[-1000:])
        time.sleep(0.2)

run_detached("exp-assumption-sensor-auth-http-" + SUFFIX, image("rimsky-sensor-http"),
             network=NET, alias="http-sensor",
             env={"RIMSKY_SENSOR_HTTP_PORT": "9082",
                  "RIMSKY_CONTROL_API_URL": "http://rimsky:8080",
                  "RIMSKY_SENSOR_HTTP_EGRESS_ALLOWLIST": "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"})
hook_port = free_port()
run_detached("exp-assumption-sensor-auth-webhook-" + SUFFIX, image("rimsky-sensor-webhook"),
             network=NET, alias="webhook-sensor", publish=[(hook_port, 9184)],
             env={"RIMSKY_SENSOR_WEBHOOK_PORT": "9084",
                  "RIMSKY_SENSOR_WEBHOOK_HTTP_PORT": "9184",
                  "RIMSKY_CONTROL_API_URL": "http://rimsky:8080"})
hook = "http://127.0.0.1:%d" % hook_port

api_port = free_port()
rimsky = run_detached("exp-assumption-sensor-auth-rimsky-" + SUFFIX, image("rimsky-all-in-one"),
                      network=NET, alias="rimsky", publish=[(api_port, 8080)],
                      mounts=[(os.path.join(HERE, "rimsky.yml"), "/etc/rimsky/rimsky.yml")])
STATE["base"] = "http://127.0.0.1:%d" % api_port
while True:
    if not running(rimsky):
        die("rimsky exited during boot:\n" + logs(rimsky)[-1500:])
    if call("GET", "/v1/health")[0] == 200:
        break
    time.sleep(0.3)

print("== a template carrying the same auth block on two publisher kinds ==")
status, out = call("POST", "/v1/templates", {"spec": SPEC})
check("registration accepts an auth block on an http publisher",
      status in (200, 201), "%s %s" % (status, json.dumps(out)[:200]))
if status not in (200, 201):
    finish()
tid = out["template_id"]
status, out = call("POST", "/v1/templates/%s/deploy" % tid, {})
check("and deploying it is accepted too", status in (200, 201), "%s %s" % (status, json.dumps(out)[:200]))

status, out = call("POST", "/v1/instances", {
    "template": tid, "instance_key": "exp-" + uuid.uuid4().hex[:12], "target_agent": "audit-agent"})
if status not in (200, 201):
    die("instance create rejected: %s %s" % (status, out))
iid = out["instance_id"]

def subs():
    return {s["publisher_name"]: s for s in (call("GET", "/v1/instances/%s" % iid)[1].get("subscriptions") or [])}

def settle(name):
    while True:
        state = (subs().get(name) or {}).get("state")
        if state in ("active", "failed"):
            return state
        text = logs(rimsky)
        if ('"publisher_name":"%s"' % name) in text and "subscribe_failed" in text:
            return "refused"
        time.sleep(0.3)


poll_state = settle("poll-with-auth")
check("the http poll subscription carrying the auth block mounts live",
      poll_state == "active",
      json.dumps({k: v["state"] for k, v in subs().items()}))

print("")
print("== what the poll actually sends ==")
polls = wait_until(lambda: [r for r in sink_requests(sink_port) if r["path"].startswith("/poll/")])
headers = polls[0]["headers"]
check("the sensor polls the upstream as declared", polls[0]["method"] == "GET", polls[0]["path"])
check("and presents none of the credentials the auth block named",
      HEADER.lower() not in headers and "authorization" not in headers
      and SECRET not in json.dumps(headers),
      "request headers: %s" % json.dumps(sorted(headers)))
check("the poll still produced a message, so the block was ignored rather than enforced",
      wait_until(lambda: [m for m in call("GET", "/v1/instances/%s/messages" % iid)[1]["messages"]
                          if m["type"] == "poll/obs"]) is not None,
      "the subscription is live and sending")

print("")
print("== the same block on the webhook publisher ==")
body = json.dumps({"probe": 1}).encode()
status, _ = raw_post(hook + "/authed", body, {"Content-Type": "application/json"})
check("the webhook ingress refuses a delivery with no credential", status not in (200, 201, 202),
      "status %s" % status)
status, _ = raw_post(hook + "/authed", body, {"Content-Type": "application/json", HEADER: SECRET})
check("and accepts the same delivery carrying the header the block named",
      status in (200, 201, 202), "status %s" % status)
plain_state = settle("hook-without-auth")
refusal = next((l for l in logs(rimsky).splitlines()
                if "hook-without-auth" in l and "resolved_config.auth required" in l), "")
check("the webhook publisher declaring no auth block never mounts, and the refusal names the block",
      plain_state != "active" and bool(refusal),
      "state %s | %s" % (plain_state, refusal[:200]))

finish()
