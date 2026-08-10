---
audit: uniform-attributes-delta-subscription
artifact: story:uniform-attributes-delta-subscription
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# One predicate on the verdict's attributes fires across both terminal kinds

Supported. Against an all-in-one deployment wired to a third-party executor that
writes the same verdict attribute whether it succeeds or errors, 4 producer
nodes covered both terminal kinds against both values of that attribute, and 4
watcher nodes carried one identical subscription — a wildcard over the terminal
kinds with a predicate on the verdict's attribute value. The subscription fired
once over the succeeding producer and once over the erroring producer, with no
per-kind entry written for either, and stayed silent over the two producers
whose verdict carried the other value. All 4 producers ran, so the two silent
watchers were silent by predicate and not by a missing signal, and the erroring
producer's attribute survived its error to reach its watcher.
