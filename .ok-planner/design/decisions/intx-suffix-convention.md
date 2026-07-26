---
decision: intx-suffix-convention
status: as-is
---

# The InTx suffix means "requires an open transaction"

## Choice

A persistence-layer function carrying the `...InTx` suffix requires an
already-open transaction passed in by its caller — that is the
suffix's whole meaning. The retired idiom of paired variants (a public
`X` that opens its own transaction backed by a private `XInTx`) does
not return: one method taking an optional transaction parameter is the
house shape. The blob backend's transactional interface keeps its
`...InTx` method names — a capability split across two interfaces, not
a duplicated pair.

## Rationale

"Requires a transaction" and "optionally opens one" are different
jobs, and one-idiom-per-job permits a distinct spelling for each.
Writing the convention down stops the bare suffix being re-flagged as
residue of the retired pairing, and removing the surviving pairs
removes the copy-source hazard — a live pair reads as the house
pattern and gets copied by the next contributor.

## Alternatives

- Rename the bare-suffix helpers away from `InTx` — rejected: the
  suffix reads correctly for "requires a transaction"; renaming ~20
  call surfaces buys nothing.
- Tolerate the two surviving public/private pairs — rejected: same
  job, second dialect, and a live copy source for the retired idiom.

## Proof

A fitness check fails when a persistence method `X` coexists with a
same-named `XInTx` sibling in the same package. Falsifier:
reintroduce a public/private pair — the check turns red.
