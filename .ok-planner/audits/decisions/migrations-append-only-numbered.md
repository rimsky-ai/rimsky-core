---
audit: migrations-append-only-numbered
artifact: decision:migrations-append-only-numbered
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:52:00Z
---

# Numerically ordered, append-only, per-backend migration sets

Unsupported: the tree practises the rejected alternative rather than the chosen one, and its own migration headers say so. The ordering and per-backend halves hold — each of the two backends carries its own embedded set applied in filename order by one shared runner that records each applied filename and skips it thereafter, the two sets differ by one dialect-specific file, and pre-v1 rethinks are indeed expressed as new drop-and-recreate files rather than as edits, with several such retirement migrations in the current sequence. The append-only half does not hold. The current client-server set is 28 files whose ordinals run 001, then 014 through 041 with one gap, because ordinals 002 through 013 were collapsed into the baseline file and two later ordinals were then reassigned to different migrations from the ones that first held them. Two files in the tree carry headers recording that reassignment and stating that the reuse is safe only for a database dropped and recreated against the current baseline, and never for one still carrying the old row — which is exactly the applied-prefix breakage the decision gives as its reason for rejecting rebased migrations, and it contradicts the rationale's claim that append-only keeps every database's applied prefix valid forever. Nothing mechanically holds the discipline either: the runner keys idempotency on filename alone, with no checksum and no detection of a removed or rewritten file, so the constraint rests on the headers' stated post-v1 promise rather than on anything the code can enforce.
