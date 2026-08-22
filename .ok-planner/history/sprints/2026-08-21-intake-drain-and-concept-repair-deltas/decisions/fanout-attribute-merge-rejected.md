---
decision: fanout-attribute-merge-rejected
---

# Fan-out does not merge per-partition attribute writebacks

## Choice

Per-partition executor attribute writebacks never aggregate onto the fan-out parent's attribute bag. An author who needs per-fan-out aggregation routes it through the claim-producer protocol (see `concept:fan-out`, `concept:data-processing`).

## Rationale

Every partition is a run of the same template node under the same attribute schema. A flat per-key merge therefore collides on every key, and any tie-break rimsky picked would be arbitrary. The producer already holds the semantics that make an aggregate meaningful, so the producer aggregates its registered candidates and surfaces the result at parent commit. Rimsky invents no aggregator vocabulary of its own.

## Alternatives

- Merge per key under a fixed rule such as last writer wins — rejected: the winner is then the dispatch order, which is not a property any author declared.
- Give templates an aggregator vocabulary naming a merge per key — rejected: it builds a second computation language inside rimsky for work the producer and the receiving executors already do.
