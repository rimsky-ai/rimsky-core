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

## Why it matters

A misconfigured deployment fails silently: the supervisor accepts dispatches, the executor accepts work, but the final-outcome POST never arrives, and the dispatch ages into orphan-reap. Diagnosis requires correlating supervisor logs, executor logs, and network policy.

## Resolution candidates (do NOT pick)

- Make `callback.advertise_host` required (no default); fail fast at startup if missing.
- Auto-derive from environment (`POD_IP`, `HOSTNAME`).
- Add a startup self-probe that POSTs to its own advertised URL and warns if unreachable.

## Evidence

- `_discover/2026-05-10-executor-streamed-execute.md` "Non-obvious gotchas" para.
- `_discover/claude-agent-async-handoff-always.md` Description "callback_url" para.
- CLAUDE.md "Non-obvious gotchas" — "Two distinct callback hostnames."

