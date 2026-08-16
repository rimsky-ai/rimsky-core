---
trap: compose-restart-supervises
release: d977250c
demonstration: experiment:assumption-compose-restart-supervises
---
## Assumption

As operator declaring an instance, I would take it that `instances[].restart` means the compose runtime re-creates or restarts an instance when it terminates, the way a container restart policy does.

name-promise — a manifest key named `restart` in a docker-compose-shaped manifest

## Actual behavior

the experiment — built for
this run — brought up two compose projects against one `rimsky-all-in-one`
from this tree's image set, one declaring `restart: always` and one `restart:
never`, force-terminated each instance, and then polled the instance listing
without running anything else.

Nothing re-created either instance. Under `restart: always` the live count
went from 1 to 0 across 30 polls and stayed there, while `compose status`
continued to report the instance as `in-manifest` with no indication it had
terminated — the operator's status view looks the same whether the instance is
running or dead.

The key is real, but it is read only by the next hand-run `compose up`. At
that point `compose plan` under `always` showed `- instance-delete
compose:restart-always:one (restart=always)` followed by `+ create …`, and
applying it restored the instance; under `never` the same situation planned
`no changes`. So `restart` is a classification rule for what a future apply
does with an already-terminated instance, not a supervision policy. Between
applies there is no compose runtime at all — `compose up` is a one-shot
reconciler that exits, and nothing outlives it to notice a termination.
2 checks, 0 pass, 2 fail.
