import json
import os
import subprocess
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import (boot, call, check, docker, finish, new_network,  # noqa: E402
                     run_container, teardown, wait_until)

ROOT = os.path.abspath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
SUBNET = "172.31.92.0/24"

CONFIG = """persistence:
  driver: postgres
  postgres:
    dsn: postgres://rimsky:rimsky@%s:5432/rimsky?sslmode=disable
claim_producers: {}
named_locks: {}
executors: {}
"""


def cli(*args):
    res = subprocess.run([CLI, *args], capture_output=True, text=True)
    return res.returncode, res.stdout + res.stderr


def write_config(pghost):
    path = os.path.join(tempfile.mkdtemp(), "rimsky.yml")
    with open(path, "w") as fh:
        fh.write(CONFIG % pghost)
    return path


def probe():
    try:
        return call("GET", "/v1/health")
    except Exception as exc:
        return 0, str(exc)


def main():
    if not os.path.exists(CLI):
        subprocess.run(["make", "-C", ROOT, "cli"], capture_output=True, text=True)
    network = new_network(SUBNET)
    pg, res = run_container("postgres:16-alpine", network=network, env={
        "POSTGRES_USER": "rimsky", "POSTGRES_PASSWORD": "rimsky", "POSTGRES_DB": "rimsky"})
    if res.returncode != 0:
        raise SystemExit("HARNESS ERROR: postgres failed to start: " + res.stderr)
    wait_until(lambda: docker("exec", pg, "pg_isready", "-U", "rimsky").returncode == 0)

    _, base = boot(network=network, mounts=[(write_config(pg), "/etc/rimsky/rimsky.yml")])
    status, out = probe()
    check("the probe reports success while persistence is available",
          status == 200 and out.get("status") == "ok", "%s %s" % (status, json.dumps(out)))
    rc, _ = cli("health", "--endpoint", base)
    check("the health CLI verb exits 0 on the same healthy deployment", rc == 0, "exit %d" % rc)

    docker("stop", pg)
    status, out = wait_until(lambda: (lambda r: r if not (r[0] == 200 and (r[1] or {}).get("status") == "ok") else None)(probe()))
    check("the probe stops reporting success once persistence is gone",
          status != 200, "%s %s" % (status, str(out)[:160]))
    rc, text = cli("health", "--endpoint", base)
    check("the health CLI verb exits non-zero on the same unhealthy deployment",
          rc != 0, "exit %d | %s" % (rc, text.strip().splitlines()[-1] if text.strip() else ""))

    docker("start", pg)
    wait_until(lambda: docker("exec", pg, "pg_isready", "-U", "rimsky").returncode == 0)
    status, out = wait_until(lambda: (lambda r: r if r[0] == 200 and (r[1] or {}).get("status") == "ok" else None)(probe()))
    check("the probe reports success again once persistence comes back",
          status == 200 and out.get("status") == "ok", "%s %s" % (status, json.dumps(out)))
    finish()


try:
    main()
finally:
    teardown()
