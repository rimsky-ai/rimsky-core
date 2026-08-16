SLUG = "assumption-roles-are-server-side"

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
HOMEDIR = tempfile.mkdtemp(prefix="rimsky-exp-")
STATE = {"base": None, "checks": []}

BUNDLED_ROLES = ["admin", "agent-supervisor", "debug-operator", "operator",
                 "publisher-service", "read-only"]


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
            text, status = resp.read().decode(), resp.status
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


def cli(*args):
    argv = [CLI, *args, "--endpoint", STATE["base"]]
    env = dict(os.environ, HOME=HOMEDIR)
    env.pop("RIMSKY_API_KEY", None)
    env.pop("RIMSKY_CONTROL_API_URL", None)
    return subprocess.run(argv, capture_output=True, text=True, env=env)


def plaintext_of(out):
    for line in out.splitlines():
        if "RIMSKY_API_KEY" in line and "for subsequent" in line:
            return line.split("RIMSKY_API_KEY=")[1].split(" ")[0].strip('"')
    die("could not read a key plaintext out of:\n" + out)


def finish():
    docker("rm", "-f", NAME)
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


def main():
    boot()
    admin = plaintext_of(cli("auth", "init").stdout)

    print("== a key minted with --role operator ==")
    op = plaintext_of(cli("auth", "create-key", "--key", admin, "--name", "op", "--role", "operator").stdout)
    status, key = call("GET", "/v1/auth/keys/op", None, admin)
    check("GET /v1/auth/keys/op answers 200", status == 200, str(status))
    print("      response fields: " + ", ".join(sorted(key.keys())))

    print("")
    print("== PRIOR CONTRADICTED: the response carries no role ==")
    check("no field of the key mentions a role",
          not [k for k in key if "role" in k.lower()], ", ".join(sorted(key.keys())))
    check("no value of the key names a bundled role",
          not [r for r in BUNDLED_ROLES if r in json.dumps({k: v for k, v in key.items()
                                                            if k != "permissions"})],
          json.dumps({k: v for k, v in key.items() if k != "permissions"}))
    check("what the key carries instead is the expanded grant",
          [e["action"] for e in key["permissions"]][:4] ==
          ["instance:*", "template:*", "tag:*", "node:*"],
          json.dumps([e["action"] for e in key["permissions"]]))
    check("the grant has 16 entries and no name for the set",
          len(key["permissions"]) == 16, str(len(key["permissions"])))

    status, listing = call("GET", "/v1/auth/keys", None, admin)
    rows = listing["keys"]
    check("the key listing carries no role field either",
          status == 200 and not [k for row in rows for k in row if "role" in k.lower()],
          ", ".join(sorted(rows[0].keys())))

    print("")
    print("== PRIOR CONTRADICTED: no route lists the roles ==")
    for path in ["/v1/auth/roles", "/v1/roles", "/v1/auth/keys/op/role", "/v1/auth/role-templates"]:
        status, _ = call("GET", path, None, admin)
        check("GET %-24s is not a route" % path, status == 404, str(status))

    print("")
    print("== the role name is expanded by the CLI, before the server is contacted ==")
    res = cli("auth", "create-key", "--key", admin, "--name", "nope", "--role", "nonesuch")
    joined = res.stdout + res.stderr
    check("an unknown role fails client-side, naming the six bundled roles",
          res.returncode != 0 and "unknown bundled role" in joined and
          all(r in joined for r in BUNDLED_ROLES), joined.strip()[:130])
    unreachable = subprocess.run(
        [CLI, "auth", "create-key", "--endpoint", "http://127.0.0.1:1", "--key", admin,
         "--name", "nope3", "--role", "nonesuch"],
        capture_output=True, text=True, env=dict(os.environ, HOME=HOMEDIR))
    check("the same refusal arrives with the endpoint pointed at a dead port",
          unreachable.returncode != 0 and
          "unknown bundled role" in (unreachable.stdout + unreachable.stderr),
          (unreachable.stdout + unreachable.stderr).strip()[:130])
    check("a valid role against the same dead port gets as far as the connection",
          "connection refused" in subprocess.run(
              [CLI, "auth", "create-key", "--endpoint", "http://127.0.0.1:1", "--key", admin,
               "--name", "nope4", "--role", "read-only"],
              capture_output=True, text=True, env=dict(os.environ, HOME=HOMEDIR)).stderr)

    print("")
    print("== the role label the CLI prints is a client-side match on the grant ==")
    status, minted = call("POST", "/v1/auth/keys",
                          {"name": "never-named-a-role", "permissions": [{"action": "*:read"}]}, admin)
    check("a key minted over HTTP with the read-only grant and no role name", status == 201, str(status))
    listing_out = cli("auth", "list", "--key", admin).stdout
    row = [ln for ln in listing_out.splitlines() if ln.startswith("never-named-a-role")]
    check("`rimsky auth list` still labels it role:read-only",
          row and "role:read-only" in row[0], row[0] if row else listing_out)
    patched = cli("auth", "create-key", "--key", admin, "--name", "patched", "--role", "read-only",
                  "--add", "instance:create")
    check("a patched grant reads back as custom, not as a role",
          "custom" in [ln.split("\t")[2] for ln in cli("auth", "list", "--key", admin).stdout.splitlines()
                       if ln.startswith("patched")],
          patched.stdout.strip()[:80])

    print("")
    print("== two different roles with the same grant are indistinguishable server-side ==")
    cli("auth", "create-key", "--key", admin, "--name", "dash", "--role", "read-only")
    _, a = call("GET", "/v1/auth/keys/dash", None, admin)
    _, b = call("GET", "/v1/auth/keys/never-named-a-role", None, admin)
    check("the CLI-role key and the raw-grant key read back identically",
          a["permissions"] == b["permissions"] == [{"action": "*:read"}],
          json.dumps([a["permissions"], b["permissions"]]))
    check("the operator key is a real key for what its grant allows",
          call("GET", "/v1/templates", None, op)[0] == 200)

    finish()


main()
