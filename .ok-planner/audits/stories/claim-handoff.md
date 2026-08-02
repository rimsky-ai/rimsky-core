---
audit: claim-handoff
artifact: story:claim-handoff
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:18Z
---

# Co-holder nodes share an acquirer's claim and cascade Commit/Abandon atomically

Supported. `lib/graph/node`'s template validator and `lib/runtime/runner_acquire_holders.go` implement the `holds:` co-holdership directive that inserts co-holder claim-holder rows at each co-holder's own acquire-time; `{{claim.<alias>.address|payload...|claim_scope}}` substitution into a co-holder's attribute schema is exercised end-to-end. `test/scenarios/claim_handoff_e2e_test.go` runs five sub-tests covering per-field substitution (address, payload field, claim scope), the abandon path (co-holder failure abandons the acquirer's claim), a multi-co-holder all-success commit, and wire-payload byte-parity between the persisted `claim_handle.Address` and the substituted attribute — all against a real claim-producer stub over gRPC.
