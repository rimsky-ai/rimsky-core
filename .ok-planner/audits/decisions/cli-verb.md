---
audit: cli-verb
artifact: decision:cli-verb
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# The manifest one-shot's placement as a compose sub-verb read against the CLI's dispatch tables

Supported. The compose dispatcher routes five sub-verbs — the four lifecycle verbs the decision names plus the one-shot run — and the one-shot is registered there, not as a member of the binary's top-level verb table, which the decision's rejected alternative would have required. The distinction the choice rests on holds too: each of the four lifecycle verbs resolves an endpoint and fails without a reachable control API, while the one-shot boots its own role stack against a loopback endpoint and needs no running rimsky. Both consume the same manifest type and the same plan-and-apply path.
