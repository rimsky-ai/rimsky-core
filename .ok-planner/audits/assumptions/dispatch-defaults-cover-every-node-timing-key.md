---
assumption: dispatch-defaults-cover-every-node-timing-key
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# every per-node timing or retry key has a deployment-wide default, so `max_retries` and `retry_backoff` can be set once under `dispatch_defaults` rather than on every node.

As operator setting deployment-wide policy, I would take it that every per-node timing or retry key has a deployment-wide default, so `max_retries` and `retry_backoff` can be set once under `dispatch_defaults` rather than on every node.

## Source

sibling-symmetry — `dispatch_defaults.{sync_rpc_deadline,max_quiet_period,max_runtime}` matching three of the six per-node timing keys

## What a run would observe

set `dispatch_defaults.max_retries` in `rimsky.yml` and see whether the strict loader accepts the key.

## Measured

`.ok-planner/experiments/assumption-dispatch-defaults-cover-every-node-timing-key`
— built for this run — booted one container per key, mounting a `rimsky.yml`
that names that key under `dispatch_defaults`, over the eight per-node timing
and retry keys a template may carry.

Three of the eight have a deployment-wide form: `sync_rpc_deadline`,
`max_quiet_period`, and `max_runtime` each brought up a healthy deployment.
The five the prior is actually about did not. `max_retries` and all four
`retry_backoff` subkeys stop the deployment before it starts — `rimsky-migrate`
exits with `field max_retries not found in type config.yamlDispatchDefaults`
(likewise `retry_backoff`) and the entrypoint reports `migrate failed`.

So retry policy is per-node only, and the failure mode for assuming otherwise
is not a warning or an ignored key: the operator who adds
`dispatch_defaults.max_retries` to a working `rimsky.yml` has a deployment
that will not boot. 1 check, 0 pass, 1 fail.
