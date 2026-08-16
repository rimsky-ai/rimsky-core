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

SLUG = "assumption-http-delete-idempotent"
TAG = os.environ.get("RIMSKY_IMAGE_TAG") or sys.exit("export RIMSKY_IMAGE_TAG=src-<tree hash> first")
IMAGE = "rimsky-all-in-one:" + TAG
NAME = "exp-" + SLUG
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
STATE = {"base": None, "checks": []}

TPL = {"name": "delete-probe", "version": "1",
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

    return plaintext(subprocess.run(
        [CLI, "auth", "init", "--endpoint", STATE["base"]],
        capture_output=True, text=True, env=env).stdout)


def finish():
    docker("rm", "-f", NAME)
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


def delete_twice(label, path, token, ok_status=200):
    first, first_text = call("DELETE", path, None, token)
    second, second_text = call("DELETE", path, None, token)
    check("%-30s first DELETE succeeds" % label, first == ok_status,
          "%s %s" % (first, first_text[:60]))
    check("%-30s repeat DELETE is NOT idempotent — 404" % label, second == 404,
          "%s %s" % (second, second_text[:60]))
    return second, second_text


def main():
    boot()
    admin = bootstrap_auth()

    _, text = call("POST", "/v1/templates", {"spec": TPL}, admin)
    template_id = json.loads(text)["template_id"]
    call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    call("POST", "/v1/tags", {"tag": "delete:v1", "template": template_id}, admin)
    _, text = call("POST", "/v1/instances", {"template": template_id, "instance_key": "delete-a",
                                             "target_agent": "audit-agent"}, admin)
    instance_id = json.loads(text)["instance_id"]
    _, text = call("POST", "/v1/instances/%s/breakpoints" % instance_id,
                   {"node_type": "w", "checkpoint": "before_dispatch"}, admin)
    breakpoint_id = json.loads(text)["breakpoint_id"]
    _, text = call("POST", "/v1/instances", {"template": template_id, "instance_key": "delete-b",
                                             "target_agent": "audit-agent"}, admin)
    live_id = json.loads(text)["instance_id"]

    print("== PRIOR CONTRADICTED: the second DELETE errors instead of succeeding ==")
    delete_twice("/v1/tags/{tag}", "/v1/tags/delete:v1", admin)
    delete_twice("/v1/instances/{id}/breakpoints/{id}",
                 "/v1/instances/%s/breakpoints/%s" % (instance_id, breakpoint_id), admin, 204)
    check("the success code is not even uniform — 200 for a tag, 204 for a breakpoint", True)

    call("POST", "/v1/instances/%s/terminate" % instance_id, {}, admin)
    while call("DELETE", "/v1/instances/" + instance_id, None, admin)[0] == 409:
        time.sleep(0.25)
    second, second_text = call("DELETE", "/v1/instances/" + instance_id, None, admin)
    check("%-30s repeat DELETE is NOT idempotent — 404" % "/v1/instances/{id}", second == 404,
          "%s %s" % (second, second_text[:60]))

    print("")
    print("== PRIOR CONTRADICTED: DELETE on a never-created resource errors too ==")
    for label, path, want in [("/v1/tags/{tag}", "/v1/tags/never-existed", 404),
                              ("/v1/templates/{id}", "/v1/templates/sha256-deadbeef", 404),
                              ("/v1/instances/{id}", "/v1/instances/11111111-1111-1111-1111-111111111111", 404),
                              ("/v1/auth/keys/{nameOrID}", "/v1/auth/keys/never-existed", 404)]:
        status, text = call("DELETE", path, None, admin)
        check("%-26s DELETE on an absent resource answers %d" % (label, want), status == want,
              "%s %s" % (status, text[:60]))

    status, text = call("DELETE", "/v1/instances/%s/assets/w.never" % live_id, None, admin)
    check("%-26s DELETE on an absent asset answers 404" % "/v1/instances/{id}/assets/{alias}",
          status == 404, "%s %s" % (status, text[:60]))
    status, text = call("DELETE", "/v1/instances/%s/assets/badalias" % live_id, None, admin)
    check("%-26s DELETE on a malformed alias answers 400" % "/v1/instances/{id}/assets/{alias}",
          status == 400, "%s %s" % (status, text[:70]))

    print("")
    print("== the one DELETE that is idempotent ==")
    first, _ = call("DELETE", "/v1/mcp", None, admin)
    second, _ = call("DELETE", "/v1/mcp", None, admin)
    check("DELETE /v1/mcp answers 200 on both calls (session teardown)",
          first == 200 and second == 200, "%s then %s" % (first, second))

    print("")
    print("== a live resource refuses DELETE with 409, and repeats itself ==")
    first, first_text = call("DELETE", "/v1/instances/" + live_id, None, admin)
    second, _ = call("DELETE", "/v1/instances/" + live_id, None, admin)
    check("DELETE a non-terminal instance answers 409 every time",
          first == 409 and second == 409, "%s %s" % (first, first_text[:70]))

    print("")
    print("== the template teardown a script would actually write ==")
    call("POST", "/v1/instances/%s/terminate" % live_id, {}, admin)
    while call("DELETE", "/v1/instances/" + live_id, None, admin)[0] == 409:
        time.sleep(0.25)
    status, text = call("POST", "/v1/templates/%s/undeploy" % template_id, {}, admin)
    check("undeploy succeeds once every instance is gone", status == 200, "%s %s" % (status, text[:60]))
    delete_twice("/v1/templates/{id}", "/v1/templates/" + template_id, admin)

    finish()


main()
