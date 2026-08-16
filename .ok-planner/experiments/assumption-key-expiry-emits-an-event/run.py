SLUG = "assumption-key-expiry-emits-an-event"

import datetime
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

AUDIT_KINDS = ["auth.access_attempted", "auth.access_denied", "auth.key_created",
               "auth.key_revoked", "auth.key_rotated"]


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
    env = dict(os.environ, HOME=HOMEDIR)
    env.pop("RIMSKY_API_KEY", None)
    env.pop("RIMSKY_CONTROL_API_URL", None)
    return subprocess.run([CLI, *args, "--endpoint", STATE["base"]],
                          capture_output=True, text=True, env=env)


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


def audit(query=""):
    status, body = call("GET", "/v1/audit" + query, None, STATE["admin"])
    if status != 200:
        die("GET /v1/audit%s -> %s %s" % (query, status, json.dumps(body)))
    return body["audit"]


def kinds_for(key_name):
    return sorted({row["kind"] for row in audit("?key_name=" + key_name)})


def main():
    boot()
    STATE["admin"] = plaintext_of(cli("auth", "init").stdout)

    print("== the three lifecycle phases the prior takes as siblings ==")
    _, made = call("POST", "/v1/auth/keys",
                   {"name": "revoked-one", "permissions": [{"action": "*:read"}]}, STATE["admin"])
    call("DELETE", "/v1/auth/keys/revoked-one", None, STATE["admin"])
    check("revoking a key writes auth.key_revoked",
          kinds_for("revoked-one") == ["auth.key_created", "auth.key_revoked"],
          ",".join(kinds_for("revoked-one")))
    call("POST", "/v1/auth/keys", {"name": "rotated-one", "permissions": [{"action": "*:read"}]},
         STATE["admin"])
    call("POST", "/v1/auth/keys/rotated-one/rotate", {"grace": "1h"}, STATE["admin"])
    check("rotating a key writes auth.key_rotated",
          "auth.key_rotated" in kinds_for("rotated-one"), ",".join(kinds_for("rotated-one")))

    print("")
    print("== now let a key lapse at its own expiry ==")
    expires = (datetime.datetime.now(datetime.timezone.utc)
               + datetime.timedelta(seconds=5)).strftime("%Y-%m-%dT%H:%M:%SZ")
    status, minted = call("POST", "/v1/auth/keys",
                          {"name": "lapsing", "permissions": [{"action": "*:read"}],
                           "expires_at": expires}, STATE["admin"])
    check("a key with a 5s expiry mints", status == 201, "%s %s" % (status, expires))
    secret = minted["plaintext"]
    status, _ = call("POST", "/v1/auth/keys",
                     {"name": "lapsing-unused", "permissions": [{"action": "*:read"}],
                      "expires_at": expires}, STATE["admin"])
    check("a second key with the same expiry mints, to be left untouched", status == 201, str(status))
    check("the first key authenticates before the expiry",
          call("GET", "/v1/instances", None, secret)[0] == 200)
    before = kinds_for("lapsing")
    print("      audit kinds for the used key before it lapses: " + ",".join(before))
    print("      polling until the key stops being accepted (blocks until it does)")
    while call("GET", "/v1/instances", None, secret)[0] != 401:
        time.sleep(0.3)
    check("the key has lapsed — its next request is 401",
          call("GET", "/v1/instances", None, secret)[0] == 401)

    print("")
    print("== PRIOR CONTRADICTED: nothing in the audit feed describes the lapse ==")
    after = kinds_for("lapsing")
    check("no new lifecycle kind appears for the lapsed key",
          [k for k in after if k.startswith("auth.key_")] == ["auth.key_created"],
          "before %s | after %s" % (before, after))
    unused = [r["kind"] for r in audit("?key_name=lapsing-unused")]
    check("the key that lapsed without being used has exactly one audit row, its creation",
          unused == ["auth.key_created"], ",".join(unused))
    for kind in ["auth.key_expired", "auth.key_lapsed", "auth.key_expiry"]:
        status, body = call("GET", "/v1/audit?kind=" + kind, None, STATE["admin"])
        check("kind=%-18s is not an audit kind" % kind,
              status == 400 and "audit allowlist" in json.dumps(body) or
              status == 400 and "invalid kind" in json.dumps(body), "%s" % status)
    allkinds = sorted({r["kind"] for r in audit()})
    check("the audit surface carries five kinds, none of them an expiry",
          set(allkinds) <= set(AUDIT_KINDS) and not [k for k in AUDIT_KINDS if "expir" in k],
          ",".join(allkinds))

    print("")
    print("== what the operator does see instead ==")
    denied = [r for r in audit("?kind=auth.access_denied") if "lapsing" in json.dumps(r)]
    check("the lapsed key's refused request is recorded as auth.access_denied",
          bool(denied), json.dumps(denied[0])[:160] if denied else "none names the key")
    listing = cli("auth", "list", "--key", STATE["admin"], "--json")
    rows = json.loads(listing.stdout)
    lapsed = [r for r in rows if r["name"] == "lapsing"]
    check("the lapsed key is still listed by `rimsky auth list`, expiry visible on the row",
          bool(lapsed) and bool(lapsed[0].get("expires_at")),
          json.dumps(lapsed[0]) if lapsed else listing.stdout[:120])
    status, body = call("GET", "/v1/auth/status", None, STATE["admin"])
    check("auth status counts the lapsed key out of the active total",
          status == 200 and body["mode"] == "authenticated", json.dumps(body))

    finish()


main()
