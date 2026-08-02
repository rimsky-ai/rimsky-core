---
audit: claude-agent-error-classes-closed
artifact: decision:claude-agent-error-classes-closed
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:41Z
---

# Declared error-class vocabulary is closed, advertised, and enforced at the emission boundary

Supported. `schema.go::DeclaredErrorClasses` returns a fixed 13-entry list (three of them trailing-wildcard prefixes); `observability.go::CapabilitiesPayload` advertises it verbatim as `DeclaredErrorClasses` on the executor's observability capabilities surface. `internalmcpserver.go`'s `report_error` handler calls `errorClassDeclared` and rejects any class not in (or matched by a wildcard prefix of) that list with a JSON-RPC error naming the offending class, before the outcome reaches the runtime — checked the one call site that constructs an executor-facing error outcome from agent-supplied input (there is no second path; internally-synthesized error classes such as timeouts and spawn failures are hardcoded to values already in the list). `TestRunAgentReportErrorRejectsUndeclaredErrorClass` proves both halves directly: an undeclared class is rejected with the class named in the message, and a declared wildcard-matched class (`agent/subprocess_exit/before_complete` under `agent/subprocess_exit/*`) is accepted through to the outcome.
