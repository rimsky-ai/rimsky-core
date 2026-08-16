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

SLUG = "assumption-http-idorkey-accepted-uniformly"
TAG = os.environ.get("RIMSKY_IMAGE_TAG") or sys.exit("export RIMSKY_IMAGE_TAG=src-<tree hash> first")
IMAGE = "rimsky-all-in-one:" + TAG
NAME = "exp-" + SLUG
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
STATE = {"base": None, "checks": []}
KEY = "idorkey-a"

ACCEPTS_KEY = [("GET", ""), ("GET", "/nodes"), ("GET", "/breakpoints"), ("GET", "/breakpoint-hits")]

REJECTS_KEY = [("GET", "/frames"), ("GET", "/frames/{u}"), ("GET", "/messages"),
               ("POST", "/messages"), ("GET", "/assets"), ("GET", "/assets/w.x"),
               ("GET", "/assets/w.x/versions"), ("GET", "/assets/w.x/materialization-history"),
               ("DELETE", "/assets/w.x"), ("POST", "/debug/override")]


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def die(msg):
    print("HARNESS ERROR: " + msg)
    docker("rm", "-f", NAME)
    sys.exit(2)


def call(method, path, body=None, token=None):
    headers = {"Content-Type": "application/json", "Idempotency-Key": uuid.uuid4().hex}
    if token:
        headers["Authorization"] = "Bearer " + token
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(STATE["base"] + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, resp.read().decode()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def boot():
    if docker("image", "inspect", IMAGE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % IMAGE)
    docker("rm", "-f", NAME)
    port = free_port()
    res = docker("run", "-d", "--name", NAME, "-p", "127.0.0.1:%d:8080" % port, IMAGE)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                return
        except Exception:
            pass
        if docker("inspect", "-f", "{{.State.Running}}", NAME).stdout.strip() != "true":
            die("container exited during boot:\n" + docker("logs", NAME).stdout + docker("logs", NAME).stderr)
        time.sleep(0.3)


def bootstrap_auth():
    env = dict(os.environ, HOME=tempfile.mkdtemp(prefix="rimsky-exp-"))
    env.pop("RIMSKY_API_KEY", None)
    env.pop("RIMSKY_CONTROL_API_URL", None)
    out = subprocess.run([CLI, "auth", "init", "--endpoint", STATE["base"]],
                         capture_output=True, text=True, env=env).stdout
    for line in out.splitlines():
        if "RIMSKY_API_KEY" in line and "for subsequent" in line:
            return line.split("RIMSKY_API_KEY=")[1].split(" ")[0].strip('"')
    die("could not read the admin plaintext out of:\n" + out)


def finish():
    docker("rm", "-f", NAME)
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


def main():
    boot()
    admin = bootstrap_auth()
    unknown = "11111111-1111-1111-1111-111111111111"

    _, text = call("POST", "/v1/templates", {"spec": {
        "name": "idorkey-probe", "version": "1",
        "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}}, admin)
    template_id = json.loads(text)["template_id"]
    call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    _, text = call("POST", "/v1/instances", {"template": template_id, "instance_key": KEY,
                                             "target_agent": "audit-agent"}, admin)
    instance_id = json.loads(text)["instance_id"]

    print("== the {idOrKey} routes take the instance key, exactly as spelled ==")
    for method, suffix in ACCEPTS_KEY:
        body = {} if method == "POST" else None
        by_id, _ = call(method, "/v1/instances/%s%s" % (instance_id, suffix.format(u=unknown)),
                        body, admin)
        by_key, text = call(method, "/v1/instances/%s%s" % (KEY, suffix.format(u=unknown)),
                            body, admin)
        check("%-6s /v1/instances/{key}%-22s answers the same as by id" % (method, suffix),
              by_key == by_id and by_key == 200, "id=%s key=%s %s" % (by_id, by_key, text[:60]))
    def reset(paused):
        call("POST", "/v1/instances/%s/%s" % (instance_id, "pause" if paused else "resume"), {}, admin)

    for suffix, precondition in (("/pause", False), ("/resume", True)):
        reset(precondition)
        by_id, _ = call("POST", "/v1/instances/%s%s" % (instance_id, suffix), {}, admin)
        reset(precondition)
        by_key, text = call("POST", "/v1/instances/%s%s" % (KEY, suffix), {}, admin)
        check("POST   /v1/instances/{key}%-22s answers the same as by id" % suffix,
              by_key == by_id == 200, "id=%s key=%s %s" % (by_id, by_key, text[:60]))
    reset(False)
    by_id, _ = call("POST", "/v1/instances/%s/breakpoints" % instance_id,
                    {"node_type": "w", "checkpoint": "before_dispatch"}, admin)
    by_key, text = call("POST", "/v1/instances/%s/breakpoints" % KEY,
                        {"node_type": "w", "checkpoint": "before_dispatch"}, admin)
    check("POST   /v1/instances/{key}/breakpoints          answers the same as by id",
          by_key == by_id == 201, "id=%s key=%s" % (by_id, by_key))

    print("")
    print("== PRIOR CONTRADICTED: the {id} routes reject the key with 400 ==")
    for method, suffix in REJECTS_KEY:
        body = {} if method == "POST" else None
        by_id, id_text = call(method, "/v1/instances/%s%s" % (instance_id, suffix.format(u=unknown)),
                              body, admin)
        by_key, key_text = call(method, "/v1/instances/%s%s" % (KEY, suffix.format(u=unknown)),
                                body, admin)
        check("%-6s /v1/instances/{key}%-36s 400 \"invalid instance id\"" % (method, suffix),
              by_key == 400 and json.loads(key_text)["error"] == "invalid instance id",
              "by id=%s %s | by key=%s %s" % (by_id, id_text[:40], by_key, key_text[:40]))

    print("")
    print("== the two spellings answer differently for the same unknown name ==")
    by_key, key_text = call("GET", "/v1/instances/never-created", None, admin)
    check("GET /v1/instances/{unknown key}        answers 404 instance not found",
          by_key == 404, "%s %s" % (by_key, key_text[:60]))
    by_key, key_text = call("GET", "/v1/instances/never-created/frames", None, admin)
    check("GET /v1/instances/{unknown key}/frames answers 400 invalid instance id",
          by_key == 400, "%s %s" % (by_key, key_text[:60]))

    print("")
    print("== terminate and delete, the teardown pair, do take the key ==")
    status, _ = call("POST", "/v1/instances/%s/terminate" % KEY, {}, admin)
    check("POST   /v1/instances/{key}/terminate           answers 200", status == 200, str(status))
    while True:
        status, text = call("DELETE", "/v1/instances/" + KEY, None, admin)
        if status != 409:
            break
        time.sleep(0.25)
    check("DELETE /v1/instances/{key}                     answers 200", status == 200,
          "%s %s" % (status, text[:60]))

    finish()


main()
