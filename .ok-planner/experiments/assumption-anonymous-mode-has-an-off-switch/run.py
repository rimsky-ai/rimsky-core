SLUG = "assumption-anonymous-mode-has-an-off-switch"

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
BASENAME = "exp-" + SLUG
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
HOMEDIR = tempfile.mkdtemp(prefix="rimsky-exp-")
WORK = tempfile.mkdtemp(prefix="rimsky-exp-cfg-")
STATE = {"base": None, "checks": [], "containers": []}

BASE_CONFIG = """persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers: {}
named_locks: {}
executors: {}
"""

CONFIG_CANDIDATES = [
    ("anonymous_mode: false\n", "anonymous_mode"),
    ("auth:\n  required: true\n", "auth"),
    ("require_auth: true\n", "require_auth"),
    ("anonymous:\n  enabled: false\n", "anonymous"),
    ("auth_mode: required\n", "auth_mode"),
]

ENV_CANDIDATES = {
    "RIMSKY_ANONYMOUS_MODE": "off",
    "RIMSKY_AUTH_REQUIRED": "1",
    "RIMSKY_REQUIRE_AUTH": "true",
    "RIMSKY_DISABLE_ANONYMOUS": "1",
    "RIMSKY_AUTH_MODE": "required",
}


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def cleanup():
    for name in STATE["containers"]:
        docker("rm", "-f", name)


def die(msg):
    print("HARNESS ERROR: " + msg)
    cleanup()
    sys.exit(2)


def call(method, path, body=None, token=None, base=None):
    hdrs = {"Content-Type": "application/json", "Idempotency-Key": uuid.uuid4().hex}
    if token:
        hdrs["Authorization"] = "Bearer " + token
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request((base or STATE["base"]) + path, data=data, headers=hdrs, method=method)
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


def run_container(suffix, config_text=None, env=None):
    name = BASENAME + "-" + suffix
    docker("rm", "-f", name)
    port = free_port()
    args = ["run", "-d", "--name", name, "-p", "127.0.0.1:%d:8080" % port]
    if config_text is not None:
        path = os.path.join(WORK, "rimsky-%s.yml" % suffix)
        with open(path, "w") as fh:
            fh.write(config_text)
        args += ["-v", "%s:/etc/rimsky/rimsky.yml:ro" % path]
    for k, v in (env or {}).items():
        args += ["-e", "%s=%s" % (k, v)]
    args.append(IMAGE)
    if docker(*args).returncode != 0:
        die("docker run failed for " + name)
    STATE["containers"].append(name)
    return name, "http://127.0.0.1:%d" % port


def wait_healthy_or_exit(name, base):
    while True:
        try:
            if call("GET", "/v1/health", base=base)[0] == 200:
                return "healthy"
        except Exception:
            pass
        if docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() != "true":
            return "exited"
        time.sleep(0.3)


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def cli(*args, base=None):
    env = dict(os.environ, HOME=HOMEDIR)
    env.pop("RIMSKY_API_KEY", None)
    env.pop("RIMSKY_CONTROL_API_URL", None)
    return subprocess.run([CLI, *args, "--endpoint", base or STATE["base"]],
                          capture_output=True, text=True, env=env)


def plaintext_of(out):
    for line in out.splitlines():
        if "RIMSKY_API_KEY" in line and "for subsequent" in line:
            return line.split("RIMSKY_API_KEY=")[1].split(" ")[0].strip('"')
    die("could not read a key plaintext out of:\n" + out)


def finish():
    cleanup()
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


