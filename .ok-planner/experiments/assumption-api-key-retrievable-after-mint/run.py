SLUG = "assumption-api-key-retrievable-after-mint"

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
        return status, (json.loads(text) if text else None), text
    except ValueError:
        return status, text, text


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


def main():
    boot()
    admin = plaintext_of(cli("auth", "init").stdout)

    print("== the plaintext is surfaced once, at mint ==")
    status, minted, _ = call("POST", "/v1/auth/keys",
                             {"name": "lost", "permissions": [{"action": "*:read"}]}, admin)
    secret = minted["plaintext"]
    key_id = minted["id"]
    check("POST /v1/auth/keys returns the plaintext in its response",
          status == 201 and secret.startswith("rk_"), "%s %s…" % (status, secret[:6]))
    check("the minted key authenticates", call("GET", "/v1/instances", None, secret)[0] == 200)

    print("")
    print("== PRIOR CONTRADICTED: neither read-back surface returns it again ==")
    for label, path in [("GET /v1/auth/keys/{name}", "/v1/auth/keys/lost"),
                        ("GET /v1/auth/keys/{id}", "/v1/auth/keys/" + key_id),
                        ("GET /v1/auth/keys", "/v1/auth/keys")]:
        status, body, text = call("GET", path, None, admin)
        check("%-26s answers 200 without the plaintext" % label,
              status == 200 and secret not in text and "plaintext" not in text,
              "%s | fields: %s" % (status, ",".join(sorted((body if isinstance(body, dict) else
                                                            body["keys"][0]).keys()))))

    show = cli("auth", "show", "lost", "--key", admin)
    check("`rimsky auth show lost` answers without the plaintext",
          show.returncode == 0 and secret not in (show.stdout + show.stderr),
          show.stdout.strip().replace("\n", " ")[:110])
    listing = cli("auth", "list", "--key", admin)
    check("`rimsky auth list` answers without the plaintext",
          listing.returncode == 0 and secret not in (listing.stdout + listing.stderr))

    print("")
    print("== and no query parameter reveals it ==")
    for query in ["?reveal=true", "?include_plaintext=true", "?show_secret=true"]:
        status, _, text = call("GET", "/v1/auth/keys/lost" + query, None, admin)
        check("GET /v1/auth/keys/lost%-22s still hides it" % query,
              status == 200 and secret not in text, str(status))

    print("")
    print("== what the server holds is a digest, and it never matches by name ==")
    status, _, _ = call("GET", "/v1/instances", None, "rk_" + "x" * 24)
    check("an invented plaintext is refused 401", status == 401, str(status))

    print("")
    print("== recovery is rotation: a new plaintext, and the old one dies ==")
    rot = cli("auth", "rotate", "lost", "--key", admin, "--grace", "1s")
    new = [ln.strip() for ln in rot.stdout.splitlines() if ln.strip().startswith("rk_")]
    check("rotate prints a plaintext", bool(new), rot.stdout.strip().replace("\n", " ")[:110])
    check("the rotated plaintext differs from the lost one", new and new[0] != secret)
    check("the new plaintext authenticates", call("GET", "/v1/instances", None, new[0])[0] == 200)
    while call("GET", "/v1/instances", None, secret)[0] != 401:
        time.sleep(0.3)
    check("the old plaintext stops being accepted once the grace passes",
          call("GET", "/v1/instances", None, secret)[0] == 401)

    finish()


main()
