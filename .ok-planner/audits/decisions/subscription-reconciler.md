---
audit: subscription-reconciler
artifact: decision:subscription-reconciler
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:33:05Z
---

# The reconciler retries forever on a fixed tick, and failed stays non-retryable

Supported. A reconciliation worker runs on a fixed-interval ticker with a five-second default, started by the control API at boot and covered by a test that asserts the start. Each tick runs the same resync pass the process runs at startup: it lists the active and mounting rows, lists each publisher's live subscriptions, promotes a mounting row whose id the publisher already reports, and issues Subscribe only for rows the live set is missing — so an already-live subscription is never re-issued, and a row the publisher has lost is re-driven. Nothing caps attempts on a row: a Subscribe failure logs and moves on, leaving the row in mounting for the next tick, and a test drives a subscription past the per-attempt RPC retry budget and asserts it still reaches active. The failed state has exactly two writers in production code, both at row insert, both non-retryable causes; no exhausted retry path writes it. A recovery pass flips unknown-publisher failures back to mounting once that publisher registers, and leaves failures with any other reason alone.
