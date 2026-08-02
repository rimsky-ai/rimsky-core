---
decision: allowlist-defaults-open
---

# Bundled-service allowlists default open when operator config is absent

## Choice

If the operator env vars carrying a bundled service's policy allowlists are unset, the handler constructs with all policy allowlists open — every reference the template makes is accepted. A set-but-empty allowlist is an explicit closed boundary.

## Rationale

Zero-config local dev works out of the box; operators wanting policy set the env explicitly.

## Alternatives

- Default closed — rejected: breaks zero-config local use, where no operator config exists at all.
