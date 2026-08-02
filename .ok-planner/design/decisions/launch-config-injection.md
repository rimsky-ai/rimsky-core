---
decision: launch-config-injection
---

# Synthetic config files injected through standard discovery

## Choice

The compose verb writes two synthetic YAML files to the run directory — a unified config file matching the `concept:rimsky-yml` shape and a separate supervisor-tuning file — and points the role runners at them through the standard config-discovery surfaces before the runners start. The synthetic files persist alongside the SQL state and the blob root as part of the run artifact (see `decision:artifact-layout`).

## Rationale

The role runners load YAML from disk; there is no programmatic config seam. The synthetic files are loaded via the standard config-load surface, cost a write per run at startup, and turn the config into an audit artifact for free — operators reading a post-mortem run see exactly what config the run used.

## Alternatives

Adding a programmatic config-injection seam to the role-runner surface (rejected as a larger refactor than this verb's scope warrants).
