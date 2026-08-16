---
audit: validation-errors-additive-not-uniform
artifact: decision:validation-errors-additive-not-uniform
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:39:39Z
---

# One array, two entry shapes, told apart by the kind discriminator

Supported. The validation result carries two collections — simple path-and-message errors and structured entries — and both control-API surfaces that report validation failure, template registration and the standalone validate verb, concatenate them into a single response array under one key, so a consumer sees one list of mixed shapes. The structured shape is used at the two substitution-coverage rejection sites and nowhere else; each entry opens with the kind discriminator and carries the receiver node type, the offending reference, the attribute property, and a copy-pasteable suggested subscribes entry plus a note. The missing-flag rejection the decision names as the counter-case is emitted as a plain path-and-message entry pointing at the entry's upstream-refresh field. A scenario test reads the response array and asserts the discriminator value on the structured entry, which is the mechanism a consumer uses to tell the shapes apart.
