---
audit: uncovered-substitution-error-shape
artifact: decision:uncovered-substitution-error-shape
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:38:09Z
---

# The uncovered-substitution rejection is a structured, kind-discriminated entry with a drop-in suggestion

Supported. The coverage check emits a map-shaped entry carrying its own kind discriminator naming it an uncovered-substitution rejection, the receiver node type, the offending ref literal, and the attribute property path where the ref appears; both the registration route and the validate route append these entries to the same validation-errors array the prose entries land in, and registration answers with a rejection status. The suggestion is a flat three-key object — sender, implied signal type, and an upstream-refresh flag defaulted to false — that drops straight into a subscribes list, and the explanatory sentence about that flag is a sibling string field on the entry, never nested inside the suggestion. Both ref families that can be uncovered (node-attribute reads and message-body reads) produce the same shape. Unit tests assert the discriminator, the ref and property fields, the exact three-key suggestion with its conservative flag default, the absence of any note field inside the suggestion, and the note's presence as a top-level sibling.
