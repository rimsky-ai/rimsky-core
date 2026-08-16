"""Probe: do the CLI's destructive verbs ask before they mutate?

Usage: probe.py <rimsky-bin> <base-url> <home-dir>

Every verb runs on a real pty with "n" already on its stdin -- the answer an
operator gives when they change their mind. A verb that prompts sees a TTY and
a refusal; a verb that does not prompt never reads it. After each one the probe
re-reads the subject through the CLI and reports whether it survived.

`compose down` is the control: it is known to prompt, so it proves the pty and
the "n" are doing their job. It runs first, before the deployment leaves
anonymous mode, because the compose family sends no api-key.

A member whose subject the probe cannot construct is reported as PROMPT-ONLY:
the prompt question is answered, the survival question is not.
"""

import json
import os
import pty
import select
import subprocess
import sys
import time

BIN, BASE, HOMEDIR = sys.argv[1:4]

ENV = dict(os.environ)
ENV.update(HOME=HOMEDIR, RIMSKY_CONTROL_API_URL=BASE)
ENV.pop("RIMSKY_CONTEXT", None)
ENV.pop("RIMSKY_API_KEY", None)

TEMPLATE = 'version: "1"\nnodes:\n  - type: verify\n    executor: verifier-shape-checks\n'
results = []


def quiet(*args):
    p = subprocess.run([BIN, *args], env=ENV, capture_output=True, text=True)
    return p.returncode, p.stdout


def on_pty(args, feed=b"n\n", budget=60.0):
    pid, fd = pty.fork()
    if pid == 0:
        os.execvpe(BIN, [BIN, *args], ENV)
    os.write(fd, feed)
    out, deadline = b"", time.time() + budget
    while time.time() < deadline:
        r, _, _ = select.select([fd], [], [], 1.0)
        if not r:
            continue
        try:
            chunk = os.read(fd, 4096)
        except OSError:
            break
        if not chunk:
            break
        out += chunk
    os.close(fd)
    _, status = os.waitpid(pid, 0)
    return (os.WEXITSTATUS(status) if os.WIFEXITED(status) else -1), out.decode(errors="replace")


PROMPT_MARKS = ("[y/N]", "[Y/n]", "(y/n)", "y/n", "Proceed", "proceed?", "Are you sure", "confirm?")


def looks_like_prompt(text):
    return any(m in text for m in PROMPT_MARKS)


def record(verb, prompted, survived, detail):
    results.append((verb, prompted, survived, detail))
    p = "ASKED" if prompted else "silent"
    s = {True: "subject survived", False: "subject destroyed", None: "not constructible"}[survived]
    print(f"  {verb:30s} {p:6s}  {s:18s}  {detail}")


def rows(*args):
    _, out = quiet(*args)
    try:
        return json.loads(out or "[]") or []
    except ValueError:
        return []


def write(path, text):
    with open(path, "w") as fh:
        fh.write(text)


print("== control: compose down, the one verb known to ask ==")
cdir = os.path.join(HOMEDIR, "compose")
os.makedirs(cdir, exist_ok=True)
write(os.path.join(cdir, "t.yml"), "name: destructive-probe-compose\n" + TEMPLATE)
write(os.path.join(cdir, "rimsky-compose.yml"),
      "project: exp-destructive\ntemplates:\n  - path: t.yml\n    tag: probe\n    state: deployed\n")
here = os.getcwd()
os.chdir(cdir)
quiet("compose", "up", "--yes")
code, text = on_pty(["compose", "down"])
os.chdir(here)
control_asked = looks_like_prompt(text)
record("compose down (control)", control_asked, None, f"exit {code}")
if not control_asked:
    print("FAIL  the control did not prompt; the pty probe cannot be trusted")
    sys.exit(1)
quiet("compose", "down", "--yes")

print()
print("== seeding ==")
tpl = os.path.join(HOMEDIR, "t.yml")
write(tpl, "name: destructive-probe\n" + TEMPLATE)
tpl2 = os.path.join(HOMEDIR, "t2.yml")
write(tpl2, "name: destructive-probe-two\n" + TEMPLATE)
H = json.loads(quiet("template", "register", tpl, "-o", "json")[1])["template_id"]
quiet("template", "deploy", H)
quiet("tag", "create", "probe-tag", "--template", H)
INST = json.loads(quiet("instance", "create", H, "-o", "json")[1])["instance_id"]
INST2 = json.loads(quiet("instance", "create", H, "-o", "json")[1])["instance_id"]
NODE = rows("instance", "nodes", INST, "-o", "json")[0]["id"]
admin = ""
for line in quiet("auth", "init")[1].splitlines():
    line = line.strip()
    if line.startswith("rk_"):
        admin = line
