---
audit: run-token-swept
artifact: decision:run-token-swept
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:29:03Z
---

# The terminal return leg carries no per-call token; the ack id only correlates

Supported. The async-callback route runs two checks and neither is a token: a peer check that under mutual-TLS requires a client certificate chaining to the deployment CA and is a no-op otherwise, and a principal binding that compares the calling certificate's principal against the principal recorded on the dispatch. The acknowledgement id is used only to pop the dispatch from the in-memory registry or look it up on the persisted row; an unknown id is a not-found, not an authorization failure. The two mid-dispatch channels on the same server — keepalive and attribute writeback — do carry a per-dispatch token and check it before doing anything, which is the separation the decision draws. On the services side, none of the bundled services sends a per-call credential on its publish-back calls to the control API; the only bearer headers in that module belong to an executor's own outbound integrations. Tests cover the accepted valid client certificate, the missing certificate, an impostor CA, both principal-binding outcomes, and the unknown-acknowledgement not-found.
