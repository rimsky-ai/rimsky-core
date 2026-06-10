---
tension: callback-hostname-split
category: unclear
status: open
affects:
  - executor
  - supervisor
---

# Supervisor binds on `0.0.0.0`; the advertised callback URL must be peer-reachable — two distinct hostnames

## What is muddy

The supervisor's async-callback HTTP listener binds on `0.0.0.0`, but executors need a peer-reachable hostname to dial back. The peer-reachable host is set via `callback.advertise_host` (YAML) or `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_HOST` (env). If empty, executors can't reach back.

Two distinct hostnames in play; the YAML config doesn't make the asymmetry obvious. Operators may assume the listener bind value is the advertise value.

Proxy-mediated executors do not sidestep the asymmetry: the `concept:host-agent-proxy` → supervisor callback hop has the same advertise-host requirement as any other executor → supervisor hop, so routing a dispatch through the proxy does not change the callback-reachability story for that hop. A separate hostname class joins the system on the dev-side path — the `concept:host-agent`'s local-listener address, used by spawned processes to POST callbacks back through the agent — but it is implicit (loopback by default) and reported to the proxy at registration time; it needs no advertise-host knob because agents dial outbound (the proxy never dials the agent).

## Why it matters

A misconfigured deployment fails silently: the supervisor accepts dispatches, the executor accepts work, but the final-outcome POST never arrives, and the dispatch ages into orphan-reap. Diagnosis requires correlating supervisor logs, executor logs, and network policy.

## Resolution candidates (do NOT pick)

- Require the advertised-callback-host setting (no default) and fail fast at startup if it is missing.
- Auto-derive the advertised host from the deployment environment.
- Add a startup self-probe that POSTs to its own advertised URL and warns if unreachable.

## Evidence

- `_discover/2026-05-10-executor-streamed-execute.md` "Non-obvious gotchas" para.
- `_discover/claude-agent-async-handoff-always.md` Description "callback_url" para.
- CLAUDE.md "Non-obvious gotchas" — "Two distinct callback hostnames."