def main():
    if docker("image", "inspect", IMAGE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % IMAGE)

    print("== PRIOR CONTRADICTED: no config key turns anonymous mode off ==")
    schema_dump = ""
    for i, (snippet, keyname) in enumerate(CONFIG_CANDIDATES):
        name, base = run_container("cfg%d" % i, config_text=BASE_CONFIG + snippet)
        outcome = wait_healthy_or_exit(name, base)
        text = logs(name)
        schema_dump = schema_dump or text
        check("rimsky.yml %-16s is rejected as an unknown field" % keyname,
              outcome == "exited" and "field %s not found" % keyname in text,
              (outcome + " | " + text.strip().splitlines()[-1][:110]) if text.strip() else outcome)
        docker("rm", "-f", name)

    top_level = ["persistence", "claim_producers", "named_locks", "executors", "publishers",
                 "validators", "data_processors", "retention", "dispatch_defaults",
                 "late_bind_service_proxies", "peer_auth", "unreachable_validator_policy"]
    check("the refusal prints the whole top-level schema — 12 keys, no auth toggle among them",
          all(('yaml:\\"%s\\"' % k) in schema_dump for k in top_level) and
          not any(('yaml:\\"%s\\"' % k) in schema_dump for k in
                  ["anonymous_mode", "require_auth", "auth", "auth_mode", "anonymous"]),
          ", ".join(top_level))

    print("")
    print("== PRIOR CONTRADICTED: no env var turns it off either; the stack boots open ==")
    name, base = run_container("env", env=ENV_CANDIDATES)
    check("the stack boots with five candidate auth env vars set",
          wait_healthy_or_exit(name, base) == "healthy")
    status, body = call("GET", "/v1/auth/status", base=base)
    check("with no token at all, auth status answers 200 and reports anonymous",
          status == 200 and body.get("mode") == "anonymous", "%s %s" % (status, json.dumps(body)))
    status, who = call("GET", "/v1/auth/whoami", base=base)
    check("the unauthenticated caller is admitted as the synthetic anonymous identity",
          status == 200 and who == {"kind": "anonymous", "key_name": "anonymous"}, json.dumps(who))
    check("and it can register a template with no credential",
          call("POST", "/v1/templates", {"spec": {"name": "anon", "version": "1", "nodes": [
              {"type": "w", "kind": "attribute_passthrough"}]}}, base=base)[0] == 201)
    check("and it can mint the deployment's first api key with no credential",
          call("POST", "/v1/auth/keys", {"name": "bootstrap", "permissions": [{"action": "*"}]},
               base=base)[0] == 201)
    banner = [ln for ln in logs(name).splitlines() if "anonymous mode" in ln.lower()]
    check("the startup banner warns that every request is treated as admin",
          bool(banner), banner[0][:150] if banner else "no banner line")
    docker("rm", "-f", name)

    print("")
    print("== the only way out is data: mint a key ==")
    name, base = run_container("mint")
    wait_healthy_or_exit(name, base)
    check("before the mint, an unauthenticated read is admitted",
          call("GET", "/v1/instances", base=base)[0] == 200)
    admin = plaintext_of(cli("auth", "init", base=base).stdout)
    check("after the mint, the same unauthenticated read is refused 401",
          call("GET", "/v1/instances", base=base)[0] == 401)
    status, body = call("GET", "/v1/auth/status", None, admin, base=base)
    check("auth status now reports authenticated",
          status == 200 and body.get("mode") == "authenticated", json.dumps(body))

    print("")
    print("== and revoking the last key returns the deployment to anonymous mode ==")
    status, body = call("DELETE", "/v1/auth/keys/admin", None, admin, base=base)
    check("revoking the last key refuses without an explicit intent flag",
          status == 409 and "anonymous mode" in json.dumps(body), "%s %s" % (status, json.dumps(body)))
    status, _ = call("DELETE", "/v1/auth/keys/admin?force_leave_anonymous=true", None, admin, base=base)
    check("with the flag it succeeds", status == 200, str(status))
    while call("GET", "/v1/instances", base=base)[0] != 200:
        time.sleep(0.3)
    check("the deployment admits unauthenticated requests again",
          call("GET", "/v1/instances", base=base)[0] == 200)

    finish()


main()
