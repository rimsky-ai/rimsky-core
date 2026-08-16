import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

TAG = os.environ.get("RIMSKY_IMAGE_TAG")
if not TAG:
    print("HARNESS ERROR: export RIMSKY_IMAGE_TAG=src-<tree hash> first")
    sys.exit(2)
ALL_IN_ONE = "rimsky-all-in-one:" + TAG
NET = "exp-assumption-metrics-net"
PG = "exp-assumption-metrics-pg"
METRICS_PORT = 9464
CHECKS = []
CONTAINERS = []
NETWORKS = []

FS_YML = ("root: /data\n"
          "pick_policies:\n"
          "  work:\n"
          "    root: work\n"
          "    folder_pattern: \".*\"\n"
          "    on_commit: pop\n"
          "    on_give_up: recycle\n"
          "    visibility_timeout_seconds: 60\n"
          "    sync_strategy: on_drain\n")

PG_YML = ("connection: postgres://rimsky:rimsky@" + PG + ":5432/rimsky?sslmode=disable\n"
          "write_semantics: read_only\n")

SERVICES = [
    ("sensor-cron", "rimsky-sensor-cron", [9081], {}, []),
    ("sensor-http", "rimsky-sensor-http", [9082], {}, []),
    ("sensor-object-store", "rimsky-sensor-object-store", [9083], {}, []),
    ("sensor-webhook", "rimsky-sensor-webhook", [9084, 9184], {}, []),
    ("http-node", "rimsky-executor-http-node", [9091, 9092], {}, []),
    ("verifier-http", "rimsky-executor-verifier-http", [9096], {}, []),
    ("verifier-shape-checks", "rimsky-executor-verifier-shape-checks", [9095], {}, []),
    ("claude-agent", "rimsky-executor-claude-agent", [9090, 9190],
     {"ANTHROPIC_API_KEY": "unused-by-this-run", "RIMSKY_EXECUTOR_STUB_MODE": "1"}, []),
    ("claim-producer-filesystem", "rimsky-claim-producer-filesystem", [9100, 9110],
     {"RIMSKY_CLAIM_PRODUCER_FILESYSTEM_CONFIG": "/cfg/fs.yml"}, [("CFG", "/cfg"), ("DATA", "/data")]),
    ("claim-producer-postgres", "rimsky-claim-producer-postgres", [9101, 9111],
     {"RIMSKY_CLAIM_PRODUCER_POSTGRES_CONFIG": "/cfg/pg.yml"}, [("CFG", "/cfg")]),
]


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
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def finish():
    teardown()
    failed = [c for c in CHECKS if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(CHECKS), len(failed)))
    print("RESULT: " + ("FAIL" if failed else "PASS"))
    sys.exit(1 if failed else 0)


def tempdir():
    path = tempfile.mkdtemp()
    os.chmod(path, 0o777)
    return path


def running(name):
    return docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() == "true"


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def run_detached(name, image, env=None, publish=None, mounts=None, network=None):
    docker("rm", "-f", name)
    args = ["run", "-d", "--name", name]
    if network:
        args += ["--network", network]
    for host_port, guest_port in (publish or []):
        args += ["-p", "127.0.0.1:%d:%d" % (host_port, guest_port)]
    for host, guest in (mounts or []):
        args += ["-v", "%s:%s" % (host, guest)]
    for key, value in (env or {}).items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(image)
    res = docker(*args)
    CONTAINERS.append(name)
    if res.returncode != 0:
        die("docker run %s failed: %s" % (name, res.stderr.strip()))
    return name


def await_tcp(name, port):
    while True:
        if not running(name):
            die("container %s exited before listening:\n%s" % (name, logs(name)[-1500:]))
        sock = socket.socket()
        try:
            sock.connect(("127.0.0.1", port))
            sock.close()
            return
        except Exception:
            time.sleep(0.15)


def fetch(port, path):
    url = "http://127.0.0.1:%d%s" % (port, path)
    try:
        with urllib.request.urlopen(url, timeout=30) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace")
    except Exception as exc:
        return 0, str(exc)


def is_prometheus(status, body):
    return status == 200 and "# HELP" in body and "# TYPE" in body


if docker("image", "inspect", ALL_IN_ONE).returncode != 0:
    die("image %s is not present locally; build it with: make core-images service-images" % ALL_IN_ONE)
docker("network", "rm", NET)
if docker("network", "create", NET).returncode != 0:
    die("docker network create failed")
NETWORKS.append(NET)

print("== the three core roles, with a metrics port opened ==")
api_port = free_port()
role_ports = {"scheduler": free_port(), "supervisor": free_port(), "control-api": free_port()}
run_detached("exp-assumption-metrics-core", ALL_IN_ONE, network=NET,
             env={"RIMSKY_METRICS_HOST": "0.0.0.0", "RIMSKY_METRICS_PORT": str(METRICS_PORT)},
             publish=[(api_port, 8080),
                      (role_ports["scheduler"], METRICS_PORT),
                      (role_ports["supervisor"], METRICS_PORT + 1),
                      (role_ports["control-api"], METRICS_PORT + 2)])
