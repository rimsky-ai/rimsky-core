---
issue: surface-intent-bundled-service-http-surfaces
kind: audit
category: unclear
artifacts:
  - concept:service
  - concept:observability
status: promoted
sprint: 2026-08-21-intake-drain-and-concept-repair.md
opened: 2026-08-16T04:07:09Z
---

# The surface intent does not say whether the bundled services' HTTP listeners are public

The surface intent (the owner's statement of which classes of element are public) names three public HTTP classes: control-API routes under the version prefix, the supervisor's callback routes, and the sensor-webhook ingress. The bundled services serve HTTP too: the claim producers' protocol bridge (open, commit, abandon, release, split, conflict), the lifecycle bridge, the observability bridge, admin listeners, and the claude-agent executor's internal MCP server. An operator can reach those nine route families, and configuration keys point at them. The intent's general rule ("what reaches a consumer is public") and its named-class rule disagree, so this run's extractor defaulted the nine internal. The ruling amends the intent one way or the other.

A compatibility promise is at stake. Public means the bridge paths and bodies cannot change without notice. Internal means they can.

## Options

- Every route a bundled service serves is public; cost: the bridges become a frozen contract alongside the gRPC protocols.
- Split by class: the protocol, lifecycle and observability bridges are public because a third party implements or dials them, and the admin listeners and the internal MCP server are internal; cost: two rules to keep straight.
- Only the three named classes are public; cost: a consumer dialing a bridge has no promise.

The ruling decides which bundled-service listeners the intent calls public.

## Ruling

> Recommended ruling (/verify-issues): Amend the intent to call the protocol, lifecycle and observability bridges public, and to name the admin listeners and the executor's internal MCP server as internal. The three bridges mirror gRPC protocols the intent already calls public, and a service author is told to expect them.
>
> Rationale: the bridges carry the same contract as the RPCs in another encoding, and the run's own trap findings show consumers already reach for them. The admin and internal listeners serve the service itself. Flip case: if the owner plans to retire the HTTP bridges in favour of gRPC-only peers, calling them public now freezes what is meant to go, and the third option is right.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
