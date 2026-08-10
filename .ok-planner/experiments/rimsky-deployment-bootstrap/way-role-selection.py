import json
import os
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from harness import (RIMSKY, call, check, docker, finish, free_port, logs,  # noqa: E402
                     new_network, require_image, run_container, teardown, wait_healthy,
                     wait_until)

SUBNET = "172.31.93.0/24"

PG_CONFIG = """persistence:
  driver: postgres
  postgres:
    dsn: postgres://rimsky:rimsky@%s:5432/rimsky?sslmode=disable
claim_producers: {}
named_locks: {}
executors: {}
"""

SQLITE_CONFIG = """persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
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


def write_config(text):
    path = os.path.join(tempfile.mkdtemp(), "rimsky.yml")
    with open(path, "w") as fh:
        fh.write(text)
    return path



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


def rimsky_processes(container):
    res = docker("top", container)
    return [ln for ln in res.stdout.splitlines()[1:] if "rimsky" in ln]


def start_role(config, network, command=None, publish=None, env=None):
    merged = dict(ROLE_ENV)
    merged.update(env or {})
    name, res = run_container(RIMSKY, network=network, env=merged, publish=publish,
                              mounts=role_mounts(config), command=command)
    if res.returncode != 0:
        raise SystemExit("HARNESS ERROR: docker run failed: " + res.stderr)
    return name


def run_to_exit(config, command):
    _, res = run_container(RIMSKY, env=ROLE_ENV, mounts=role_mounts(config),
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
    pg_config = write_config(PG_CONFIG % pg)

    port = free_port()
    api = start_role(pg_config, network, command=["rimsky-control-api"], publish=(port, 8080))
    base = "http://127.0.0.1:%d" % port
    wait_healthy(base)
    check("naming one role in the container command runs only that role",
          "rimsky-control-api" in [ln.split()[-1].split("/")[-1] for ln in rimsky_processes(api)]
          and len(rimsky_processes(api)) == 2,
          "\n".join(rimsky_processes(api)))
    check("the control-api role is the one that carries the migrations",
          "migrations complete" in logs(api),
          str([ln for ln in logs(api).splitlines() if "migrations complete" in ln][:1]))
    check("that single-role deployment answers on the control API",
          call("GET", "/v1/health", base=base)[1].get("status") == "ok", base)

    sched = start_role(pg_config, network, command=["rimsky-scheduler"])
    wait_until(lambda: "selected roles" in logs(sched))
    check("a scheduler-only container runs the scheduler and nothing else",
          '"roles":["rimsky-scheduler"]' in logs(sched).replace(", ", ","),
          str([ln for ln in logs(sched).splitlines() if "selected roles" in ln][:1]))
    check("the scheduler role does not run migrations",
          "skipping migrations for this role" in logs(sched)
          and "running migrations" not in logs(sched),
          "skip=%s run=%s" % ("skipping migrations for this role" in logs(sched),
                              "running migrations" in logs(sched)))

    sup = start_role(pg_config, network, command=["rimsky-supervisor"])
    wait_until(lambda: "selected roles" in logs(sup))
    check("a supervisor-only container runs the supervisor and does not migrate",
          '"roles":["rimsky-supervisor"]' in logs(sup).replace(", ", ",")
          and "skipping migrations for this role" in logs(sup),
          str([ln for ln in logs(sup).splitlines() if "selected roles" in ln][:1]))

    port = free_port()
    unified = start_role(write_config(SQLITE_CONFIG), network, publish=(port, 8080))
    wait_healthy("http://127.0.0.1:%d" % port)
    check("no command at all launches all three roles together, in one process",
          len(rimsky_processes(unified)) == 1
          and all(r in logs(unified) for r in ("rimsky-scheduler", "rimsky-supervisor",
                                               "rimsky-control-api")),
          "\n".join(rimsky_processes(unified)))
    check("the no-command deployment migrated itself before serving",
          "migrations complete" in logs(unified),
          str([ln for ln in logs(unified).splitlines() if "migrations complete" in ln][:1]))

    rc, out = run_to_exit(pg_config, ["rimsky-frobnicate"])
    check("an unknown role argument refuses to launch anything",
          rc != 0 and "unknown role" in out, "exit %d | %s" % (rc, str(out.strip().splitlines()[-1:])))
    rc, out = run_to_exit(pg_config, ["rimsky-migrate"])
    check("the migrate binary is not a role the entrypoint will run",
          rc != 0 and "unknown role" in out, "exit %d | %s" % (rc, str(out.strip().splitlines()[-1:])))
    rc, out = run_to_exit(pg_config, ["rimsky-scheduler", "rimsky-supervisor"])
    check("naming two roles at once refuses to launch anything",
          rc != 0 and "at most one role argument" in out,
          "exit %d | %s" % (rc, str(out.strip().splitlines()[-1:])))
    finish()


try:
    main()
finally:
    teardown()
