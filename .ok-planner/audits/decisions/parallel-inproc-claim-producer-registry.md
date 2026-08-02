---
audit: parallel-inproc-claim-producer-registry
artifact: decision:parallel-inproc-claim-producer-registry
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:34Z
---

# The claim-producer in-process registry parallels the executor one, with full envelope enforcement

Supported. `lib/runtime/claimproducer/inproc_registry.go` implements `InProcessRegistry.Register`, binding a name to a `Registration` of `{Handler, Capabilities, Validation, DataProcessing}`; `validateRegistration` rejects a mix-in protocol (validation, data-processing) advertised in `Capabilities` without its matching client, and vice versa, in both directions for both mix-ins — covered by an eight-case table test (`TestRegisterValidation`). The registry's `Client` type (`inproc_client.go`) satisfies `locks.ClaimProducer`, the same consumer-facing interface the gRPC peer client (`lib/runtime/peer/client.go`) also satisfies, and enforces the capability envelope in-process (`EnforceOpenWriteSemantics` on `Open`, capability gates on `SplitScope`/`ScopesConflict`) rather than only at the gRPC boundary. A parallel executor in-process registry (`lib/runtime/executor/inproc_registry.go`) exists as the counterpart this decision extends. Registration, lookup, duplicate rejection, sorted name listing, and mix-in view accessors are each covered by dedicated tests in `inproc_registry_test.go`.
