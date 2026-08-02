---
audit: subscriber-lineage-receiver
artifact: story:subscriber-lineage-receiver
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:57Z
---

# Bundled OpenLineage subscriber delivers rimsky lineage to an external receiver

Supported. The repository ships a standalone bundled subscriber binary (`lib/services/subscribers/openlineage`, imaged as `rimsky-subscriber-openlineage` via the `service-images` Makefile target) that polls the durable `rimsky_lineage` projection on a cursor, translates `leaf_run` and `claim_terminal` records into OpenLineage `RunEvent` JSON via `MakeLeafRunEvent`/`MakeClaimTerminalEvent`, and POSTs them to an operator-configured `RIMSKY_OPENLINEAGE_BACKEND_URL`, with permanent-failure dead-lettering and cursor persistence for restart safety — requiring no custom subscriber code from the operator. Config loading, event translation, dead-letter handling, and emitter behavior are covered by the package's own unit test files (`config_test.go`, `emitter_test.go`, `subscriber_test.go`, `subscriber_lite_test.go`, plus a schema-conformance test), 13 `Test*` functions checked across `config_test.go` and `subscriber_test.go` alone.
