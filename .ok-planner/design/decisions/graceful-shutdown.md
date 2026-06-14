---
decision: graceful-shutdown
status: adopted
---

# graceful-shutdown

## Choice

On SIGINT, SIGTERM, `--timeout` expiry, or natural completion: supervisor stops new dispatches → in-flight dispatches and spawned children receive SIGTERM → 5-second hardcoded grace → SIGKILL on anything still running → control-api stops → SQL connection closes → `latest` symlink updates → exit. A second SIGINT escalates to hard exit (immediate SIGKILL, best-effort close).

## Rationale

5 seconds is a conservative SIGTERM-then-SIGKILL grace — well-behaved executors unwind within it, misbehaving ones get hard-killed without blocking the operator. The second-SIGINT escape hatch is the conventional "I really mean it" fallback.
