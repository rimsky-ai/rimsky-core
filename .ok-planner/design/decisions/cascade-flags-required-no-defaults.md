---
decision: cascade-flags-required-no-defaults
---

# Cascade-shape flag is required on every subscription entry

## Choice

`force_upstream_refresh` is a required field on every subscription entry; registration rejects entries missing it. No default is applied.

## Rationale

Call-site clarity — reading any subscription entry tells the reader the full cascade behavior with no document-memorization required. Forces template authors (human or LLM agent) to think about the cascade behavior at every edge.

## Alternatives

- Default the flag to the common cascade shape — rejected: the edge's behavior becomes invisible at the call site and requires memorizing the default.
