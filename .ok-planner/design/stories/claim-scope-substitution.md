---
story: claim-scope-substitution
status: as-is
---

# Template author uses the canonical claim-scope substitution spelling

## Role

As a template author, I can use the canonical alias-keyed claim-scope substitution in a node's attributes and have it resolve at runtime to the live claim's claim-scope bytes, with the unsupported abbreviated spelling cleanly refused at registration, so that I have one canonical spelling end-to-end.

## Capability

Single canonical substitution spelling for the claim-scope source; any unsupported abbreviated spelling is refused at registration with a clear validation message identifying the canonical spelling.

## Business value

Template authors get one unambiguous spelling for the claim-scope source; unsupported spellings fail loudly rather than silently no-op.

