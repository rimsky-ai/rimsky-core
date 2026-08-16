---
audit: hard-dep-field-no-special-case
artifact: decision:hard-dep-field-no-special-case
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:16:08Z
---

# No code anywhere names an attribute-field hard-dep flag

Supported, and the claim is negative so the check is an exhaustive absence search rather than a behavioural exercise. Searching the whole tree for the flag's property name across source, protocol declarations, and configuration turns up exactly two hits, both of them test filenames in an unrelated wallclock-lint baseline; no source file reads, writes, or mentions the property. The edge builder derives its upstream set purely from the subscription entries' refresh flag and never inspects attribute-schema properties, and the cascade walker consumes only that derived map. The attribute-schema validator carries no rejector naming the property — searching for any migration-redirect error identifier of that shape returns nothing — so an author who writes the retired key gets whatever the ordinary JSON Schema handling of an unrecognised keyword gives, which is exactly the deferral the decision states. No migration-redirect error exists to generate.
