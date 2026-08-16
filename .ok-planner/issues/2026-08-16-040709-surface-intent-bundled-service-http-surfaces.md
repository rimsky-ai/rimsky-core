---
issue: surface-intent-bundled-service-http-surfaces
kind: audit
category: unclear
artifacts:
  - concept:service
  - concept:observability
status: verified
opened: 2026-08-16T04:07:09Z
---

# The surface intent does not say whether the bundled services' own HTTP listeners are public

The surface intent (the owner's statement of which classes of element are public) names three public HTTP classes: control-API routes under the version prefix, the supervisor's callback routes, and the sensor-webhook ingress. The bundled services also serve HTTP: the claim producers' protocol bridge (open, commit, abandon, release, split, conflict), the lifecycle bridge, the observability bridge, admin listeners, and the claude-agent executor's internal MCP server — nine route families an operator can reach and configuration keys point at. The intent's general rule ("what reaches a consumer is public") and its named-class rule pull in different directions, so this run's extractor defaulted them internal. The ruling amends the intent one way or the other.

The stake is a compatibility promise: public means the bridge paths and bodies cannot change without notice; internal means they can.

## Options

- Every route a bundled service serves is public; cost: the bridges become a frozen contract alongside the gRPC protocols.
- Split by class — protocol, lifecycle and observability bridges public (a third party implements or dials them), admin listeners and the internal MCP server internal; cost: two rules to keep straight.
- Only the three named classes are public; cost: a consumer dialing a bridge has no promise.

The ruling decides which bundled-service listeners the intent calls public.

## Ruling

> Recommended ruling (/verify-issues): Amend the intent to make the protocol, lifecycle and observability bridges public — they mirror gRPC protocols the intent already calls public, and a service author is told to expect them — and to name admin listeners and the executor's internal MCP server as internal.
>
> Rationale: the bridges are the same contract as the RPCs in another encoding, and the run's own trap findings show consumers already reach for them; the admin and internal listeners serve the service itself. Flip case: if the HTTP bridges are slated for retirement in favour of gRPC-only peers, calling them public now freezes what is meant to go — then the third option is right.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
