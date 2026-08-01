---
decision: intx-suffix-convention
status: as-is
---

# The InTx suffix means "requires an open transaction"

## Choice

A persistence-layer function carrying the `...InTx` suffix requires an
already-open transaction passed in by its caller — that is the
suffix's whole meaning. Paired variants (a public `X` that opens its
own transaction backed by a private `XInTx`) are forbidden: one method
taking an optional transaction parameter is the house shape. The blob
backend's transactional interface carries `...InTx` method names — a
capability split across two interfaces, not a duplicated pair.

## Rationale

"Requires a transaction" and "optionally opens one" are different
jobs, and one-idiom-per-job permits a distinct spelling for each.
Writing the convention down stops the bare suffix being flagged as
residue of the forbidden pairing, and keeping no live pairs removes
the copy-source hazard — a live pair would read as the house pattern
and get copied by the next contributor.

## Alternatives

- Rename the bare-suffix helpers away from `InTx` — rejected: the
  suffix reads correctly for "requires a transaction"; renaming ~20
  call surfaces buys nothing.
- Tolerate public/private pairs alongside the optional-parameter
  shape — rejected: same job, second dialect, and a live copy source
  for the forbidden idiom.
