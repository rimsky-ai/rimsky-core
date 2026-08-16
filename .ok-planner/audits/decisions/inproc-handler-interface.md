---
audit: inproc-handler-interface
artifact: decision:inproc-handler-interface
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:20:30Z
---

# What in-process utility executors implement, and what the handler context gives them

Unsupported, because one of the two side-channel effects the Choice attributes to the handler-context struct is not there. The interface itself matches: it is a single-method Go interface declaring one execute method taking a context, the generated execute-request message, and the handler-context struct, returning the generated outcome message or an error, with the generated protobuf types passed directly as values and no encoding step at the call boundary. The handler context, however, carries exactly one field — the cascade-message sender — and no scratch accessor; the struct was read in full and is constructed at only two sites, neither of which sets anything else. Scratch is not a side channel at all in this codebase: handlers receive it as a field on the request message and return it as a field on the outcome, which is the main channel, and in fact none of the three builtin handlers touches scratch by any route. The Choice's other imprecision is smaller: the in-process client returns a handler error to its caller unchanged, and the translation into an error terminal happens in the shared dispatch layer that treats all three transports identically, not in the in-process client. Coverage of what does exist is good — happy path, handler error surfacing, scratch round-tripping on the outcome, and the handler-context factory receiving typed identifiers.
