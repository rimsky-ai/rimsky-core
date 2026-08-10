import json
import os
import subprocess

from harness import (STATE, boot, call, check, deploy, die, docker, finish, new_instance,
                     new_network, quiet, send_message, show, start_container, tmpdir)

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")

PRODUCERS = {
    "content-a": "content/release_refused",
    "content-b": "storage/quota_exceeded",
}

RIMSKY_YML = """persistence:
  driver: sqlite
  sqlite:
    path: /var/lib/rimsky/state.db
claim_producers:
  content-a:
    endpoint: "grpc://producer-a:9500"
    tls: "off"
    write_semantics_allowed: ["sync"]
    protocols: ["claim_producer", "data_processing"]
  content-b:
    endpoint: "grpc://producer-b:9500"
    tls: "off"
    write_semantics_allowed: ["sync"]
    protocols: ["claim_producer", "data_processing"]
named_locks: {}
executors: {}
"""


def node(producer):
    return {
        "type": "materializer-" + producer[-1],
        "kind": "attribute_passthrough",
        "claim_producers": [{"name": producer, "selector": "datasets/" + producer,
                             "intent": "rw", "alias": "dataset", "lifetime": "durable"}],
        "error_types": {"acquire/unavailable": {"action": "give_up"}},
        "attributes": {"schema": {"type": "object", "properties": {
            "rows": {"type": "integer", "default": 3}}}},
    }


SPEC = {
    "name": "exp-producer-error-passthrough",
    "version": "1",
    "nodes": [node(p) for p in sorted(PRODUCERS)],
}


def cli(*args):
    res = subprocess.run([CLI, *args, "--endpoint", STATE["base"]],
                         capture_output=True, text=True)
    return res.returncode, res.stdout.strip(), res.stderr.strip()


def build_producer():
    bindir = tmpdir()
    arch = docker("version", "--format", "{{.Server.Arch}}").stdout.strip() or "arm64"
    env = dict(os.environ, GOOS="linux", GOARCH=arch, CGO_ENABLED="0")
    build = subprocess.run(["go", "build", "-o", os.path.join(bindir, "producer"), HERE],
                           capture_output=True, text=True, cwd=ROOT, env=env)
    if build.returncode != 0:
        die("producer build failed:\n" + build.stderr)
    return bindir


def main():
    bindir = build_producer()
    net = new_network()
    for producer, klass in sorted(PRODUCERS.items()):
        start_container("alpine:latest",
                        ["/exp/producer", "-bind", "0.0.0.0:9500", "-fail-release", klass],
                        network=net, alias="producer-" + producer[-1], mounts=[(bindir, "/exp")])
    work = tmpdir()
    cfg = os.path.join(work, "rimsky.yml")
    with open(cfg, "w") as fh:
        fh.write(RIMSKY_YML)
    boot(mounts=[(cfg, "/etc/rimsky/rimsky.yml")], network=net)

    iid = new_instance(deploy(SPEC))
    send_message(iid)
    tl = quiet(iid)
    show(tl)

    seen = {}
    for producer, klass in sorted(PRODUCERS.items()):
        alias = "materializer-%s.dataset" % producer[-1]
        print("--- the operator retires the asset held by %s, and the producer refuses" % producer)
        status, body = call("DELETE", "/v1/instances/%s/assets/%s" % (iid, alias))
        print("    DELETE %s -> %s %s" % (alias, status, json.dumps(body)))
        seen[producer] = body
        check("the %s operation failed rather than reporting success" % producer,
              status >= 400, str(status))
        check("the response carries the error class %s declared" % producer,
              isinstance(body, dict) and body.get("error_class") == klass,
              json.dumps(body)[:300])
        check("the response carries the message %s wrote" % producer,
              isinstance(body, dict)
              and "the object store refused to drop claim" in (body.get("message") or ""),
              json.dumps(body.get("message"))[:300])
        check("the response names %s as the producer that failed" % producer,
              isinstance(body, dict) and body.get("producer_name") == producer,
              json.dumps(body)[:200])
        check("the status distinguishes a producer rejection from a rimsky internal error",
              status in (422, 502), str(status))
        check("the response names the verb the producer was running",
              isinstance(body, dict) and "Release" in (body.get("error") or ""),
              json.dumps(body.get("error"))[:200])

    classes = {p: (b or {}).get("error_class") for p, b in seen.items()}
    check("the class in the response follows the producer, so it is not a rimsky constant",
          classes == PRODUCERS, json.dumps(classes))

    print("--- the CLI reports the same failure")
    rc, out, err = cli("asset", "delete", "--instance", iid, "materializer-a.dataset")
    print("    rimsky asset delete -> rc=%s out=%s err=%s" % (rc, out, err))
    check("the CLI exits non-zero and repeats the producer's message",
          rc != 0 and "the object store refused to drop claim" in (out + err), (out + err)[:300])

    rc, out, _ = cli("asset", "list", "--instance", iid, "-o", "json")
    check("the refused retires left both assets in place",
          rc == 0 and len(json.loads(out or "[]")) == 2, out[:200])
    finish()


try:
    main()
finally:
    from harness import teardown
    teardown()
