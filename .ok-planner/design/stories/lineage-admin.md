---
story: lineage-admin
status: as-is
---

# Operator prunes lineage records

## Story

As an operator, I can prune lineage records older than a cutoff timestamp, so that the lineage table doesn't grow unbounded in a long-lived deployment.

Operator-driven lineage pruning: cutoff-based remove of lineage records through the control-api or CLI.

Operators keep the lineage table bounded in a long-lived deployment without bulk-rebuilding the deployment or accepting unbounded growth.
