---
audit: validator-learns-producer-classes
artifact: decision:validator-learns-producer-classes
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:40:04Z
---

# The range check unions four vocabularies and downgrades a miss to a warning

Supported. One validator function range-checks each error-type policy key, and it consults exactly the four vocabularies the decision names: the executor's declared classes, the acquire-prefixed synthetic family, a shared list of runtime-synthesized classes covering resolution, validation, schema-unavailable, attributes-schema, unresolved-executor, sync-timeout, protocol-violation and abandoned, and the declared classes of every producer named in the node's required-claim-producers set. Declared entries match exactly or by a trailing wildcard prefix. A key attributable to none of the four appends a warning naming the class, the executor, and both declared sets — never an error — and warnings ride the response on both the registration and validate surfaces. The policy action vocabulary remains a hard rejection, which is a separate axis from the class key. Eleven tests in the validator's error-type file cover each vocabulary branch, the wildcard match, the producer class reachable and unreachable from the node, and the case where no vocabulary is known at all, which the code treats as no basis to warn.
