---
issue: decisions-spec-altitude-mechanism-detail
kind: audit
category: compliance
status: verified
opened: 2026-07-25T21:11:30Z
---

# About twenty decisions read as specs rather than choice points

A decision records a choice, its rationale, and its alternatives — not schemas, call sequences, or literal wire detail. About twenty decisions sit below that altitude, re-verified in the five spot-checked files: literal routes and status codes (`decision:keepalive-endpoint`), a full attribute schema plus dispatch algorithm (`decision:loop-counter-shape`), an exhaustive state-transition walkthrough (`decision:held-as-state-not-phase`), a literal Go function name (`decision:per-service-load-opts-from-env`). The corpus's own precedent shows the target altitude: `decision:idempotency-status-code-distinction` deliberately abstracts a status code to "a created-resource status code" — the house style already exists; these twenty predate it.

Two notes sharpen the previous framing. The old ruling's target form — "choice/rationale/alternatives/proof" — is stale: decisions carry no Proof section anymore (verification belongs to the periodic implementation audit), so the trim target is Choice/Rationale/Alternatives alone, and relocated spec detail lands in code and tests, not in any corpus section. And where exactly "naming the artifact" ends and "spec enumeration" begins is the sibling boundary question `issue:decisions-enumerate-routes-and-envs-in-body` rules on — that reading should govern this sweep's cuts. This population overlaps the sibling sweeps for missing Alternatives and historical narration; one joint per-file pass covers all three.

## Options

- **Trim to choice altitude in the joint decisions pass** — spec detail relocates to code and tests, applying the enumeration boundary the sibling ruling draws.
- **Rule the lower altitude acceptable** — inconsistent with the corpus's own abstraction precedent and the "decisions are not specs" rule.

The ruling confirms the forced direction and the joint scheduling.

## Ruling

> Generated ruling (/verify-issues): trim the ~20 spec-altitude
> decisions to Choice/Rationale/Alternatives inside the joint
> decisions pass with issue:decisions-corpus-wide-missing-alternatives
> and issue:decisions-historical-language-residue, relocating
> schemas, routes, literal codes, and algorithms into code and
> tests, with the cuts governed by the enumeration boundary
> issue:decisions-enumerate-routes-and-envs-in-body settles. The
> "decisions are not specs" rule forces the direction; only the
> editing is sprint work.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
