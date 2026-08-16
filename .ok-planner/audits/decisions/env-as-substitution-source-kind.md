---
audit: env-as-substitution-source-kind
artifact: decision:env-as-substitution-source-kind
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:10:36Z
---

# Host-environment variables are the sixth source kind in the one substitution grammar

Supported on every clause. The resolver's kind switch carries exactly six kinds — claim, params, nodes, messages, child, env — enumerated from the switch itself rather than from the decision, and the registration-time grammar recogniser carries the same six and rejects anything outside them with an error naming all six, so a bare shell-style reference fails at template paste time; that rejection has its own registration test. An env directive takes a single conventional variable name, validated by the same name pattern at registration and at resolve, and reads the process environment of the supervisor, which is where substitution runs, with the value landing in the dispatch attribute bag before the executor is called. Unset yields the grammar's missing-source error and empty-but-set yields the empty string, both tested. The kind induces no subscription edge: the reference extractor that feeds both cascade-edge derivation and the coverage check recognises only the node and message kinds, and a test asserts that an env directive alone registers no coupling edge and surfaces as neither a node nor a message reference. The lenient marker and the single-literal fallback are parsed before the kind switch and so apply to env identically to every other kind, each with its own test, alongside whole-directive, embedded, malformed-name, and process-environment-fallback cases.
