import json
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
CHECKS = []
CONTAINERS = []


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


def state_dir():
    path = tempfile.mkdtemp()
    os.chmod(path, 0o777)
    return path


def boot(name, env, mounts, container_port):
    if docker("image", "inspect", ALL_IN_ONE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images" % ALL_IN_ONE)
    docker("rm", "-f", name)
    port = free_port()
    args = ["run", "-d", "--name", name, "-p", "127.0.0.1:%d:%d" % (port, container_port)]
    for host, guest in mounts:
        args += ["-v", "%s:%s" % (host, guest)]
    for key, value in env.items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(ALL_IN_ONE)
    res = docker(*args)
    CONTAINERS.append(name)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
    base = "http://127.0.0.1:%d" % port
    deadline_free = True
    while deadline_free:
        if docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() != "true":
            die("container %s exited during boot:\n%s" % (name, logs(name)[-2000:]))
        try:
            with urllib.request.urlopen(base + "/v1/health") as resp:
                if resp.status == 200:
                    return base
        except Exception:
            time.sleep(0.3)


def get(base, path):
    try:
        with urllib.request.urlopen(base + path) as resp:
            raw = resp.read().decode()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()
    except Exception as exc:
        return 0, str(exc)


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def names(payload, key):
    return sorted(entry["name"] for entry in (payload or {}).get(key, []))


print("== a container configured only by naturally-named RIMSKY_* variables ==")
plain = state_dir()
env_named_after_config_keys = {
    "RIMSKY_PERSISTENCE_DRIVER": "postgres",
    "RIMSKY_PERSISTENCE_POSTGRES_DSN": "postgres://nobody:nobody@127.0.0.1:5999/none?sslmode=disable",
    "RIMSKY_PERSISTENCE_SQLITE_PATH": "/var/lib/rimsky/from-env.db",
    "RIMSKY_PERSISTENCE_BLOB_BACKEND": "filesystem",
    "RIMSKY_PERSISTENCE_BLOB_FILESYSTEM_ROOT": "/var/lib/rimsky/blobs-from-env",
    "RIMSKY_EXECUTORS_FOO_ENDPOINT": "127.0.0.1:9999",
    "RIMSKY_EXECUTORS_FOO_TRANSPORT": "grpc",
    "RIMSKY_CLAIM_PRODUCERS_BAR_ENDPOINT": "grpc://127.0.0.1:9998",
    "RIMSKY_RETENTION_RECENT_FRAMES_KEPT": "1",
    "RIMSKY_DISPATCH_DEFAULTS_SYNC_RPC_DEADLINE": "1s",
    "RIMSKY_UNREACHABLE_VALIDATOR_POLICY": "fail_open",
}
base = boot("exp-assumption-env-overrides-envonly", env_named_after_config_keys,
            [(plain, "/var/lib/rimsky")], 8080)

check("a container told persistence.driver=postgres by environment still comes up",
      get(base, "/v1/health")[0] == 200,
      "an honored override would have pointed it at an unreachable postgres")
files = sorted(os.listdir(plain))
check("the database is at the baked persistence.sqlite.path, not the one the environment named",
      "state.db" in files and "from-env.db" not in files, "state dir: %s" % files)
check("no blob root appeared where RIMSKY_PERSISTENCE_BLOB_FILESYSTEM_ROOT named one",
      "blobs-from-env" not in files, "state dir: %s" % files)
executors = names(get(base, "/v1/observability/executors")[1], "executors")
check("the executor named by RIMSKY_EXECUTORS_FOO_ENDPOINT is not configured",
      "foo" not in executors, "executors: %s" % executors)
producers = names(get(base, "/v1/observability/claim-producers")[1], "claim_producers")
check("the claim producer named by RIMSKY_CLAIM_PRODUCERS_BAR_ENDPOINT is not configured",
      "bar" not in producers, "claim producers: %s" % producers)
boot_log = logs("exp-assumption-env-overrides-envonly")
mentioned = [v for v in env_named_after_config_keys if v in boot_log]
check("not one of the eleven variables is named anywhere in the startup log",
      not mentioned, "named: %s" % mentioned)

print("")
print("== the same settings, carried by a mounted file ==")
mounted = state_dir()
cfgdir = state_dir()
cfg = os.path.join(cfgdir, "rimsky.yml")
with open(cfg, "w") as fh:
    fh.write("persistence:\n"
             "  driver: sqlite\n"
             "  sqlite:\n"
             "    path: ${DB_PATH}\n"
             "claim_producers: {}\n"
             "named_locks: {}\n"
             "executors:\n"
             "  foo:\n"
             "    transport: grpc\n"
             "    endpoint: 127.0.0.1:9999\n")
base = boot("exp-assumption-env-overrides-mounted",
            {"DB_PATH": "/var/lib/rimsky/from-file.db"},
            [(mounted, "/var/lib/rimsky"), (cfg, "/etc/rimsky/rimsky.yml")], 8080)
executors = names(get(base, "/v1/observability/executors")[1], "executors")
check("the same executor declared in the mounted file is configured",
      "foo" in executors, "executors: %s" % executors)
files = sorted(os.listdir(mounted))
check("a ${VAR} reference inside the mounted file does reach the config",
      "from-file.db" in files and "state.db" not in files, "state dir: %s" % files)

print("")
print("== a deployment setting that does have its own variable ==")
port = free_port()
docker("rm", "-f", "exp-assumption-env-overrides-namedvar")
res = docker("run", "-d", "--name", "exp-assumption-env-overrides-namedvar",
             "-p", "127.0.0.1:%d:8099" % port,
             "-e", "RIMSKY_CONTROL_API_PORT=8099", ALL_IN_ONE)
CONTAINERS.append("exp-assumption-env-overrides-namedvar")
if res.returncode != 0:
    die("docker run failed: " + res.stderr.strip())
moved = "http://127.0.0.1:%d" % port
while True:
    if docker("inspect", "-f", "{{.State.Running}}",
              "exp-assumption-env-overrides-namedvar").stdout.strip() != "true":
        die("container exited during boot:\n" + logs("exp-assumption-env-overrides-namedvar")[-2000:])
    status, _ = get(moved, "/v1/health")
    if status == 200:
        break
    time.sleep(0.3)
check("RIMSKY_CONTROL_API_PORT moves the control API's listener",
      get(moved, "/v1/health")[0] == 200, "control API answered on the port the variable named")

finish()
