---
audit: force-upstream-refresh-via-receiver-keyed-map
artifact: decision:force-upstream-refresh-via-receiver-keyed-map
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:15:58Z
---

# Force-upstream-refresh is a separate receiver-keyed edge map consumed by the cascade walker

Supported. The edge builder produces a map keyed by receiver node-type whose values are upstream node-types, collected from exactly those subscription entries whose refresh flag is true, and it is a structure distinct from the per-edge subscription map. Registration builds it: the template validator calls the builder and surfaces its error, so a bad graph is rejected at deploy rather than at dispatch, and the runtime resolves the same map through a per-template-hash cache. The cascade walker consumes it when a receiver is marked stale — for each named upstream with no in-flight run it creates a pending run in the sender's frame, evaluates that run's gate, and inserts a wait-set row binding the receiver to it, which is what makes the upstream re-run and the receiver wait for it inside the same frame. All five clauses were checked against the builder itself: cycle detection runs inside the build call and reports single and multiple cycles, fan-out targets are collected and rejected with an error naming each offending pair, self-references are dropped, and a per-receiver seen-set de-duplicates repeat senders. Four of the five carry tests — cycle detection in three forms, fan-out rejection, self-reference exclusion, and the map's shape — while the sender de-duplication arm is implemented but exercised by no test and by no template in the tree, since nothing anywhere declares two refresh entries from one receiver to the same sender. The walker-side consumption is exercised by two upstream-pull scenarios and by the two-upstream rendezvous scenario.
