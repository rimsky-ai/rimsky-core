---
audit: claude-agent-error-classes-closed
artifact: decision:claude-agent-error-classes-closed
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:43:41Z
---

# The claude-agent error vocabulary is declared, advertised, and gated at emission

Supported. The executor declares its error classes as one static list, thirteen members including two trailing-wildcard families, and that same list is what it advertises: its observability capabilities response returns it, and the bundled in-process registration path advertises it into the discovery cache. The emission gate is the load-bearing half and it exists: the agent-facing report-error tool rejects any class the declaration does not cover — exact match or wildcard prefix — with an error naming the offending class, before the outcome is recorded, so a free-form string never becomes an outcome. Sweeping every error-class literal the executor's own non-test sources emit turns up sixteen spellings, and every one of them is covered by the declaration, the concrete subprocess-exit and tool-use-failure spellings by their wildcard families. A test drives an undeclared class through the tool and asserts the rejection names it, then drives a wildcard-matched class through and asserts it settles as that class; another test pins the declared list, and a third pins the advertised count.
