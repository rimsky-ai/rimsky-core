---
decision: env-var-registry
status: as-is
---

# Enforced registry of operator env vars

## Choice

Every `RIMSKY_*` environment variable read by live code appears in a
generated registry table, backed by a fitness test that fails when a
read site references a variable missing from the registry. Endpoint
variables name their target service: the control API's endpoint
variable and the host-agent proxy's endpoint variable each carry the
service they point at, everywhere — the CLI included.

## Rationale

Live code reads several dozen `RIMSKY_*` variables, and without a
fitness test nothing fails when a new one ships unlisted — an
undocumented majority is the steady state a hand-maintained list
decays to. Look-alike endpoint names pointing at different services
make misconfiguration silent (set the wrong one and things quietly
don't connect); naming the target service removes the collision at
the root.

## Alternatives

- A hand-written table with no test — rejected: rots exactly the way
  the documentation did.
- Keep the generic names and document harder — rejected:
  documentation doesn't stop a quiet non-connection when the wrong
  look-alike is set.
- No registry — rejected: leaves discoverability at "grep the
  source" for three quarters of the operator surface.
