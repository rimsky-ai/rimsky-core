---
story: lineage-exploration
status: as-is
---

# Operator walks lineage forward and backward

## Role

As an operator, I can walk the lineage of a run forward and backward, query lineage by claim handle, and pivot through source or named producer, so that I trace how data flowed through the rimsky stack.

## Capability

Operator-driven lineage exploration: walk upstream / downstream from a run; query by claim handle; pivot by source / named producer.

## Business value

Operators trace how data flowed through their rimsky stack — for debugging, impact analysis, or compliance.

## Acceptance

After running an instance whose template produces lineage records, an operator queries the lineage for a run through the control-api and walks upstream to the producers that fed it and downstream to consumers that depended on it; query by claim handle returns the lineage record for that claim; the source-pivot and producer-pivot return the records they should — a producer the run actually used appears in upstream, a consumer that actually consumed appears in downstream.

## Falsifier

A real upstream producer is missing from the ancestor walk, OR a real downstream consumer is missing from the descendant walk.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
