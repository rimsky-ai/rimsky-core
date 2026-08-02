---
decision: fanout-list-array-store-agnostic
---

# List fan-out is one grammar across both bundled stores

## Choice

The list partition grammar for fan-out — a partition request carrying a list of items produced upstream — is store-agnostic: both bundled claim producers (filesystem and Postgres) serve it through the same split-scope surface, and which bundled store holds the parent claim is a deployment choice, not a separate capability.

## Rationale

The list grammar has no store-dependent semantics — the items come from upstream, not from the store — so per-store variants would duplicate one capability behind two doors and force the story catalog to tell one outcome twice. The one genuinely store-specific partition idiom that exists, folder expansion, is its own grammar with its own story (`story:fs-fanout-expand-folder`), which keeps the line honest: grammars split by semantics, never by backend.

## Alternatives

- Per-store list grammars, each its own capability — rejected: identical semantics duplicated per backend, with the authoring surface diverging for no user-visible reason.
- Serving the list grammar on only one bundled store — rejected: forces a store migration on anyone who needs list fan-out, though the grammar never touches store internals.
