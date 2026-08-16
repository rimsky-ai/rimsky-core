import os
import socket
import subprocess
import sys
import time
import uuid

TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    print("HARNESS ERROR: export RIMSKY_IMAGE_TAG=src-<tree hash> first")
    sys.exit(2)
SUFFIX = uuid.uuid4().hex[:6]
NET = "exp-assumption-sensor-dsn-net-" + SUFFIX
PG = "exp-assumption-sensor-dsn-pg-" + SUFFIX
CHECKS = []
CONTAINERS = []
NETWORKS = []

SENSORS = [
    ("sensor-cron", "rimsky-sensor-cron", 9081, "RIMSKY_SENSOR_CRON_STATE_DSN", "sensor_cron_state"),
    ("sensor-http", "rimsky-sensor-http", 9082, "RIMSKY_SENSOR_HTTP_STATE_DSN", "sensor_http_state"),
    ("sensor-object-store", "rimsky-sensor-object-store", 9083,
     "RIMSKY_SENSOR_OBJECT_STORE_STATE_DSN", "sensor_object_store_state"),
    ("sensor-webhook", "rimsky-sensor-webhook", 9084,
     "RIMSKY_SENSOR_WEBHOOK_STATE_DSN", "sensor_webhook_state"),
]
ONE_DSN = "postgres://u:p@%s:5432/sensorstate?sslmode=disable" % PG
UNREACHABLE_DSN = "postgres://u:p@%s:5432/sensorstate?sslmode=disable" % ("no-such-host-" + SUFFIX)


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def die(msg):
    print("HARNESS ERROR: " + msg)
    teardown()
    sys.exit(2)


def teardown():
    for name in CONTAINERS:
        docker("rm", "-f", name)
    del CONTAINERS[:]
    for net in NETWORKS:
        docker("network", "rm", net)
    del NETWORKS[:]


def free_port():
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
    sock.close()
    return port


def check(label, ok, detail=""):
    CHECKS.append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + str(detail)[:300]) if detail else ""))


def finish():
    teardown()
    failed = [c for c in CHECKS if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(CHECKS), len(failed)))
    print("RESULT: " + ("FAIL" if failed else "PASS"))
    sys.exit(1 if failed else 0)


def image(name):
    ref = "%s:%s" % (name, TAG)
    if docker("image", "inspect", ref).returncode != 0:
        die("image %s is not present locally; build it with: make service-images" % ref)
    return ref


def run_detached(name, ref, env=None, publish=None, network=None):
    docker("rm", "-f", name)
    args = ["run", "-d", "--name", name]
    if network:
        args += ["--network", network]
    for host_port, guest_port in (publish or []):
        args += ["-p", "127.0.0.1:%d:%d" % (host_port, guest_port)]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(ref)
    res = docker(*args)
    CONTAINERS.append(name)
    if res.returncode != 0:
        die("docker run %s failed: %s" % (name, res.stderr.strip()))
    return name


def running(name):
    return docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() == "true"


def exit_code(name):
    return docker("inspect", "-f", "{{.State.ExitCode}}", name).stdout.strip()


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def await_tcp(name, port):
    while True:
        if not running(name):
            die("container %s exited before listening:\n%s" % (name, logs(name)[-1200:]))
        sock = socket.socket()
        try:
            sock.connect(("127.0.0.1", port))
            sock.close()
            return
        except Exception:
            time.sleep(0.15)


def await_exit(name):
    while running(name):
        time.sleep(0.15)
    return exit_code(name)


def psql(sql):
    res = docker("exec", PG, "psql", "-U", "u", "-d", "sensorstate", "-tAc", sql)
    return res.stdout.strip()


docker("network", "rm", NET)
if docker("network", "create", NET).returncode != 0:
    die("docker network create failed")
NETWORKS.append(NET)
run_detached(PG, "postgres:16-alpine", network=NET,
             env={"POSTGRES_USER": "u", "POSTGRES_PASSWORD": "p", "POSTGRES_DB": "sensorstate"})
while docker("exec", PG, "pg_isready", "-h", PG, "-U", "u", "-d", "sensorstate").returncode != 0:
    if not running(PG):
        die("postgres exited:\n" + logs(PG)[-1000:])
    time.sleep(0.3)

print("== all four sensors, one postgres, one connection string shape ==")
started = []
for sensor, img, guest_port, dsn_var, table in SENSORS:
    port = free_port()
    name = "exp-assumption-sensor-dsn-" + sensor + "-" + SUFFIX
    run_detached(name, image(img), network=NET, publish=[(port, guest_port)],
                 env={dsn_var: ONE_DSN, "RIMSKY_CONTROL_API_URL": "http://127.0.0.1:8080"})
    started.append((sensor, name, port, table))

for sensor, name, port, table in started:
    await_tcp(name, port)
check("all four sensors accept the same postgres DSN shape and reach their listener",
      all(running(name) for _, name, _, _ in started),
      "running: %s" % [s for s, name, _, _ in started if running(name)])

tables = sorted(psql("select tablename from pg_tables where schemaname='public'").splitlines())
tables = [t for t in tables if t]
expected = sorted(table for _, _, _, table in started)
check("each sensor bootstrapped its own state table in the one database",
      all(t in tables for t in expected), "tables present: %s" % tables)
prefixes = {sensor: sensor.replace("-", "_") for sensor, _, _, _ in started}
owned = {}
for table in [t for t in tables if t.startswith("sensor_")]:
    owned[table] = sorted(s for s, prefix in prefixes.items() if table.startswith(prefix))
check("every state table belongs to exactly one sensor, so one database backs all four without collision",
      all(len(v) == 1 for v in owned.values())
      and set(s for v in owned.values() for s in v) == set(prefixes),
      "; ".join("%s -> %s" % (t, v) for t, v in sorted(owned.items())))
check("a sensor that needs more than one table stays inside its own name space",
      sorted(t for t, v in owned.items() if v == ["sensor-object-store"])
      == ["sensor_object_store_seen_names", "sensor_object_store_state"],
      "object-store tables: %s" % sorted(t for t, v in owned.items() if v == ["sensor-object-store"]))

print("")
print("== the same variable, pointed at a database that is not there ==")
refusals = []
for sensor, img, guest_port, dsn_var, table in SENSORS:
    name = "exp-assumption-sensor-dsn-bad-" + sensor + "-" + SUFFIX
    run_detached(name, image(img), network=NET,
                 env={dsn_var: UNREACHABLE_DSN, "RIMSKY_CONTROL_API_URL": "http://127.0.0.1:8080"})
    code = await_exit(name)
    text = logs(name)
    refusals.append((sensor, code, "state db" in text))
check("every sensor refuses to run on an unreachable state DSN, and says so",
      all(code != "0" and named for _, code, named in refusals),
      "; ".join("%s exit=%s named=%s" % r for r in refusals))

finish()
