---
decision: launch-config-injection
---

# Synthetic config file injected through standard discovery

## Choice

The compose verb writes one synthetic YAML file to the run directory — a unified config file matching the `concept:rimsky-yml` shape, the supervisor's tuning under its per-role section — and points the role runners at it through the standard config-discovery surface before the runners start. The synthetic file persists alongside the SQL state and the blob root as part of the run artifact (see `decision:artifact-layout`).

## Rationale

The role runners load YAML from disk; there is no programmatic config seam. The standard config-load surface loads the synthetic file; it costs a write per run at startup and turns the config into an audit artifact for free — operators reading a post-mortem run see exactly what config the run used.

## Alternatives

- A programmatic config-injection seam on the role-runner surface — rejected as a larger refactor than this verb's scope warrants.
- A second synthetic file carrying the supervisor's tuning — rejected: it breaks the single-file commitment of `concept:rimsky-yml`, and a file the image and the test harness consume is no longer this verb's to own.
