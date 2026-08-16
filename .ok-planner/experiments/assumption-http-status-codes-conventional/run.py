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

SLUG = "assumption-http-status-codes-conventional"
TAG = os.environ.get("RIMSKY_IMAGE_TAG") or sys.exit("export RIMSKY_IMAGE_TAG=src-<tree hash> first")
IMAGE = "rimsky-all-in-one:" + TAG
NAME = "exp-" + SLUG
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
STATE = {"base": None, "checks": []}
UNKNOWN = "11111111-1111-1111-1111-111111111111"

TPL = {"name": "status-probe", "version": "1",
       "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}


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


def status_is(label, want, method, path, body=None, token=None):
    got, text = call(method, path, body, token)
    check(label, got == want, "want %s got %s %s" % (want, got, text[:70]))


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

    def plaintext(out):
        for line in out.splitlines():
            if "RIMSKY_API_KEY" in line and "for subsequent" in line:
                return line.split("RIMSKY_API_KEY=")[1].split(" ")[0].strip('"')
        die("could not read a key plaintext out of:\n" + out)

    admin = plaintext(subprocess.run(
        [CLI, "auth", "init", "--endpoint", STATE["base"]],
        capture_output=True, text=True, env=env).stdout)
    reader = plaintext(subprocess.run(
        [CLI, "auth", "create-key", "--endpoint", STATE["base"], "--key", admin,
         "--name", "reader", "--role", "read-only"],
        capture_output=True, text=True, env=env).stdout)
    return admin, reader


def finish():
    docker("rm", "-f", NAME)
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


def main():
    boot()
    admin, reader = bootstrap_auth()

    _, text = call("POST", "/v1/templates", {"spec": TPL}, admin)
    template_id = json.loads(text)["template_id"]
    call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    call("POST", "/v1/tags", {"tag": "status:v1", "template": template_id}, admin)
    _, text = call("POST", "/v1/instances", {"template": template_id, "instance_key": "status-a",
                                             "target_agent": "audit-agent"}, admin)
    instance_id = json.loads(text)["instance_id"]

    print("== missing token yields 401 ==")
    for method, path, body in [("GET", "/v1/instances", None), ("GET", "/v1/templates", None),
                               ("GET", "/v1/events", None), ("GET", "/v1/audit", None),
                               ("GET", "/v1/tags", None), ("GET", "/v1/auth/keys", None),
                               ("GET", "/v1/auth/whoami", None),
                               ("GET", "/v1/observability/instances", None),
                               ("GET", "/v1/nodes/" + UNKNOWN, None),
                               ("POST", "/v1/templates", {"spec": TPL}),
                               ("DELETE", "/v1/tags/status:v1", None)]:
        status_is("401 no token   %-6s %s" % (method, path), 401, method, path, body)
    status_is("401 invalid token GET /v1/instances", 401, "GET", "/v1/instances", None, "rk_bogus")
    status_is("200 GET /v1/health stays open with no token", 200, "GET", "/v1/health")

    print("")
    print("== insufficient permission yields 403 ==")
    for method, path, body in [("POST", "/v1/templates", {"spec": TPL}),
                               ("POST", "/v1/tags", {"tag": "x:y", "template": template_id}),
                               ("DELETE", "/v1/tags/status:v1", None),
                               ("DELETE", "/v1/templates/" + template_id, None),
                               ("POST", "/v1/auth/keys", {"name": "n"}),
                               ("POST", "/v1/admin/lineage/prune", {}),
                               ("POST", "/v1/instances", {"template": template_id}),
                               ("POST", "/v1/instances/%s/terminate" % instance_id, {}),
                               ("DELETE", "/v1/instances/" + instance_id, None),
                               ("POST", "/v1/nodes/%s/reset" % UNKNOWN, {})]:
        status_is("403 read-only  %-6s %s" % (method, path), 403, method, path, body, reader)
    status_is("200 read-only may read instances", 200, "GET", "/v1/instances", None, reader)

    print("")
    print("== unknown id yields 404 ==")
    for method, path, body in [("GET", "/v1/instances/" + UNKNOWN, None),
                               ("GET", "/v1/instances/%s/nodes" % UNKNOWN, None),
                               ("GET", "/v1/instances/%s/frames" % UNKNOWN, None),
                               ("GET", "/v1/instances/%s/assets" % UNKNOWN, None),
                               ("GET", "/v1/instances/%s/breakpoints" % UNKNOWN, None),
                               ("GET", "/v1/templates/nosuch", None),
                               ("GET", "/v1/nodes/" + UNKNOWN, None),
                               ("GET", "/v1/runs/" + UNKNOWN, None),
                               ("GET", "/v1/messages/" + UNKNOWN, None),
                               ("GET", "/v1/auth/keys/nosuch", None),
                               ("GET", "/v1/lineage/runs/" + UNKNOWN, None),
                               ("GET", "/v1/observability/instances/" + UNKNOWN, None),
                               ("GET", "/v1/observability/executors/nosuch", None),
                               ("POST", "/v1/instances/%s/pause" % UNKNOWN, {}),
                               ("POST", "/v1/instances/%s/terminate" % UNKNOWN, {}),
                               ("POST", "/v1/instances/%s/messages" % UNKNOWN, {}),
                               ("DELETE", "/v1/instances/" + UNKNOWN, None),
                               ("PUT", "/v1/tags/nosuch", {"template": template_id})]:
        status_is("404 unknown id %-6s %s" % (method, path), 404, method, path, body, admin)

    print("")
    print("== a conflicting write yields 409 ==")
    status_is("409 tag that already exists", 409, "POST", "/v1/tags",
              {"tag": "status:v1", "template": template_id}, admin)
    status_is("409 undeploy a template with live instances", 409, "POST",
              "/v1/templates/%s/undeploy" % template_id, {}, admin)
    status_is("409 delete a template still deployed", 409, "DELETE",
              "/v1/templates/" + template_id, None, admin)
    status_is("409 delete an instance that is not terminal", 409, "DELETE",
              "/v1/instances/" + instance_id, None, admin)

    print("")
    print("== PRIOR CONTRADICTED: an unknown instance id reads 200, not 404 ==")
    status, text = call("GET", "/v1/instances/%s/messages" % UNKNOWN, None, admin)
    check("GET /v1/instances/{unknown uuid}/messages answers 200 with an empty list",
          status == 200 and json.loads(text) == {"messages": []}, "%s %s" % (status, text[:70]))
    status, text = call("GET", "/v1/claim-handles/%s/holders" % UNKNOWN, None, admin)
    check("GET /v1/claim-handles/{unknown uuid}/holders answers 200 with an empty list",
          status == 200 and json.loads(text) == {"holders": []}, "%s %s" % (status, text[:70]))

    print("")
    print("== PRIOR CONTRADICTED: bad client input reads 500, not 4xx ==")
    for path in ["/v1/instances?cursor=zzz", "/v1/templates?cursor=zzz",
                 "/v1/events?cursor=zzz", "/v1/audit?cursor=zzz",
                 "/v1/observability/instances?cursor=zzz"]:
        status, text = call("GET", path, None, admin)
        check("500 on a malformed ?cursor= %-38s" % path, status == 500, "%s %s" % (status, text[:70]))

    print("")
    print("== PRIOR CONTRADICTED: addressing an instance by key reads 400, not 404 ==")
    status, text = call("GET", "/v1/instances/status-a/frames", None, admin)
    check("GET /v1/instances/{key}/frames answers 400 \"invalid instance id\"",
          status == 400 and json.loads(text)["error"] == "invalid instance id", "%s %s" % (status, text[:70]))
    status, text = call("GET", "/v1/instances/nosuchkey", None, admin)
    check("GET /v1/instances/{unknown key} answers 404 on the sibling route",
          status == 404, "%s %s" % (status, text[:70]))

    print("")
    print("== PRIOR CONTRADICTED: an unmatched path and a bad method leave the JSON surface ==")
    status, text = call("GET", "/v1/no-such-route", None, admin)
    check("404 on an unmatched /v1 path is plain text",
          status == 404 and text.strip() == "404 page not found", "%s %r" % (status, text[:40]))
    status, text = call("PATCH", "/v1/instances", {}, admin)
    check("405 on a wrong method carries an empty body", status == 405 and text == "",
          "%s %r" % (status, text))

    finish()


main()
