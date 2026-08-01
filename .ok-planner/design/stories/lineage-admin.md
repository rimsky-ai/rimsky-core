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

