---
audit: parallel-inproc-claim-producer-registry
artifact: decision:parallel-inproc-claim-producer-registry
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:28:39Z
---

# The in-proc claim-producer registry mirrors the executor one and enforces the same envelope

Supported. The runtime layer carries an in-process claim-producer registry beside the in-process executor registry, in the same shape. A registration binds a name to a handler, the producer's capabilities as construction data, and one optional client per mix-in, and it rejects an inconsistent advertisement in all four directions — validation or data-processing advertised without a client, or a client supplied without the advertisement — each direction covered by a table case in the registry's own test. The client the registry hands out is compile-time asserted against the same claim-producer interface the gRPC peer client satisfies, and the mix-in views satisfy the same two registry interfaces the validation pipeline and the data-processing dispatch take as parameters, so those paths cannot tell the modes apart. Envelope enforcement is literally shared rather than reimplemented: both the in-process client and the remote client call the same three helpers — the write-semantics envelope check on Open and the two capability gates on split-scope and scopes-conflict — and those are the only three call sites of each helper in the tree. The in-process client's tests cover an out-of-envelope Open, an unknown realized value, and both gates. Bundled producers registered at startup reach claim acquisition through the shared producer registry without shadowing an operator-configured entry of the same name.
