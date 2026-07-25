---
issue: decision-blob-backend-missing-proof-field
kind: audit
category: proof
artifacts:
  - decision:blob-backend
status: verified
opened: 2026-07-24T00:00:00Z
---

# Nothing would notice if the default blob backend silently changed

Rimsky stores large binary data ("blobs" — payloads too big to keep inline) through a pluggable backend, and a decision document records the choice: filesystem is the shipped default, at a documented size threshold where data spills out of the database. Decision documents are required to carry a Proof section — the concrete, automatic check that fails if the choice is silently violated. This one has none, and the single code comment citing it sits on a config-file *writer* in a deployment tool, not on any check. Net effect: if a change quietly flipped the shipped default backend or moved the spill threshold, no test would catch it.

The remedy is the open question, and the tempting shortcut doesn't work: retiring this decision as redundant (the broader blob-backend concept has heavy scenario-test coverage, and a sibling decision covers backends being pluggable at all) just relocates the gap, because the sibling decision has no Proof either, and concept-level scenario tests prove behavior, not "which default ships." The corpus has one directly transferable precedent: a recently amended decision whose Proof drives every backend variant through one identical code path, so a variant-specific shortcut has nowhere to hide — here that shape becomes "assert the shipped default is filesystem at the documented threshold."

## Options

- **Write the Proof** — a config-defaults assertion on the shipped default and threshold — and move the citation off the config writer onto that check.
- **Retire the decision** in favor of the concept + the pluggability sibling — honest only if one of those gains real proof of the specific default-choice claim, which neither has today.

The ruling decides: add the Proof and relocate the citation, or retire — and if retiring, which surviving artifact carries the proof burden.

## Ruling

> Recommended ruling (/recommend-rulings): Add the Proof: a config-
> defaults assertion that the shipped persistence.blob.backend default
> is filesystem at the documented spill threshold, carrying the
> @decision: blob-backend annotation — moving the tag off the compose
> config-writer.
>
> Rationale: Adding proofs is unrestricted; retirement would just
> relocate the gap into blob-backends-pluggable, which has no Proof
> either. The object-store-watching-model amendment is the precedent
> shape.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
