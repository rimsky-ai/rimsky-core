import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

SLUG = "assumption-http-list-routes-paginate"
TAG = os.environ.get("RIMSKY_IMAGE_TAG") or sys.exit("export RIMSKY_IMAGE_TAG=src-<tree hash> first")
IMAGE = "rimsky-all-in-one:" + TAG
NAME = "exp-" + SLUG
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", ".."))
CLI = os.path.join(ROOT, "bin", "rimsky")
STATE = {"base": None, "checks": []}

PAGED = [("/v1/instances", "instances"), ("/v1/templates", "templates"),
         ("/v1/events", "events"), ("/v1/audit", "audit"), ("/v1/tags", "tags"),
         ("/v1/observability/instances", "instances"),
         ("/v1/observability/events", "events"),
         ("/v1/observability/templates", "templates")]

LOGS = ("/v1/events", "/v1/audit", "/v1/observability/events")

UNPAGED = [("/v1/observability/executors", "executors"),
           ("/v1/observability/claim-producers", "claim_producers")]


def docker(*args):
    return subprocess.run(["docker", *args], capture_output=True, text=True)


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def die(msg):
    print("HARNESS ERROR: " + msg)
    docker("rm", "-f", NAME)
    sys.exit(2)


def call(method, path, body=None, token=None):
    headers = {"Content-Type": "application/json", "Idempotency-Key": uuid.uuid4().hex}
    if token:
        headers["Authorization"] = "Bearer " + token
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(STATE["base"] + path, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req) as resp:
            raw = resp.read().decode()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode()
        try:
            return exc.code, json.loads(raw)
        except ValueError:
            return exc.code, raw


def check(label, ok, detail=""):
    STATE["checks"].append((label, bool(ok)))
    print(("PASS  " if ok else "FAIL  ") + label + ((" | " + detail) if detail else ""))


def boot():
    if docker("image", "inspect", IMAGE).returncode != 0:
        die("image %s is not present locally; build it with: make core-images service-images test-images" % IMAGE)
    docker("rm", "-f", NAME)
    port = free_port()
    res = docker("run", "-d", "--name", NAME, "-p", "127.0.0.1:%d:8080" % port, IMAGE)
    if res.returncode != 0:
        die("docker run failed: " + res.stderr.strip())
    STATE["base"] = "http://127.0.0.1:%d" % port
    while True:
        try:
            if call("GET", "/v1/health")[0] == 200:
                return
        except Exception:
            pass
        if docker("inspect", "-f", "{{.State.Running}}", NAME).stdout.strip() != "true":
            die("container exited during boot:\n" + docker("logs", NAME).stdout + docker("logs", NAME).stderr)
        time.sleep(0.3)


def bootstrap_auth():
    env = dict(os.environ, HOME=tempfile.mkdtemp(prefix="rimsky-exp-"))
    env.pop("RIMSKY_API_KEY", None)
    env.pop("RIMSKY_CONTROL_API_URL", None)
    out = subprocess.run([CLI, "auth", "init", "--endpoint", STATE["base"]],
                         capture_output=True, text=True, env=env).stdout
    for line in out.splitlines():
        if "RIMSKY_API_KEY" in line and "for subsequent" in line:
            return line.split("RIMSKY_API_KEY=")[1].split(" ")[0].strip('"')
    die("could not read the admin plaintext out of:\n" + out)


def finish():
    docker("rm", "-f", NAME)
    failed = [c for c in STATE["checks"] if not c[1]]
    print("")
    print("%d checks, %d failed" % (len(STATE["checks"]), len(failed)))
    print("EXPERIMENT PASS" if not failed else "EXPERIMENT FAIL")
    sys.exit(1 if failed else 0)


