---
audit: template-subscriptions
artifact: story:template-subscriptions
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:08Z
---

# Subscriptions match on a signal type-path and a payload predicate, and fire only on both

Supported: a run through the CLI and control API of an all-in-one deployment
registered a template carrying all five subscription forms the story implies —
an exact type-path, a trailing-wildcard prefix, a non-matching type-path, a
predicate the arriving payload satisfies, and a predicate it fails — all
admitted at registration. The source node ran and emitted one terminal signal.
The exact form, the wildcard form, and the satisfied-predicate form each fired
exactly once; the node on a different type-path and the node whose predicate the
payload fails did not fire at all, so both the path match and the payload
condition gate the firing. Six checks, none failing.

## Compliance

Prescribes mechanism by naming the predicate's expression language, which is a decision-owned choice; the compliant text says the author declares a condition on the arriving event's contents.
Prescribes mechanism by naming the matching syntax as exact or trailing-wildcard prefix; the compliant text says the author targets either one kind of upstream event or a family of them.
