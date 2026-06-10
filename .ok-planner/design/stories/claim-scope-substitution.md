---
story: claim-scope-substitution
status: as-is
---

# Template author uses canonical claim_scope

## Role

As a template author, I can use the canonical `{{claim.<alias>.claim_scope}}` substitution in a node's attributes and have it resolve at runtime to the live claim's claim-scope bytes, with the legacy `scope` spelling cleanly refused at registration, so that I have one canonical spelling end-to-end.

## Capability

Single canonical substitution spelling for the claim-scope source; the legacy `scope` spelling is refused at registration with a clear validation message naming the rename.

## Business value

Template authors get one unambiguous spelling for the claim-scope source; legacy spellings fail loudly rather than silently no-op.

## Acceptance

A template using `{{claim.<alias>.claim_scope}}` registers without complaint, instances of it dispatch with the executor receiving the resolved claim-scope bytes for that attribute. A template using the legacy `{{claim.<alias>.scope}}` is refused at registration with a clear validation message naming the rename.

## Falsifier

Legacy `scope` spelling is silently accepted, OR canonical `claim_scope` resolves to empty / wrong bytes at dispatch.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