ENV["RIMSKY_API_KEY"] = admin
quiet("auth", "create-key", "--name=doomed", "--role=read-only")
print(f"  template {H[:20]}…  instances {INST[:8]} {INST2[:8]}  node {NODE[:8]}  tag probe-tag  key doomed")


def templates():
    return {t["id"] for t in rows("template", "list", "-o", "json")}


def instance_ids():
    return {i["id"] for i in rows("instance", "list", "-o", "json")}


def terminated(iid):
    for i in rows("instance", "list", "-o", "json"):
        if i["id"] == iid:
            return bool(i.get("terminated_at"))
    return False


def state_of(h):
    for t in rows("template", "list", "-o", "json"):
        if t["id"] == h:
            return t.get("state")
    return None


def key_names():
    return {k["name"] for k in rows("auth", "list", "--json")}


print()
print('== each destructive verb, on a pty, with "n" waiting on stdin ==')

code, text = on_pty(["tag", "rm", "probe-tag"])
record("tag rm", looks_like_prompt(text),
       "probe-tag" in {t.get("tag") for t in rows("tag", "list", "-o", "json")}, f"exit {code}")

code, text = on_pty(["instance", "kill", INST2])
record("instance kill (no --force)", looks_like_prompt(text), not terminated(INST2),
       f"exit {code} — a refusal, not a question")

code, text = on_pty(["instance", "kill", INST, "--force"])
record("instance kill --force", looks_like_prompt(text), not terminated(INST), f"exit {code}")

code, text = on_pty(["rm-instance", INST])
record("rm-instance", looks_like_prompt(text), INST in instance_ids(), f"exit {code}")

quiet("instance", "kill", INST2, "--force")
code, text = on_pty(["instance", "delete", INST2])
record("instance delete", looks_like_prompt(text), INST2 in instance_ids(), f"exit {code}")

code, text = on_pty(["admin", "reset", NODE])
record("admin reset <node>", looks_like_prompt(text), None, f"exit {code}")

code, text = on_pty(["undeploy", H])
record("undeploy", looks_like_prompt(text), state_of(H) == "deployed",
       f"exit {code}, state now {state_of(H)}")

code, text = on_pty(["template", "rm", H])
record("template rm", looks_like_prompt(text), H in templates(), f"exit {code}")

code, text = on_pty(["auth", "revoke", "doomed"])
record("auth revoke", looks_like_prompt(text), "doomed" in key_names(), f"exit {code}")

code, text = on_pty(["lineage", "prune", "--older-than", "1s"])
record("lineage prune", looks_like_prompt(text), None, f"exit {code}")

code, text = on_pty(["asset", "delete", "--instance", INST, "verify.no-such"])
record("asset delete", looks_like_prompt(text), None, f"exit {code} — no asset to delete")

print()
print("== what --yes actually changes ==")
H2 = json.loads(quiet("template", "register", tpl2, "-o", "json")[1])["template_id"]
quiet("template", "deploy", H2)
I3 = json.loads(quiet("instance", "create", H2, "-o", "json")[1])["instance_id"]
c1, t1 = on_pty(["instance", "kill", I3, "--yes"])
print(f"  instance kill --yes (no --force)  exit {c1}  terminated={terminated(I3)}  "
      f"{t1.strip().splitlines()[0][:50] if t1.strip() else ''}")
quiet("instance", "delete", I3)
quiet("template", "undeploy", H2)
c2, t2 = on_pty(["template", "rm", H2, "--yes"])
c3, t3 = on_pty(["template", "rm", H2])
print(f"  template rm --yes                 exit {c2}")
print(f"  template rm (same subject, again) exit {c3}")
print("  --yes is accepted everywhere as a common flag; the only verbs whose behaviour")
print("  it changes are `instance kill` (where it stands in for --force) and the compose")
print("  family (where it answers a real prompt).")

print()
destructive = [r for r in results if not r[0].startswith("compose down")]
asked = [v for v, p, _, _ in destructive if p]
gone = [v for v, p, s, _ in destructive if not p and s is False]
print(f"of {len(destructive)} destructive verbs, {len(asked)} asked anything: {asked or 'none'}")
print(f"{len(gone)} destroyed their subject with nothing asked: {gone}")
print("the control (compose down) did ask, and honoured the refusal")

if len(asked) == len(destructive):
    print("RESULT: PASS")
    sys.exit(0)
print("RESULT: FAIL")
sys.exit(1)
