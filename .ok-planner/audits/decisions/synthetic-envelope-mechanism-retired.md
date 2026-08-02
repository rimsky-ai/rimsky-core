---
audit: synthetic-envelope-mechanism-retired
artifact: decision:synthetic-envelope-mechanism-retired
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:41:29Z
---

# Receiver wake goes exclusively through the subscriber-side cascade walker; no synthetic-envelope chokepoint remains

Supported. The only wake path found is `cascadeSubscribersStaleInTx` (`lib/runtime/runner_terminal.go`) resolving receivers via `edges.Match(senderNodeType, sig.Type)` (the subscription-edge / inverse-edge lookup) into `resolveReceiverRunForCascade`/`ensureCascadePending` (`lib/runtime/cascade_walker.go`) — no wake-node-id or wait-set-pair field appears anywhere in the wire proto (`lib/protocols/proto/v1/*.proto`), no reserved-field/reserved-property guard for such names exists, and no frame-engine code reads a wake-node-id at promotion. Each of the rationale's three named callers is confirmed to bypass no such chokepoint: instance creation (`lib/control/controlapi/instances.go`, tagged `@decision: synthetic-envelope-mechanism-retired`) only creates node rows and message-receiver rows, posting no wake; the asset-materialize endpoint is confirmed retired per `decision:asset-materialize-endpoint-retired` (no matching route registered anywhere in `lib/control/controlapi`); node reset (`handleResetNode`) writes only an audit event, calling no wake function; and the scenario test harness (`test/support/scenario/harness.go`) drives every instance-creation and mid-run wake through `PostInstanceMessage`, i.e. a real message post, never a synthetic envelope.
