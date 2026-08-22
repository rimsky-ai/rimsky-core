---
concept: lineage-record
---

# Lineage record

## What it is

A lineage record is one append-only entry in the lineage projection (see `concept:lineage`). Two kinds exist. A **leaf-run record** captures one computational unit: which run terminated, where it sits in the run tree, what triggered its frame, what it substituted from upstream, which claims it held, the executor and template that produced it, and how it ended. A **claim-terminal record** captures one data-promotion unit: which claim handle resolved, where it sits in the claim tree, the producer that resolved it, the substrate identity and version it touched, the disposition it reached, and the supervisor that drove the termination where one did. The projection covers every claim-handle terminal.

## Purpose

Lineage records answer two questions after the fact. A leaf-run record says what a computation read and what it produced, so a reader traces a value back to its sources. A claim-terminal record says how a promotion resolved and which supervisor terminated it — the only surface that answers the second question, because a claim handle stops carrying its holder reference once it leaves the active state (see `concept:claim-handle`). A sub-graph caller produces two leaf-run records for one dispatch: one for the inputs it absorbed on entry, one for the outcome it resolved to. Both carry the calling run's identity and a terminal-kind field the reader discriminates on, so the pair is deliberate rather than a duplicate.

## Boundaries

A lineage record owns its per-kind shape and the write path into the projection. It does not own the projection's storage or its query surface, which belong to `concept:lineage`, nor the emission to an external receiver, which belongs to the subscribing receiver.

Records cover computation (see `decision:lineage-records-computation-only`). A data-processing promotion's record carries the resolved version (see `decision:promotion-lineage-record-after-commit`). Identity and substrate references appear as hashes (see `decision:lineage-identity-hashed-not-raw`).

see also: `concept:lineage`, `concept:claim-handle`, `concept:node-run`, `concept:auto-terminal`, `concept:data-processing`, `concept:inertness`.