while fetch(api_port, "/v1/health")[0] != 200:
    if not running("exp-assumption-metrics-core"):
        die("all-in-one exited during boot:\n" + logs("exp-assumption-metrics-core")[-1500:])
    time.sleep(0.3)
for role, port in sorted(role_ports.items()):
    await_tcp("exp-assumption-metrics-core", port)
    status, body = fetch(port, "/metrics")
    check("the %s role serves prometheus text at /metrics" % role, is_prometheus(status, body),
          "status %s, first line %r" % (status, body.splitlines()[0] if body else ""))
status, _ = fetch(api_port, "/metrics")
check("the control API's own port does not carry /metrics", status == 404,
      "status %s — the metrics listener is a separate port" % status)

print("")
print("== the eleven bundled services, each given the same metrics variables ==")
run_detached(PG, "postgres:16-alpine", network=NET,
             env={"POSTGRES_USER": "rimsky", "POSTGRES_PASSWORD": "rimsky", "POSTGRES_DB": "rimsky"})
while docker("exec", PG, "pg_isready", "-h", PG, "-U", "rimsky").returncode != 0:
    if not running(PG):
        die("postgres exited:\n" + logs(PG)[-1000:])
    time.sleep(0.3)

cfgdir = tempdir()
datadir = tempdir()
os.makedirs(os.path.join(datadir, "work"), exist_ok=True)
os.chmod(os.path.join(datadir, "work"), 0o777)
with open(os.path.join(cfgdir, "fs.yml"), "w") as fh:
    fh.write(FS_YML)
with open(os.path.join(cfgdir, "pg.yml"), "w") as fh:
    fh.write(PG_YML)
places = {"CFG": cfgdir, "DATA": datadir}

started = []
for service, image, guest_ports, env, mounts in SERVICES:
    full = image + ":" + TAG
    if docker("image", "inspect", full).returncode != 0:
        die("image %s is not present locally; build it with: make service-images" % full)
    env = dict(env)
    env["RIMSKY_METRICS_HOST"] = "0.0.0.0"
    env["RIMSKY_METRICS_PORT"] = str(METRICS_PORT)
    ports = {}
    publish = []
    for guest in guest_ports + [METRICS_PORT]:
        host = free_port()
        ports[guest] = host
        publish.append((host, guest))
    name = "exp-assumption-metrics-" + service
    run_detached(name, full, env=env, publish=publish,
                 mounts=[(places[a], b) for a, b in mounts], network=NET)
    started.append((service, name, guest_ports, ports))

serving = []
for service, name, guest_ports, ports in started:
    await_tcp(name, ports[guest_ports[0]])
    for guest, host in sorted(ports.items()):
        status, body = fetch(host, "/metrics")
        if is_prometheus(status, body):
            serving.append("%s:%d" % (service, guest))
check("no listening port of any of the ten services with listeners answers /metrics",
      not serving, "answering: %s" % serving)
check("the metrics port the variables named was never opened by any of them",
      not [1 for _, _, _, ports in started
           if fetch(ports[METRICS_PORT], "/metrics")[0] != 0],
      "every probe of %d was refused" % METRICS_PORT)

ol_image = "rimsky-subscriber-openlineage:" + TAG
if docker("image", "inspect", ol_image).returncode != 0:
    die("image %s is not present locally; build it with: make service-images" % ol_image)
ol_metrics = free_port()
ol = run_detached("exp-assumption-metrics-openlineage", ol_image, network=NET,
                  publish=[(ol_metrics, METRICS_PORT)],
                  env={"RIMSKY_METRICS_HOST": "0.0.0.0",
                       "RIMSKY_METRICS_PORT": str(METRICS_PORT),
                       "RIMSKY_OPENLINEAGE_RIMSKY_DSN":
                           "postgres://rimsky:rimsky@%s:5432/rimsky?sslmode=disable" % PG,
                       "RIMSKY_OPENLINEAGE_BACKEND_URL": "http://127.0.0.1:9999"})
while True:
    res = docker("exec", PG, "psql", "-U", "rimsky", "-d", "rimsky", "-tAc",
                 "select to_regclass('rimsky_openlineage_cursor') is not null")
    if res.stdout.strip() == "t":
        break
    if not running(ol):
        die("openlineage exited before reaching its store:\n" + logs(ol)[-1500:])
    time.sleep(0.3)
status, _ = fetch(ol_metrics, "/metrics")
check("the eleventh service opens no port at all, metrics variables or not",
      status == 0, "the probe of %d was refused" % METRICS_PORT)

check("the image set declares no metrics port for any bundled service",
      not [s for s, image, _, _, _ in SERVICES + [("subscriber-openlineage", "rimsky-subscriber-openlineage", [], {}, [])]
           if str(METRICS_PORT) in (docker("image", "inspect", image + ":" + TAG,
                                           "--format", "{{json .Config.ExposedPorts}}").stdout or "")],
      "no EXPOSE names a metrics port")

finish()
