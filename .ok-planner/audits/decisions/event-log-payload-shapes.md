---
audit: event-log-payload-shapes
artifact: decision:event-log-payload-shapes
text: noncompliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:44:49Z
---

# The event proto splits typed operational payloads from free-form raw payloads, and rimsky's own path is untyped

Unsupported. The split itself holds: the event message carries a payload oneof of twenty-eight typed cases, every one an operational kind, beside a separate free-form struct field for everything else, and two tests enforce it — the named one in the generated-bindings package, plus a second in the events package that additionally requires every oneof case to correspond to a declared operational-kind wire form. What fails is the Choice's closing claim about rimsky's internal write and read path. All three payload-carrying fields it names are not free-form maps: each is an opaque payload value type whose only from-scratch constructor takes a generated proto message and marshals it through the canonical JSON mapping, with a separate decode-only constructor for reading rows back, so a map literal does not compile at any emit site. Rimsky therefore does construct generated payload messages throughout, for signal-class and operational events alike; what it does not construct is the event wrapper message itself, which is the narrower true half of the sentence. Checked all twenty-eight oneof cases, the raw payload field, both enforcing tests, and the three named field declarations.

## Compliance

Body names a source file path ("lib/protocols/proto/v1/gen/event_payload_split_test.go"), which the self-containment rule forbids in any artifact body; the compliant text names the enforcement without the path, e.g. "Mechanically enforced by a test over the generated event descriptor that rejects any oneof case that looks signal-class."
Body cites three code symbols in dotted path-and-symbol form for the internal payload fields; the compliant text states the property instead, e.g. "The internal write and read path carries payloads as an opaque value constructed from a declared message, independent of the proto event wrapper."
