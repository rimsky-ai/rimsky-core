SLUG = "assumption-http-tag-create-idempotent"

import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG") or sys.exit("export RIMSKY_IMAGE_TAG=src-<tree hash> first")
IMAGE = "rimsky-all-in-one:" + TAG
NAME = "exp-" + SLUG
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
STATE = {"base": None, "checks": []}


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


def raw(method, path, body=None, token=None, headers=None):
    hdrs = {"Content-Type": "application/json", "Idempotency-Key": uuid.uuid4().hex}
    if token:
        hdrs["Authorization"] = "Bearer " + token
    if headers:
        hdrs.update(headers)
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(STATE["base"] + path, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, resp.read().decode(), dict(resp.headers)
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode(), dict(exc.headers)


def call(method, path, body=None, token=None, headers=None):
    status, text, _ = raw(method, path, body, token, headers)
    try:
        return status, json.loads(text) if text else None
    except ValueError:
        return status, text


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


def cli_env():
    env = dict(os.environ, HOME=tempfile.mkdtemp(prefix="rimsky-exp-"))
    env.pop("RIMSKY_API_KEY", None)
    env.pop("RIMSKY_CONTROL_API_URL", None)
    return env


def plaintext_of(out):
    for line in out.splitlines():
        if "RIMSKY_API_KEY" in line and "for subsequent" in line:
            return line.split("RIMSKY_API_KEY=")[1].split(" ")[0].strip('"')
    die("could not read a key plaintext out of:\n" + out)


def bootstrap_admin():
    return plaintext_of(subprocess.run([CLI, "auth", "init", "--endpoint", STATE["base"]],
                                       capture_output=True, text=True, env=cli_env()).stdout)


def finish():
    docker("rm", "-f", NAME)
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)

SPEC = {"name": "idem-probe", "version": "1",
        "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}


def main():
    boot()
    admin = bootstrap_admin()

    print("== the siblings the prior reasons from ARE idempotent ==")
    first, a = call("POST", "/v1/templates", {"spec": SPEC}, admin)
    second, b = call("POST", "/v1/templates", {"spec": SPEC}, admin)
    template_id = a["template_id"]
    check("POST /v1/templates twice: 201 then 200, same template_id",
          first == 201 and second == 200 and a == b, "%s then %s" % (first, second))

    first, _ = call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    second, b = call("POST", "/v1/templates/%s/deploy" % template_id, {}, admin)
    check("POST /v1/templates/{id}/deploy twice: 200 then 200 with no_op",
          first == 200 and second == 200 and b.get("no_op") is True,
          "%s then %s %s" % (first, second, json.dumps(b)))

    body = {"template": template_id, "instance_key": "idem-a", "target_agent": "audit-agent"}
    first, a = call("POST", "/v1/instances", body, admin)
    second, b = call("POST", "/v1/instances", body, admin)
    check("POST /v1/instances twice under one key: 201 then 200, same instance_id",
          first == 201 and second == 200 and a == b, "%s then %s" % (first, second))

    print("")
    print("== PRIOR CONTRADICTED: the tag beside them answers 409 ==")
    body = {"tag": "idem:v1", "template": template_id}
    first, a = call("POST", "/v1/tags", body, admin)
    second, b = call("POST", "/v1/tags", body, admin)
    check("POST /v1/tags twice with an identical body: 201 then 409",
          first == 201 and second == 409 and b == {"error": "tag already exists"},
          "%s then %s %s" % (first, second, json.dumps(b)))
    third, c = call("POST", "/v1/tags", body, admin)
    check("the conflict is not a one-off — a third identical POST is 409 too",
          third == 409, "%s %s" % (third, json.dumps(c)))

    print("")
    print("== PUT is the idempotent form, and the caller has to know that ==")
    first, a = call("PUT", "/v1/tags/idem:v1", {"template": template_id}, admin)
    second, b = call("PUT", "/v1/tags/idem:v1", {"template": template_id}, admin)
    check("PUT /v1/tags/{tag} twice: 200 then 200, same body",
          first == 200 and second == 200 and a == b, "%s then %s" % (first, second))
    status, body = call("PUT", "/v1/tags/never-created:v1", {"template": template_id}, admin)
    check("PUT on a tag that does not exist answers 404, so PUT is not a create either",
          status == 404, "%s %s" % (status, json.dumps(body)))

    print("")
    print("== the same split through the CLI a deployment script would call ==")
    env = cli_env()
    spec_path = os.path.join(tempfile.mkdtemp(prefix="rimsky-exp-"), "template.yml")
    with open(spec_path, "w") as handle:
        handle.write("name: idem-cli\nversion: \"1\"\nnodes:\n  - type: w\n    kind: attribute_passthrough\n")
    runs = [subprocess.run([CLI, "template", "register", spec_path, "--endpoint", STATE["base"],
                            "--key", admin], capture_output=True, text=True, env=env)
            for _ in range(2)]
    check("rimsky template register twice: exit 0 both times",
          [r.returncode for r in runs] == [0, 0],
          "exits %s" % [r.returncode for r in runs])

    _, out = call("GET", "/v1/templates?limit=50", None, admin)
    cli_template = [t["id"] for t in out["templates"] if t["id"] != template_id][0]
    runs = [subprocess.run([CLI, "tag", "create", "idem-cli:v1", "--template", cli_template,
                            "--endpoint", STATE["base"], "--key", admin],
                           capture_output=True, text=True, env=env) for _ in range(2)]
    check("rimsky tag create twice: exit 0 then exit 1 with the 409",
          [r.returncode for r in runs] == [0, 1]
          and "409 tag already exists" in (runs[1].stdout + runs[1].stderr),
          "exits %s | %s" % ([r.returncode for r in runs],
                             (runs[1].stdout + runs[1].stderr).strip()[:80]))

    print("")
    print("== a re-run script therefore dies on the tag and nothing else ==")
    statuses = []
    for method, path, body in [("POST", "/v1/templates", {"spec": SPEC}),
                               ("POST", "/v1/templates/%s/deploy" % template_id, {}),
                               ("POST", "/v1/instances", {"template": template_id,
                                                          "instance_key": "idem-a",
                                                          "target_agent": "audit-agent"}),
                               ("POST", "/v1/tags", {"tag": "idem:v1", "template": template_id})]:
        statuses.append((path, call(method, path, body, admin)[0]))
    failing = [p for p, s in statuses if s >= 400]
    check("replaying the whole deployment: only /v1/tags is a failure",
          failing == ["/v1/tags"], json.dumps(statuses))

    finish()


main()
