---
issue: http-json-bridges-not-uniformly-protojson
kind: audit
category: decision-drift
artifacts:
  - decision:protojson-gateway
status: verified
opened: 2026-08-02T09:58:25Z
---

# Two of the three HTTP bridges hand-roll their JSON instead of using protojson

Rimsky's HTTP transports are supposed to be mechanical projections of the proto contract: bodies marshal through protojson (the canonical proto↔JSON codec), and the decision rejects hand-written JSON structs as "a parallel body vocabulary that drifts from the proto contract" (`decision:protojson-gateway`). Of the three HTTP bridges, only the generic executor Execute bridge round-trips both directions through protojson. The claim-producer/lifecycle-subscriber bridge — which carries this decision's own citation annotation and is what external implementers hit through the HTTP conformance transport — decodes every request through hand-written body structs, duplicated again on the client side, and only marshals responses with protojson (`code:lib/protocols/serverkit/bridge.go`). The claude-agent executor's standalone execute bridge uses plain `encoding/json` with a bespoke body struct in both directions (`code:lib/services/executors/claude-agent/httpbridge.go`).

Both are exactly the parallel-vocabulary shape the decision rejects, on wire surfaces external authors code against: the hand-written structs can drift from the proto silently, and the conformance kit currently certifies against the drifted shape rather than the contract. The ruling decides whether the two bridges converge on protojson or one of them earns a documented carve-out.

## Options

- Convert both bridges' request decoding (and the conformance client's counterpart) to protojson. Cost: a wire-compatibility break wherever protojson's field-name mapping differs from the hand-written keys — external claim-producer authors following today's observed shape must adjust (legal pre-v1, but it's their wire).
- Carve out one or both bridges in the decision as intentionally stable hand-written schemas. Cost: permanently maintains the parallel vocabulary and the drift risk the decision exists to prevent.

## Ruling

> Recommended ruling (/verify-issues): convert both bridges to protojson end to end, updating the conformance kit's client in the same change so the certified wire shape and the served wire shape move together.
>
> Rationale: the whole value of the decision is that the proto file is the single wire authority, and the surface where that matters most — the one external implementers certify against — is the one currently drifting; pre-v1 is the designated moment for exactly this class of break, and the carve-out option institutionalizes the drift instead of ending it. Flip case: if a survey of the actual field-name deltas shows the hand-written keys already match protojson's output byte for byte (possible — the structs may have been written from protojson output), the conversion is pure refactor with no wire break, and there is no reason left to hesitate; only if real external consumers demonstrably depend on non-protojson keys should the carve-out be considered.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
