---
audit: uniform-attributes-delta-subscription
artifact: story:uniform-attributes-delta-subscription
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:08Z
---

# One predicate on the verdict's attributes fires the same way on success and on error

Supported: a run drove an all-in-one deployment against a third-party executor
registered over the HTTP-bridge transport, which wrote the same verdict
attribute alongside a success outcome and alongside an error outcome. Four
producers covered both terminal kinds crossed with both verdict values, and four
watchers carried the identical subscription — one wildcard terminal path with
one predicate on the attribute value, no per-kind entry anywhere. The two
producers whose verdict carried the watched value fired their watchers once each,
one on a success terminal and one on an error terminal; the two carrying the
other value fired nothing, though all four producers ran, so the silence was the
predicate's and not a missing signal. The erroring producer's attribute survived
its error and reached its watcher. Seven checks, none failing.