def main():
    boot()
    admin = bootstrap_auth()

    template_ids = []
    for i in range(3):
        _, out = call("POST", "/v1/templates", {"spec": {
            "name": "paging-%d" % i, "version": "1",
            "nodes": [{"type": "w", "kind": "attribute_passthrough"}]}}, admin)
        template_ids.append(out["template_id"])
        call("POST", "/v1/templates/%s/deploy" % out["template_id"], {}, admin)
        call("POST", "/v1/tags", {"tag": "paging%d:v1" % i, "template": out["template_id"]}, admin)
        call("POST", "/v1/instances", {"template": out["template_id"],
                                       "instance_key": "paging-%d" % i,
                                       "target_agent": "audit-agent"}, admin)
    _, out = call("GET", "/v1/instances", None, admin)
    instance_id = out["instances"][0]["id"]

    print("== the named collections share limit / cursor / next_cursor exactly ==")
    for path, key in PAGED:
        status, page = call("GET", path + "?limit=1", None, admin)
        ok = (status == 200 and isinstance(page, dict)
              and sorted(page.keys()) == sorted([key, "next_cursor"])
              and len(page[key]) == 1 and page["next_cursor"])
        check("%-34s ?limit=1 returns {%s, next_cursor}" % (path, key), ok,
              "%s keys=%s" % (status, sorted(page.keys()) if isinstance(page, dict) else page))

    print("")
    print("== the cursor walks a settled collection with one loop ==")
    for path, key in [p for p in PAGED if p[0] not in LOGS]:
        rows = []
        cursor = ""
        pages = 0
        while True:
            _, page = call("GET", "%s?limit=1%s" % (
                path, "&cursor=" + urllib.parse.quote(cursor, safe="") if cursor else ""), None, admin)
            rows += [json.dumps(r, sort_keys=True) for r in (page[key] or [])]
            pages += 1
            cursor = page["next_cursor"]
            if not cursor:
                break
        check("%-34s one cursor loop terminates on %d distinct rows" % (path, len(set(rows))),
              not cursor and len(rows) == 3 and len(set(rows)) == 3,
              "%d pages, %d rows" % (pages, len(rows)))

    print("")
    print("== the event log grows while it is read, so its cursor only ever advances ==")
    for path, key in [(p, k) for p, k in PAGED if p in LOGS]:
        ids = []
        cursor = ""
        for _ in range(3):
            _, page = call("GET", "%s?limit=1%s" % (
                path, "&cursor=" + urllib.parse.quote(cursor, safe="") if cursor else ""), None, admin)
            ids += [r["id"] for r in (page[key] or [])]
            cursor = page["next_cursor"]
        check("%-34s three pages give three descending ids, newest first" % path,
              len(ids) == 3 and ids == sorted(ids, reverse=True) and len(set(ids)) == 3, str(ids))

    print("")
    print("== PRIOR CONTRADICTED: an empty page is [] on the core routes and null on observability ==")
    _, core_empty = call("GET", "/v1/instances/%s/frames" % instance_id, None, admin)
    _, obs_empty = call("GET", "/v1/observability/frames?limit=1", None, admin)
    check("an empty frames page is [] on the core route and null on the observability route",
          core_empty["frames"] == [] and obs_empty["frames"] is None,
          "core=%r obs=%r" % (core_empty["frames"], obs_empty["frames"]))

    print("")
    print("== PRIOR CONTRADICTED: two observability collections do not paginate at all ==")
    for path, key in UNPAGED:
        status, page = call("GET", path + "?limit=1", None, admin)
        check("%-38s answers with no next_cursor key" % path,
              status == 200 and "next_cursor" not in page,
              "keys=%s" % sorted(page.keys()))
    _, one = call("GET", "/v1/observability/executors?limit=1", None, admin)
    _, all_of = call("GET", "/v1/observability/executors", None, admin)
    check("/v1/observability/executors ignores ?limit= entirely",
          len(one["executors"]) == len(all_of["executors"]) and len(all_of["executors"]) > 1,
          "limit=1 gave %d of %d" % (len(one["executors"]), len(all_of["executors"])))

    print("")
    print("== PRIOR CONTRADICTED: instance-nested collections use three more shapes ==")
    _, page = call("GET", "/v1/instances/%s/nodes?limit=1" % instance_id, None, admin)
    check("/v1/instances/{id}/nodes         keeps next_cursor", "next_cursor" in page,
          "keys=%s" % sorted(page.keys()))
    for path, key in [("/frames", "frames"), ("/messages", "messages")]:
        _, page = call("GET", "/v1/instances/%s%s?limit=1" % (instance_id, path), None, admin)
        check("/v1/instances/{id}%-14s omits next_cursor on the last page" % path,
              sorted(page.keys()) == [key], "keys=%s" % sorted(page.keys()))
    for path, key in [("/assets", "assets"), ("/breakpoints", "breakpoints")]:
        _, page = call("GET", "/v1/instances/%s%s?limit=1" % (instance_id, path), None, admin)
        check("/v1/instances/{id}%-14s has no cursor field at all" % path,
              sorted(page.keys()) == [key], "keys=%s" % sorted(page.keys()))
    _, page = call("GET", "/v1/instances/%s/breakpoint-hits?limit=1" % instance_id, None, admin)
    check("/v1/instances/{id}/breakpoint-hits pages on since/next_since/truncated instead",
          sorted(page.keys()) == ["hits", "next_since", "truncated"],
          "keys=%s" % sorted(page.keys()))
    _, page = call("GET", "/v1/claim-handles/11111111-1111-1111-1111-111111111111/holders",
                   None, admin)
    check("/v1/claim-handles/{id}/holders    has no cursor field at all",
          sorted(page.keys()) == ["holders"], "keys=%s" % sorted(page.keys()))

    print("")
    print("== PRIOR CONTRADICTED: the cursor is not one opaque token, and a bad one is not one error ==")
    _, page = call("GET", "/v1/instances?limit=1", None, admin)
    core_cursor = page["next_cursor"]
    _, page = call("GET", "/v1/tags?limit=1", None, admin)
    tag_cursor = page["next_cursor"]
    check("core cursors are base64 blobs, the tag cursor is the raw tag value",
          core_cursor.startswith("ey") and tag_cursor == "paging0:v1",
          "core=%s... tags=%s" % (core_cursor[:12], tag_cursor))
    for path in ["/v1/instances", "/v1/templates", "/v1/events", "/v1/audit",
                 "/v1/observability/instances"]:
        status, _ = call("GET", path + "?cursor=not-a-cursor", None, admin)
        check("%-34s answers 500 on a malformed cursor" % path, status == 500, str(status))
    status, page = call("GET", "/v1/tags?cursor=not-a-cursor", None, admin)
    check("/v1/tags                           answers 200 and silently pages from it instead",
          status == 200, "%s %s" % (status, json.dumps(page)[:70]))

    print("")
    print("== a bad ?limit= is not one behaviour either ==")
    status, page = call("GET", "/v1/instances?limit=abc", None, admin)
    check("/v1/instances?limit=abc            falls back to the default and answers 200",
          status == 200, str(status))
    status, page = call("GET", "/v1/observability/events?limit=abc", None, admin)
    check("/v1/observability/events?limit=abc rejects it with 400",
          status == 400, "%s %s" % (status, json.dumps(page)[:70]))

    finish()


main()
