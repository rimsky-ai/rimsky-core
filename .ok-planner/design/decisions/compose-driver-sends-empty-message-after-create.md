---
decision: compose-driver-sends-empty-message-after-create
status: as-is
---

# Compose driver sends an empty message after each instance create

## Choice

The compose driver sends an empty wake message after creating an instance, via the same HTTP client path it already uses for instance-create, with a deterministic idempotency key derived from the instance key — but only for instances whose template declares at least one structural root; an instance whose template has no structural root receives no wake message, since there is nothing for the empty message to wake. The wake send precedes the wait-for-terminal loop.

## Rationale

The compose driver's user-facing contract (`story:one-shot-to-terminal`) is unchanged. The implementation absorbs the now-explicit wake step internally so operators do not have to add wake messages to their compose manifests.

## Alternatives considered

Require operator-authored wake messages in the manifest — breaks the compose UX; add a convenience flag at instance-create that bundles the wake — rejected per the spec's instance-create-is-idle decision that instance-create is strictly two-step.
