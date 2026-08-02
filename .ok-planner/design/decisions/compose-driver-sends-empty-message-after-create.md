---
decision: compose-driver-sends-empty-message-after-create
---

# Compose driver sends an empty message after each instance create

## Choice

After creating an instance whose template declares at least one structural root, the compose driver sends an empty wake message with a deterministic idempotency key derived from the instance key. An instance whose template has no structural root receives no wake message, since there is nothing for the empty message to wake.

## Rationale

The compose driver's user-facing contract (`story:one-shot-to-terminal`) requires created instances to run to terminal, and instance-create alone leaves an instance idle. The driver absorbs the wake step internally so operators do not have to add wake messages to their compose manifests, and the deterministic key makes a retried create-plus-wake idempotent.

## Alternatives

- Operator-authored wake messages in the compose manifest — rejected: breaks the compose UX; the wake is part of the driver's contract, not operator content.
- A convenience flag at instance-create that bundles the wake — rejected per `story:instance-create-is-idle`: instance-create is strictly two-step.
