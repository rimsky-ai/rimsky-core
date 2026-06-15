---
decision: cascade-flags-required-no-defaults
status: as-is
---

# Both cascade-shape flags are required on every subscription entry

## Choice

Both `wake_on_change` and `force_upstream_refresh` are required fields on every subscription entry; registration rejects entries missing either. No defaults are applied.

## Rationale

Call-site clarity — reading any subscription entry tells the reader the full cascade behavior with no document-memorization required. Forces template authors (human or LLM agent) to think about the cascade behavior at every edge.
