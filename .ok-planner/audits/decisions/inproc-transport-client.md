---
audit: inproc-transport-client
artifact: decision:inproc-transport-client
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:20:50Z
---

# In-process execution is a third transport case on the executor client pool

Supported. The pool's factory switches on transport across exactly three cases — gRPC, HTTP bridge, and in-process — plus an unknown-transport error, so in-process is one more instance of the existing pattern rather than a parallel path, and all three produce the same client interface with the same execute signature. The in-process case constructs its client from the registry the pool was given, failing with a named error when no registry is wired, and the client resolves its handler from that registry by the endpoint's in-process identity, both when it is constructed and again on each execute; construction rejects an unregistered identity and a wrong transport up front. The per-dispatch handler context is built inside execute from the injected factory, as the cited interface decision prescribes. Transport opacity holds at the dispatch call site: dispatch obtains a client from the pool and calls execute with no transport branch, and searching the whole tree for the in-process transport literal outside the executor package finds only three sites, all of them endpoint construction in the address book and alias seeding, never a dispatch-path condition. The pool's in-process case and its registry requirement each carry a test.
