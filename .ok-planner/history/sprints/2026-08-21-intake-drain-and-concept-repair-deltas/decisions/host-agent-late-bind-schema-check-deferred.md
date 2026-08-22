---
decision: host-agent-late-bind-schema-check-deferred
---

# Late-bound executors check attribute schema per dispatch

## Choice

Rimsky checks a late-bound executor's attribute-schema conformance at each dispatch, once the spawned binary's own capabilities handshake has completed, rather than at template registration. A mismatch settles that dispatch as a contract error (see `concept:host-agent-proxy`, `concept:template`).

## Rationale

The schema a late-bound binary accepts lives on a developer's machine and does not exist when the template registers. The template names a binding, not a binary, and the binary behind that binding changes between one dispatch and the next. The handshake is the first moment the real schema is knowable, and checking there still refuses a payload the binary never promised to accept. The cost is that an author learns about a mismatch at dispatch rather than at registration.

## Alternatives

- Check at registration against a schema the template declares — rejected: the author restates a schema the binary owns, and the two drift with no signal.
- Forward the resolved attributes unchecked — rejected: the binary receives a payload it never advertised, and the failure surfaces as whatever that binary does with it rather than as a contract error.
