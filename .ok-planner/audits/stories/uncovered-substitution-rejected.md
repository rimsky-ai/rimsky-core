---
audit: uncovered-substitution-rejected
artifact: story:uncovered-substitution-rejected
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:06:40Z
---

# An uncovered substitution ref is refused at registration, naming the ref and the entry that would cover it

Supported. Driven through the public surface against a container of the released
all-in-one image, on two templates that each read something they never subscribe
to — one an upstream node's attribute, one a typed message's field. Eight checks,
none failing. Both registrations were refused and no template id came back; each
refusal carried a structured finding naming the offending ref, the receiver node,
the schema property the ref sits in, and the exact subscription entry that would
cover it. The remedy was proved rather than described: adding precisely the shown
entry to the same template made it register. The same finding is available before
any registration is attempted, from the validate route, so the author fixes the
wiring at authoring time rather than meeting an orphan read at runtime.
