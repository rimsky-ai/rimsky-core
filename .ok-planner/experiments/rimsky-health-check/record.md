---
experiment: rimsky-health-check
commit: PENDING
---

# The deployment-health probe, unauthenticated and persistence-dependent

The story makes two claims — the probe needs no credentials, and its answer
tracks persistence — so this directory holds two runnable ways. Both use the CLI
binary built from this tree alongside the probe route.

## way-probe-unauthenticated.py

### What it ran against

A `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG`, first in its default
anonymous mode and then after the deployment is moved off anonymous access.

### What was observed

With no credentials attached, the probe answered 200 with status `ok`, and the
health CLI verb reported the same and exited 0. After the deployment was moved
off anonymous access and an admin key was minted, an ordinary route answered 401
`no_token` to an unauthenticated caller, while the probe still answered 200 with
status `ok` to a caller carrying nothing.

Five checks, none failing.

## way-persistence-down.py

### What it ran against

A docker network carrying a postgres container and a `rimsky-all-in-one`
container running against it through a mounted postgres config. The script stops
the postgres container, then starts it again.

### What was observed

While postgres was up, the probe answered 200 with status `ok` and the health CLI
verb exited 0. After the postgres container was stopped, the probe answered 500
naming the failed transaction, and the health CLI verb exited 1 reporting that
status. After the postgres container was started again, the probe answered 200
with status `ok`.

Five checks, none failing.

RESULT: PASS
