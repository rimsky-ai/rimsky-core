---
assumption: error-types-catchall-supported
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# a bare `*` entry in `error_types` catches every class not otherwise mapped, so a template can declare one fallback policy instead of enumerating classes.

As template author, I would take it that a bare `*` entry in `error_types` catches every class not otherwise mapped, so a template can declare one fallback policy instead of enumerating classes.

## Source

published-concept — `concept:error-policy` (trailing-wildcard vocabulary entries, "the same trailing-wildcard convention `concept:signal` fixes")

## What a run would observe

register a template with `error_types: {"*": {action: retry}}` and provoke an unmapped class.

## Measured

The experiment `assumption-error-types-catchall-supported` registered four
versions of one node against a URL that does not resolve, so each run raises
the same `http/network_error`. The exact key works: `{"http/network_error":
{action: pass}}` registered clean and the node settled fresh. The bare `*`
registers and routes nothing — the template was accepted with the warning
`error class "*" is not in any declared vocabulary … the policy registers but
will only match if a peer emits this exact class`, and the node failed.
`http/*` behaved identically. Both are indistinguishable from declaring
nothing: the node with an empty `error_types` failed the same way. Runtime
policy lookup is an exact match on the class name, so no fallback entry exists;
an author who declares one fallback policy instead of enumerating classes gets
default handling for every class they did not name, and the only signal is a
registration warning that does not fail the register.
