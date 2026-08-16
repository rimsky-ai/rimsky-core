SLUG = "assumption-permission-wildcards-are-globs"

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


def call(method, path, body=None, token=None):
    hdrs = {"Content-Type": "application/json", "Idempotency-Key": uuid.uuid4().hex}
    if token:
        hdrs["Authorization"] = "Bearer " + token
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(STATE["base"] + path, data=data, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            text = resp.read().decode()
            status = resp.status
    except urllib.error.HTTPError as exc:
        text, status = exc.read().decode(), exc.code
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
    if docker("run", "-d", "--name", NAME, "-p", "127.0.0.1:%d:8080" % port, IMAGE).returncode != 0:
        die("docker run failed")
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


def bootstrap_admin():
    out = subprocess.run([CLI, "auth", "init", "--endpoint", STATE["base"]],
                         capture_output=True, text=True, env=cli_env()).stdout
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


GLOB_SHAPES = [
    "instance:re*",
    "instance:*ead",
    "instance:re*d",
    "inst*:read",
    "*ance:read",
    "*:re*",
    "instance:**",
    "*:*",
    "**",
    "instance:read*",
    "*instance:read",
]

VALID_SHAPES = ["*", "template:*", "*:read"]


def main():
    boot()
    admin = bootstrap_admin()
    serial = [0]

    def mint(permissions, name=None):
        serial[0] += 1
        return call("POST", "/v1/auth/keys",
                    {"name": name or ("wc-%d" % serial[0]), "permissions": permissions}, admin)

    print("== PRIOR CONTRADICTED: no glob shape mints; the grammar is three literal forms ==")
    for shape in GLOB_SHAPES:
        status, body = mint([{"action": shape}])
        msg = body.get("error", "") if isinstance(body, dict) else str(body)
        check("%-18s refused at key creation" % shape,
              status == 400 and "unsupported wildcard shape" in msg or
              status == 400 and "must be" in msg,
              "%s %s" % (status, msg[:90]))

    print("")
    print("== the exact error names the whole vocabulary ==")
    _, body = mint([{"action": "instance:re*"}])
    check("infix wildcard names the three allowed shapes",
          body == {"error": "entry 0: action \"instance:re*\": unsupported wildcard shape "
                            "(only '*', '<noun>:*', '*:<verb>' allowed)"},
          json.dumps(body))

    print("")
    print("== the three literal forms mint ==")
    for shape in VALID_SHAPES:
        status, _ = mint([{"action": shape}])
        check("%-12s accepted" % shape, status == 201, str(status))

    print("")
    print("== and they match on the separator boundary, not as a substring ==")
    _, out = mint([{"action": "template:*"}])
    tpl = out["plaintext"]
    check("template:* grants template:read (GET /v1/templates)",
          call("GET", "/v1/templates", None, tpl)[0] == 200)
    check("template:* grants template:register",
          call("POST", "/v1/templates", {"spec": {"name": "wc", "version": "1", "nodes": [
              {"type": "w", "kind": "attribute_passthrough"}]}}, tpl)[0] == 201)
    check("template:* does not reach instance:read", call("GET", "/v1/instances", None, tpl)[0] == 403)
    check("template:* does not reach tag:read", call("GET", "/v1/tags", None, tpl)[0] == 403)

    _, out = mint([{"action": "*:read"}])
    ro = out["plaintext"]
    check("*:read grants instance:read", call("GET", "/v1/instances", None, ro)[0] == 200)
    check("*:read grants audit:read", call("GET", "/v1/audit", None, ro)[0] == 200)
    check("*:read does not reach instance:create",
          call("POST", "/v1/instances", {"template": "nope", "instance_key": "k"}, ro)[0] == 403)

    _, out = mint([{"action": "nod:*"}])
    truncated_noun = out["plaintext"]
    check("nod:* does NOT reach node:read — the ':' is part of the boundary",
          call("GET", "/v1/nodes/%s" % uuid.uuid4(), None, truncated_noun)[0] == 403)
    _, out = mint([{"action": "*:ead"}])
    truncated_verb = out["plaintext"]
    check("*:ead does NOT reach instance:read — the ':' is part of the boundary",
          call("GET", "/v1/instances", None, truncated_verb)[0] == 403)

    print("")
    print("== a wildcard over a noun that does not exist mints silently and grants nothing ==")
    status, body = mint([{"action": "banana:read"}])
    check("a literal action outside the registry is refused by name",
          status == 400 and body == {"error": "unknown action: banana:read"},
          "%s %s" % (status, json.dumps(body)))
    for shape in ["banana:*", "backfill:*", "*:frobnicate", "instanc:*"]:
        status, out = mint([{"action": shape}])
        ok = status == 201
        reach = call("GET", "/v1/instances", None, out["plaintext"])[0] if ok else None
        check("%-14s mints 201 and reaches nothing" % shape, ok and reach == 403,
              "mint %s, GET /v1/instances %s" % (status, reach))

    finish()


main()
