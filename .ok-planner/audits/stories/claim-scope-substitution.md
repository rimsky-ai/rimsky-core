---
audit: claim-scope-substitution
artifact: story:claim-scope-substitution
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# One spelling for claim-scope substitution, resolved at runtime and range-checked at registration

Supported. Against an all-in-one deployment with a filesystem-backed claim
producer, one node declared an attribute sourced from the canonical claim-scope
directive and settled carrying the same bytes the claim-handle ledger recorded
for its live claim. With the same node's selector written non-canonically the
directive still resolved to the producer's canonical claim scope, so the value
follows the claim rather than the template text. Both of the story's spellings
were put to registration on the identical template shape: the canonical one
registered, and the abbreviated one was refused with a validation error naming
the directive, the attribute path, and the spellings admitted.

## Compliance

The capability clause qualifies the refusal as "cleanly", a quality judgment no
procedure settles, which the story rules keep out of a story body; the compliant
text is "with the unsupported abbreviated spelling refused at registration".
