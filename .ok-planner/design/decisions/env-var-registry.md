---
decision: env-var-registry
status: as-is
---

# Enforced registry of operator env vars

## Choice

Every `RIMSKY_*` environment variable read by live code appears in a
generated registry table, backed by a fitness test that fails when a
read site references a variable missing from the registry. Endpoint
variables name their target service: the control API's endpoint is
`RIMSKY_CONTROL_API_URL` everywhere — the CLI included — and the
host-agent's proxy endpoint is `RIMSKY_HOST_AGENT_PROXY_URL`.

## Rationale

Live code reads roughly 58 `RIMSKY_*` variables and only 15 were
documented anywhere — the gap grows because nothing fails when a new
variable ships unlisted. Three look-alike endpoint names pointing at
two different services made misconfiguration silent (set the wrong
one and things quietly don't connect); naming the target service
removes the collision at the root, and the pre-v1 break-freely stance
makes the renames cheap now in a way they never will be again.

## Alternatives

- A hand-written table with no test — rejected: rots exactly the way
  the documentation did.
- Keep the generic names and document harder — rejected:
  documentation doesn't stop a quiet non-connection when the wrong
  look-alike is set.
- No registry — rejected: leaves discoverability at "grep the
  source" for three quarters of the operator surface.

## Proof

The registry fitness test: it fails on any `RIMSKY_*` read (including
reads through the env-helper call sites) missing from the generated
table. Falsifier: add an unregistered env-var read — the test turns
red.
