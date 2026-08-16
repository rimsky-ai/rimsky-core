"""Probe: what does `rimsky admin reset` target, and does it ask first?

Usage: probe.py <rimsky-bin> <base-url> <home-dir> <instance-id> <node-id>

The verb is driven on a real pty with "n" waiting on stdin, so a prompt would
be seen and answered. Around that the probe establishes what the verb accepts
as a target -- no argument, two arguments, an instance id, a node id -- and
re-reads the whole deployment afterwards to see whether anything beyond the
target moved.
"""

import json
import os
import pty
import select
import subprocess
import sys
import time

BIN, BASE, HOMEDIR, INSTANCE, NODE = sys.argv[1:6]
ENV = dict(os.environ)
ENV.update(HOME=HOMEDIR, RIMSKY_CONTROL_API_URL=BASE)
ENV.pop("RIMSKY_CONTEXT", None)


def quiet(*args):
    p = subprocess.run([BIN, *args], env=ENV, capture_output=True, text=True)
    return p.returncode, (p.stdout or "") + (p.stderr or "")


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


PROMPT_MARKS = ("[y/N]", "[Y/n]", "(y/n)", "y/n", "Proceed", "Are you sure", "confirm?")


def world():
    def rows(*a):
        try:
            return json.loads(quiet(*a)[1] or "[]") or []
        except ValueError:
            return []
    return (len(rows("template", "list", "-o", "json")),
            len(rows("instance", "list", "-o", "json")),
            len(rows("tag", "list", "-o", "json")))


fail = 0
print("== what does the verb accept as a target? ==")
for label, args in (("no argument", ["admin", "reset"]),
                    ("two arguments", ["admin", "reset", NODE, NODE]),
                    ("an instance id", ["admin", "reset", INSTANCE]),
                    ("a node id", ["admin", "reset", NODE])):
    rc, out = quiet(*args)
    first = out.strip().splitlines()[0] if out.strip() else ""
    print(f"  {label:16s} exit {rc}: {first[-96:]}")

print()
print("== does it ask before acting? ==")
before = world()
rc, text = on_pty(["admin", "reset", NODE])
after = world()
asked = any(m in text for m in PROMPT_MARKS)
print(f"  rimsky admin reset <node-id> on a pty with 'n' on stdin → exit {rc}")
print(f"  output: {text.strip().splitlines()[-1][:100] if text.strip() else '(nothing)'}")
if asked:
    print("PASS  the verb asked for confirmation")
else:
    print("FAIL  the verb asked nothing; it went straight to the request")
    fail = 1

print()
print("== is anything beyond the target touched? ==")
print(f"  templates/instances/tags before: {before}  after: {after}")
if before == after:
    print("PASS  the verb is scoped: nothing else in the deployment moved")
else:
    print("FAIL  the deployment changed beyond the target")
    fail = 1

print()
print("RESULT: PASS" if fail == 0 else "RESULT: FAIL")
sys.exit(fail)
