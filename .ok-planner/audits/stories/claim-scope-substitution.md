---
audit: claim-scope-substitution
artifact: story:claim-scope-substitution
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:06:40Z
---

# The canonical claim-scope directive resolves to the live claim's scope, and the abbreviated spelling is refused

Supported. Driven through the public surface against a released-image stack with
the bundled filesystem claim producer over a mounted workspace, on a node that
acquires a claim and passes through whatever the directive resolved to. Six
checks, none failing. The canonical spelling resolved to the claim's scope bytes
exactly as the claim-handle ledger recorded them for that live claim. Written
with a deliberately non-canonical selector, the directive still resolved to the
producer's canonical scope rather than the template's selector text, so the value
follows the claim. The abbreviated spelling was refused at registration with a
validation error naming the offending directive, the attribute path it sits on
and the segments the grammar admits, and the canonical form registered on the
identical template shape — one spelling that works, one that is refused before
anything runs.
