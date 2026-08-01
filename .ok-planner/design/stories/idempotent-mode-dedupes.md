---
story: idempotent-mode-dedupes
status: as-is
---

# Idempotent modes drop re-runs whose input bag equals a predecessor

## Story

As a template author whose executor is expensive and whose inputs are sometimes byte-identical across cascade rounds, I can opt my node into `cascade_mode=idempotent-queue` (or `idempotent-settled`) and know that re-runs whose input bag JCS-equals a predecessor are dropped before reaching the executor. My executor never sees consecutive identical inputs.

When `cascade_mode=idempotent-queue`, at pending→stale transition the gate evaluator computes the JCS canonical hash (per `decision:jcs-cyberphone`) of the new run's input bag and compares it to the input bag of any prior cascade-driven stale-not-claimed for the same (node, run-scope). If equal, the new run is dropped (does not transition to stale). If unequal — or if no prior cascade stale exists — the new run transitions to stale and queues normally.

When `cascade_mode=idempotent-settled`, the comparison ALSO covers the most recent fresh-settled predecessor's input bag when no cascade-driven stale-not-claimed exists. This catches the "queue was just cleared by a settle, now an identical cascade arrives" case that `idempotent-queue` would miss.

Both variants compare INPUT bags (what the executor saw at dispatch), not LIVE bags (post-writeback). The input bag is preserved on the run's attribute store (per `concept:attribute`) separately from the live bag. Non-cascade rows are immune to dedup — `operator_invalidate`, `recalculate`, and `message_delivery` creation reasons always queue regardless of bag equality.

Expensive pure executors should not re-run for identical inputs. Most-recent collapses to the latest input but still dispatches it; if the latest is byte-equal to what already ran, the executor's work is wasted. The idempotent modes catch this case mechanically without the executor having to implement its own dedup. The split between queue and settled variants lets the author pick the cost-of-comparison granularity (queue-only is cheaper; settled-also catches more).
