---
story: lineage-admin
status: as-is
---

# Operator prunes lineage records

## Role

As an operator, I can prune lineage records older than a cutoff timestamp, so that the lineage table doesn't grow unbounded in a long-lived deployment.

## Capability

Operator-driven lineage pruning: cutoff-based remove of lineage records through the control-api or CLI.

## Business value

Operators keep the lineage table bounded in a long-lived deployment without bulk-rebuilding the deployment or accepting unbounded growth.

## Acceptance

With lineage records of varied ages persisted, an operator submits a prune request through the control-api or `rimsky lineage prune` carrying a cutoff; only records strictly older than the cutoff are removed, records at or after the cutoff are untouched (verifiable through a follow-up lineage query).

## Falsifier

Prune removes records at the cutoff boundary, OR removes records newer than cutoff, OR silently drops the cutoff and returns a no-op count.

## Proof

Executable proof.
