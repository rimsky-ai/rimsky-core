---
audit: services-source
artifact: decision:services-source
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:44:00Z
---

# The compose manifest's mirrored service blocks and the sibling unified config as secondary source

Supported. The manifest schema carries an executors block and a claim-producers block whose entries mirror the unified config's field for field — transport, endpoint, TLS mode, protocol membership, and observability endpoint, with the permitted write-semantics list present and required on claim-producer entries. The rules match too: both surfaces require the write-semantics list, accept the same four values, accept the same two TLS modes, and share one protocol-name set, which the manifest validator imports from the config package directly. The manifest is the primary source — its blocks are folded into the synthetic config first — and the sibling unified config is picked up automatically by probing the manifest's own directory for it, returning an empty result rather than an error when absent. Only publishers and named locks come from that sibling: neither block exists in the manifest schema, and the sibling loader extracts those two and nothing else. Neither rejected alternative is present — the manifest alone suffices, and no new services namespace was invented.
