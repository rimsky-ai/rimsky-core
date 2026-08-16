---
audit: inproc-eventstream
artifact: decision:inproc-eventstream
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:20:19Z
---

# The in-process executor call is unary, bridged only for cancellation and panic isolation

Supported. The in-process client's execute method invokes the handler once and returns one outcome or one error; there is no stream type, no receive loop, and no end-of-stream signal anywhere in the client or the handler interface, so the transport mirrors the unary shape the other two transports carry. The bridge is exactly what the decision describes and nothing more: a single goroutine writing into a result channel of capacity one, selected against the caller's context. Both stated purposes are present and each is tested — a deferred recover converts a handler panic into an ordinary error rather than unwinding the shared process, covered by a panic test, and the select returns the context error the moment the caller's context ends, covered by two tests, one where the handler honours the deadline and one where the handler deliberately ignores its context and the caller still unblocks on cancel. Nothing else rides the bridge: an ordinary handler error is returned unwrapped, and the outcome is passed through untouched.
