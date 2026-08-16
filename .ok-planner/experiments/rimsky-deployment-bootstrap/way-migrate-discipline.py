import json
import os
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import (RIMSKY, call, check, deploy, docker, finish, free_port,  # noqa: E402
                     logs, new_instance, new_network, passthrough_node, quiet,
                     require_image, run_container, send_message, show, teardown,
                     wait_healthy, wait_until)

SUBNET = "172.31.94.0/24"

CONFIG = """persistence:
  driver: postgres
  postgres:
    dsn: postgres://rimsky:rimsky@%s:5432/%s?sslmode=disable
claim_producers: {}
named_locks: {}
executors: {}
"""

SUPERVISOR_CONFIG = """concurrency: 4
claim_poll_interval_ms: 200
callback:
  host: 0.0.0.0
  port: 9100
  advertise_host: 127.0.0.1
"""

ROLE_ENV = {"RIMSKY_CONTROL_API_HOST": "0.0.0.0",
            "RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST": "127.0.0.1"}

SPEC = {
    "name": "exp-migrate-discipline",
    "version": "1",
    "nodes": [passthrough_node("worker", None, {"v": {"type": "integer", "default": 5}})],
}



def write_supervisor_config():
    path = os.path.join(tempfile.mkdtemp(), "supervisor-config.yml")
    with open(path, "w") as fh:
        fh.write(SUPERVISOR_CONFIG)
    return path


SUPERVISOR_MOUNT = None


def role_mounts(config):
    global SUPERVISOR_MOUNT
    if SUPERVISOR_MOUNT is None:
        SUPERVISOR_MOUNT = write_supervisor_config()
    return [(config, "/etc/rimsky/rimsky.yml"),
            (SUPERVISOR_MOUNT, "/etc/rimsky/supervisor-config.yml")]


def create_database(pg, name):
    docker("exec", pg, "createdb", "-U", "rimsky", name)
    res = docker("exec", pg, "psql", "-U", "rimsky", "-d", "postgres", "-tAc",
                 "select 1 from pg_database where datname = '%s'" % name)
    return res.returncode == 0 and res.stdout.strip() == "1"


def write_config(pghost, dbname):
    path = os.path.join(tempfile.mkdtemp(), "rimsky.yml")
    with open(path, "w") as fh:
        fh.write(CONFIG % (pghost, dbname))
    return path


def start(config, network, command=None, publish=None, env=None):
    merged = dict(ROLE_ENV)
    merged.update(env or {})
    name, res = run_container(RIMSKY, network=network, env=merged, publish=publish,
                              mounts=role_mounts(config), command=command,
                              extra=["--restart", "unless-stopped"])
    if res.returncode != 0:
        raise SystemExit("HARNESS ERROR: docker run failed: " + res.stderr)
    return name


def run_to_exit(config, command=None, env=None):
    merged = dict(ROLE_ENV)
    merged.update(env or {})
    _, res = run_container(RIMSKY, env=merged, mounts=role_mounts(config),
                           command=command, detach=False)
    return res.returncode, res.stdout + res.stderr


def main():
    require_image(RIMSKY)
    network = new_network(SUBNET)
    pg, res = run_container("postgres:16-alpine", network=network, env={
        "POSTGRES_USER": "rimsky", "POSTGRES_PASSWORD": "rimsky", "POSTGRES_DB": "rimsky"})
    if res.returncode != 0:
        raise SystemExit("HARNESS ERROR: postgres failed to start: " + res.stderr)
    wait_until(lambda: docker("exec", pg, "pg_isready", "-U", "rimsky").returncode == 0)
    for db in ("skipdb", "forcedb"):
        wait_until(lambda name=db: create_database(pg, name))

    split = write_config(pg, "rimsky")
    sched = start(split, network, command=["rimsky-scheduler"])
    sup = start(split, network, command=["rimsky-supervisor"])
    port = free_port()
    api = start(split, network, command=["rimsky-control-api"], publish=(port, 8080))
    base = "http://127.0.0.1:%d" % port
    wait_healthy(base)
    for name in (sched, sup):
        wait_until(lambda n=name: "selected roles" in logs(n))

    migrating = [n for n in (sched, sup, api) if "running migrations" in logs(n)]
    skipping = [n for n in (sched, sup, api) if "skipping migrations for this role" in logs(n)]
    check("across a three-container role split, exactly one container runs the migrations",
          len(migrating) == 1 and migrating[0] == api,
          "migrated: %d of 3 containers" % len(migrating))
    check("the other two say so rather than racing or staying silent",
          sorted(skipping) == sorted([sched, sup]), "skipped: %d of 3 containers" % len(skipping))
    check("the schema arrived — the split deployment serves",
          call("GET", "/v1/health", base=base)[1].get("status") == "ok", base)

    iid = new_instance(deploy(SPEC, base=base), base=base)
    send_message(iid, base=base)
    tl = quiet(iid, base=base)
    show(tl)
    check("work dispatched and settled across the split roles on the migrated schema",
          any(r["kind"] == "terminal/success" and r["node"] == "worker" for r in tl),
          json.dumps([r["kind"] for r in tl][-3:]))

    skip_cfg = write_config(pg, "skipdb")
    skipped = start(skip_cfg, network, env={"RIMSKY_ENTRYPOINT_MIGRATE": "0"})
    wait_until(lambda: "selected roles" in logs(skipped))
    check("the override can skip the migration a deployment would otherwise own",
          "skipping migrations for this role" in logs(skipped)
          and "running migrations" not in logs(skipped),
          str([ln for ln in logs(skipped).splitlines() if "migrations" in ln][:1]))

    force_cfg = write_config(pg, "forcedb")
    forced = start(force_cfg, network, command=["rimsky-scheduler"],
                   env={"RIMSKY_ENTRYPOINT_MIGRATE": "1"})
    wait_until(lambda: "migrations complete" in logs(forced))
    check("the override can force the migration onto a role that would otherwise skip it",
          "running migrations" in logs(forced) and "migrations complete" in logs(forced),
          str([ln for ln in logs(forced).splitlines() if "migrations complete" in ln][:1]))

    rc, out = run_to_exit(split, env={"RIMSKY_ENTRYPOINT_MIGRATE": "yes"})
    check("a value that is neither 1 nor 0 is a startup error, not a guess",
          rc != 0 and "RIMSKY_ENTRYPOINT_MIGRATE" in out and "yes" in out,
          "exit %d | %s" % (rc, str(out.strip().splitlines()[-1:])))
    finish()


try:
    main()
finally:
    teardown()
