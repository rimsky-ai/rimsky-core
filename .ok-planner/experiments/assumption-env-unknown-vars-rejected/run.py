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

TYPOS = {
    "RIMSKY_CONTROL_API_PROT": "8099",
    "RIMSKY_SCHEDULR_TICK_MS": "5000",
    "RIMSKY_LOG_LEVE": "debug",
    "RIMSKY_METRICS_PORT_SCHEDULR": "9464",
    "RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOSTT": "supervisor.internal",
}


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


def logs(name):
    res = docker("logs", name)
    return res.stdout + res.stderr


def health(base):
    try:
        with urllib.request.urlopen(base + "/v1/health") as resp:
            return resp.status
    except urllib.error.HTTPError as exc:
        return exc.code
    except Exception:
        return 0


def boot(name, env, mounts, container_port):
    if docker("image", "inspect", ALL_IN_ONE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images" % ALL_IN_ONE)
    docker("rm", "-f", name)
    port = free_port()
    args = ["run", "-d", "--name", name, "-p", "127.0.0.1:%d:%d" % (port, container_port)]
    for host, guest in mounts:
        args += ["-v", "%s:%s:ro" % (host, guest)]
    for key, value in env.items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(ALL_IN_ONE)
    res = docker(*args)
    CONTAINERS.append(name)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
    base = "http://127.0.0.1:%d" % port
    while True:
        if docker("inspect", "-f", "{{.State.Running}}", name).stdout.strip() != "true":
            die("container %s exited during boot:\n%s" % (name, logs(name)[-2000:]))
        if health(base) == 200:
            return base
        time.sleep(0.3)


def run_to_exit(name, env, mounts):
    docker("rm", "-f", name)
    args = ["run", "--name", name]
    for host, guest in mounts:
        args += ["-v", "%s:%s:ro" % (host, guest)]
    for key, value in env.items():
        args += ["-e", "%s=%s" % (key, value)]
    args.append(ALL_IN_ONE)
    res = docker(*args)
    CONTAINERS.append(name)
    return res.returncode, res.stdout + res.stderr


print("== five misspelled RIMSKY_* variables ==")
base = boot("exp-assumption-env-unknown-typos", TYPOS, [], 8080)
check("a container carrying five misspelled variables comes up",
      health(base) == 200, "the deployment answers /v1/health")
boot_log = logs("exp-assumption-env-unknown-typos")
named = [v for v in TYPOS if v in boot_log]
check("startup names none of the five misspellings", not named, "named: %s" % named)
check("the setting the misspelling was aiming at kept its default",
      health(base) == 200,
      "RIMSKY_CONTROL_API_PROT=8099 left the control API on its usual port")

print("")
print("== the same setting, spelled correctly ==")
moved_port = free_port()
docker("rm", "-f", "exp-assumption-env-unknown-correct")
res = docker("run", "-d", "--name", "exp-assumption-env-unknown-correct",
             "-p", "127.0.0.1:%d:8099" % moved_port,
             "-e", "RIMSKY_CONTROL_API_PORT=8099", ALL_IN_ONE)
CONTAINERS.append("exp-assumption-env-unknown-correct")
if res.returncode != 0:
    die("docker run failed: " + res.stderr.strip())
moved = "http://127.0.0.1:%d" % moved_port
while True:
    if docker("inspect", "-f", "{{.State.Running}}",
              "exp-assumption-env-unknown-correct").stdout.strip() != "true":
        die("container exited during boot:\n" + logs("exp-assumption-env-unknown-correct")[-2000:])
    if health(moved) == 200:
        break
    time.sleep(0.3)
check("correctly spelled, the variable does move the control API",
      health(moved) == 200, "so the misspelling was a real miss, not a no-op setting")

print("")
print("== the same typo, made in the YAML instead ==")
cfgdir = tempfile.mkdtemp()
os.chmod(cfgdir, 0o777)
cfg = os.path.join(cfgdir, "rimsky.yml")
with open(cfg, "w") as fh:
    fh.write("persistance:\n"
             "  driver: sqlite\n"
             "persistence:\n"
             "  driver: sqlite\n"
             "  sqlite:\n"
             "    path: /var/lib/rimsky/state.db\n"
             "claim_producers: {}\n"
             "named_locks: {}\n"
             "executors: {}\n")
code, text = run_to_exit("exp-assumption-env-unknown-yamlkey", {},
                         [(cfg, "/etc/rimsky/rimsky.yml")])
check("an unknown YAML key stops the container", code != 0, "exit %d" % code)
check("and the message names the offending key", "persistance" in text,
      next((l.strip()[:120] for l in text.splitlines() if "persistance" in l), ""))

print("")
print("== a real variable given an unusable value ==")
code, text = run_to_exit("exp-assumption-env-unknown-badvalue",
                         {"RIMSKY_ENTRYPOINT_MIGRATE": "yes"}, [])
check("a bad value on a variable the product reads stops the container",
      code != 0, "exit %d" % code)
check("and the message names the variable and the value",
      "RIMSKY_ENTRYPOINT_MIGRATE" in text and "yes" in text,
      next((l.strip()[:160] for l in text.splitlines() if "RIMSKY_ENTRYPOINT_MIGRATE" in l), ""))

finish()
