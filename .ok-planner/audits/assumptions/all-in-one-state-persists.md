---
assumption: all-in-one-state-persists
commit: PENDING
disposition: held
synthesized: 2026-08-16T05:48:16Z
---

# mounting `/var/lib/rimsky` on the all-in-one image preserves templates, instances, and event history across container restarts, since that is the declared volume and holds `state.db`.

As local developer, I would take it that mounting `/var/lib/rimsky` on the all-in-one image preserves templates, instances, and event history across container restarts, since that is the declared volume and holds `state.db`.

## Source

name-promise — `/var/lib/rimsky` declared as VOLUME with `/var/lib/rimsky/state.db` inside it

## What a run would observe

create a template and instance in the all-in-one, restart the container with the volume mounted, and list them again.

## Measured

Experiment `assumption-all-in-one-state-persists` (seven checks, none failing)
built up a deployment in a `rimsky-all-in-one` container over a host directory
mounted at `/var/lib/rimsky` — one template, one instance under a fixed key,
eleven events over five kinds — then destroyed the container, started a fresh
one over the same directory, and read everything back. The prior holds. The
template returned under the same id, the instance under the same id and key, the
event history whole at the same eleven events, and the messages and per-node run
counts with it. `state.db` and its write-ahead log sit on the mounted directory,
and a container started with nothing mounted comes up with no templates and no
instances, so the mount is what carries the history. Replacing the container is
safe for a local developer as long as the declared volume is mounted.
