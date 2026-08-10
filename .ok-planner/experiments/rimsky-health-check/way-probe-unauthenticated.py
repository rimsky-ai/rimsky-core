import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import boot, call, check, finish, teardown  # noqa: E402

ROOT = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")


def cli(*args):
    res = subprocess.run([CLI, *args], capture_output=True, text=True)
    return res.returncode, res.stdout + res.stderr


def main():
    if not os.path.exists(CLI):
        subprocess.run(["make", "-C", ROOT, "cli"], capture_output=True, text=True)
    _, base = boot()

    status, out = call("GET", "/v1/health")
    check("the deployment-health probe answers success with no credentials attached",
          status == 200 and out.get("status") == "ok", "%s %s" % (status, json.dumps(out)))

    rc, text = cli("health", "--endpoint", base, "-o", "json")
    check("the health CLI verb answers the same, and exits 0", rc == 0 and '"ok"' in text,
          "exit %d | %s" % (rc, text.strip().splitlines()[0] if text.strip() else ""))

    rc, text = cli("auth", "init", "--endpoint", base)
    check("the deployment can be moved off anonymous access", rc == 0, text.strip()[:200])

    status, out = call("GET", "/v1/instances")
    check("an ordinary route now refuses an unauthenticated caller",
          status == 401, "%s %s" % (status, json.dumps(out)[:160]))
    status, out = call("GET", "/v1/health")
    check("the health probe still answers success without a token — it is the unauthenticated probe",
          status == 200 and out.get("status") == "ok", "%s %s" % (status, json.dumps(out)))
    finish()


try:
    main()
finally:
    teardown()
