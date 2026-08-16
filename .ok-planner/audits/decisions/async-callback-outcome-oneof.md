---
audit: async-callback-outcome-oneof
artifact: decision:async-callback-outcome-oneof
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:03:38Z
---

# The async-callback body is an exactly-one outcome oneof reusing the Execute RPC's outcome messages

Supported. The protocol declaration for the callback body carries a three-arm oneof whose arms are the very same Success, Error, and Park messages the unary Execute RPC's outcome uses, so the inheritance the decision claims is literal message reuse rather than a parallel shape; the retired top-level events field is reserved by both number and name, and no event-stream field exists anywhere in the callback surface. Two independent parsers enforce exactly-one: the supervisor's callback parser counts the three outcome arms and rejects any body with a count other than one, and the conformance kit's callback receiver does the same while additionally rejecting unknown top-level keys. Both parsers were read in full, all three arms in each. The supervisor's parsed result is the same internal terminal-event value the synchronous dispatch path produces, so the two transports converge on one settlement and persistence path as the rationale claims. Unit coverage in the runtime package exercises all three arms plus the rejections that matter — the legacy string-discriminator body, a body carrying two outcomes, a park missing or malformed in its resume timestamp, and a park's attributes delta being discarded to match the reserved proto field.
