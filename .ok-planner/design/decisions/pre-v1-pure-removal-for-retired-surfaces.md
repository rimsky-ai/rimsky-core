---
decision: pre-v1-pure-removal-for-retired-surfaces
---

# Pre-v1 pure removal for retired surfaces

## Choice

Retired DSL surfaces are removed from the code entirely. Templates and requests using them fail through normal validator paths (unknown field, unknown signal type, unknown message type). No detection rule, no migration error string, no parser case that names the old shape.

## Rationale

No remnants of retired features in the code. Pre-v1, the project's bias is clean removal; backwards-compatibility shims and migration helpers are not warranted. The normal validator's "unknown field" error is the rejection.

## Alternatives

- A recognition rule per retired shape with a targeted migration error — rejected: keeps a named remnant of every retired feature alive in the parser, and the list only grows.
