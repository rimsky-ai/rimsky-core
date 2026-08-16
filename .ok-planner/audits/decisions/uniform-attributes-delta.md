---
audit: uniform-attributes-delta
artifact: decision:uniform-attributes-delta
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:39:39Z
---

# Both run-terminating verdicts carry and commit the delta; park carries none

Supported. On the wire, the success and error outcome messages each declare an attributes-delta field, and the park message declares the same field number and name as reserved — the field was removed rather than ignored. In the runtime the delta is read from exactly two places in each of the two arrival paths, synchronous outcome and async callback, and in both it comes only from the success and error branches; park contributes nothing. Persistence is uniform across the two verdicts: each path merges the delta over the resolved bag, runs the same commit-writeback validation, and upserts the per-run attribute row inside the verdict's transaction, with an atomic-commit test covering that. Exposure is uniform too: the success and error signal builders each embed the delta on the emitted payload under the same key, defaulting to an empty map, so a subscriber predicate written once matches on either kind — scenario coverage predicates on the delta for both a success loop and an error, and a signal-emission test asserts the error signal's payload carries the delta as a map. The mid-dispatch writeback callback merges into the same row on its own route without emitting a signal, which is the coexistence the decision describes.
