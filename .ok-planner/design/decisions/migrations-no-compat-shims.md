---
decision: migrations-no-compat-shims
---

# Pre-v1 migration freedom

## Choice

When a schema needs rethinking pre-v1, a new migration drops and recreates rather than threading a compatibility shim.

## Rationale

Pre-v1 there is no production data to preserve (see `decision:pre-v1-break-freely`); a compat shim is permanent complexity purchased to protect data that does not exist.

## Alternatives

- Thread data-preserving compat shims through every schema change — rejected: carries dead compatibility weight into v1 for the sake of disposable dev databases.
