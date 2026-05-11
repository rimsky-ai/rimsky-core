---
tension: store-vs-claim-producer-vocabulary
category: overloaded
status: open
affects:
  - claim-producer
  - rimsky-yml
---

# `Store` vs `ClaimProducer` are both live names for the same concept

## What is muddy

The codebase has two live names for the same noun:

- **`ClaimProducer`** — the protocol-level canonical name. Proto service `ClaimProducer`. Go interface `ClaimProducer`. CLAUDE.md "Vocabulary" promotes this as canonical.
- **`Store`** — the colloquial bundled-services term. The bundled reference impls live under `stores/` (filesystem, postgres, stub). YAML `stores:` aliases `claim_producers:`. The Go type `Store = ClaimProducer` legacy alias is "kept temporarily" (CLAUDE.md "What this repo is").

Both names appear in code, prose, and config. CLAUDE.md "Vocabulary" calls the split out explicitly: "ClaimProducers (the protocol-layer name; 'stores' is the bundled-services-layer colloquial term)."

## Why it matters

External readers, third-party producer authors, and tooling all need to know which name is authoritative. Two-name systems drift in different directions; documentation references "the postgres store" but the protocol is "the postgres claim producer." Type assertions / mocks / docs cite different names.

## Resolution candidates (do NOT pick)

- Pick one (probably `ClaimProducer`) and rename everywhere, including `stores/` directory + YAML key + colloquial prose.
- Formalize "Store" as a directory-naming convention and `ClaimProducer` as the protocol-layer name, document the split.
- Add a vocabulary-lint that rejects `Store` outside the bundled-services directory.

## Evidence

- `_discover/2026-05-10-out-of-process-claim-producers.md` Observations bullet 1.
- `_discover/2026-05-10-unified-rimsky-yml-config.md` "stores alias" note.
- CLAUDE.md "Vocabulary".
- `foundation/locks/interface.go` (legacy `Store = ClaimProducer` alias comment).

