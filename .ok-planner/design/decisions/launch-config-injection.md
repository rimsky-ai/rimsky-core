---
decision: launch-config-injection
status: adopted
---

# launch-config-injection

## Choice

The verb writes two synthetic YAML files to the run directory and points the role runners at them via the standard config-discovery surfaces — a unified config file matching the `concept:rimsky-yml` shape (persistence driver, blob backend, executors block, claim-producers block) and a separate supervisor-tuning file (concurrency, heartbeat, callback host/port, advertise host). Config-path environment variables are set on the in-process environment before the runners start. The synthetic files persist alongside the SQL state and the blob root as part of the run artifact (see `decision:artifact-layout`).

## Rationale

The role runners load YAML from disk; there is no programmatic config seam. The synthetic files are loaded via the standard config-load surface, cost a write per run at startup, and turn the config into an audit artifact for free — operators reading a post-mortem run see exactly what config the run used.

## Alternatives

Adding a programmatic config-injection seam to the role-runner surface (rejected as a larger refactor than this verb's scope warrants).
