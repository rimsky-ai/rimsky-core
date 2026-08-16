---
audit: child-execution-naming
artifact: decision:child-execution-naming
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:09:54Z
---

# The child-execution primitives are one dispatch and two settles, plainly named

Supported. The runtime carries exactly one dispatch-children primitive, called from exactly two sites — one per mechanism — and two separate settle primitives, one per mechanism; no unified settle-children function exists anywhere in the tree, which is the alternative the decision rejects. The names avoid the overload the decision names: the family is named for children rather than for delegation, and each settle primitive is named for the single mechanism it serves. The carry and aggregate vocabulary the decision uses to refer to the two settles is present in the code as the delegation settle's exit-carry event kind and as the fan-out settle's aggregation policy and aggregate-outcome computation, while the function identifiers themselves are the longer descriptive forms; a reader looking for the design terms finds them, and a reader looking for one settle covering both finds nothing. This is a naming decision, so the check is a reading of the symbol set rather than a runtime exercise.
