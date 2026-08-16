---
audit: executor-unary-rpc
artifact: decision:executor-unary-rpc
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:44:49Z
---

# The executor's Execute call is unary and returns a four-case outcome oneof

Supported. The executor service declares exactly one method, taking an execute request and returning an outcome with no stream keyword on either side, and the generated client and server stubs are the unary forms with no stream-reader path. The outcome message is a oneof of precisely the four cases the decision names — success, error, park, and the await-async-callback acknowledgement — with no fifth case and nothing reserved beside them. Checked all nine service definitions in the protocol module: two of them declare a server-streaming method and one a bidirectional stream, and none of those is the executor, so no streaming variant of the dispatch call exists anywhere. The supervisor's dispatch path makes a single call and reads a single outcome, and the async mode is served by the acknowledgement case plus a later callback rather than by a held-open stream.
