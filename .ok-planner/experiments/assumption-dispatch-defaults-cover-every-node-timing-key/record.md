---
experiment: assumption-dispatch-defaults-cover-every-node-timing-key
commit: PENDING
---

# Deployment-wide defaults for the per-node timing keys

## What it ran against

Eight container boots, one per per-node timing or retry key a template may
carry: `sync_rpc_deadline`, `max_quiet_period`, `max_runtime`, `max_retries`,
and the four `retry_backoff` subkeys. Each boot mounts a `rimsky.yml` naming
that key under `dispatch_defaults` into a `rimsky-all-in-one` from this tree's
image set on a free port, and the run asks whether the deployment reaches a
healthy control API. The config loader is strict, so an unsupported key is not
ignored — it stops the deployment before it starts, which is the observation.

The container is named for the full assumption slug after an earlier run was
interrupted by another worker's cleanup of a shorter prefix; the result here
is from the clean re-run.

## What was observed

3 of the 8 keys have a `dispatch_defaults` form. `sync_rpc_deadline`,
`max_quiet_period`, and `max_runtime` each came up healthy. `max_retries` and
all four `retry_backoff` subkeys did not come up at all: `rimsky-migrate`
exits with `field max_retries not found in type config.yamlDispatchDefaults`
(and the same for `retry_backoff`), and the entrypoint reports `migrate
failed`. Setting the retry policy once for a deployment is not merely
unsupported — attempting it in the obvious place takes the deployment down.
1 check, 0 pass, 1 fail.
