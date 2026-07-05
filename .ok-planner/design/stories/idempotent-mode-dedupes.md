---
story: idempotent-mode-dedupes
status: as-is
---

# Idempotent modes drop re-runs whose input bag equals a predecessor

## Role

As a template author whose executor is expensive and whose inputs are sometimes byte-identical across cascade rounds, I can opt my node into `cascade_mode=idempotent-queue` (or `idempotent-settled`) and know that re-runs whose input bag JCS-equals a predecessor are dropped before reaching the executor. My executor never sees consecutive identical inputs.

## Capability

When `cascade_mode=idempotent-queue`, at pending→stale transition the gate evaluator computes the JCS canonical hash (per `decision:jcs-cyberphone`) of the new run's input bag and compares it to the input bag of any prior cascade-driven stale-not-claimed for the same (node, run-scope). If equal, the new run is dropped (does not transition to stale). If unequal — or if no prior cascade stale exists — the new run transitions to stale and queues normally.

When `cascade_mode=idempotent-settled`, the comparison ALSO covers the most recent fresh-settled predecessor's input bag when no cascade-driven stale-not-claimed exists. This catches the "queue was just cleared by a settle, now an identical cascade arrives" case that `idempotent-queue` would miss.

Both variants compare INPUT bags (what the executor saw at dispatch), not LIVE bags (post-writeback). The input bag is preserved on the run's attribute store (per `concept:attribute`) separately from the live bag. Non-cascade rows are immune to dedup — `operator_invalidate`, `recalculate`, and `message_delivery` creation reasons always queue regardless of bag equality.

## Business value

Expensive pure executors should not re-run for identical inputs. Most-recent collapses to the latest input but still dispatches it; if the latest is byte-equal to what already ran, the executor's work is wasted. The idempotent modes catch this case mechanically without the executor having to implement its own dedup. The split between queue and settled variants lets the author pick the cost-of-comparison granularity (queue-only is cheaper; settled-also catches more).

## Acceptance

An author writes a graph A → B with `cascade_mode=idempotent-queue` on B. A re-runs twice in succession but emits byte-identical output both times. Two cascade walks target B. The first creates a pending that transitions to stale (no prior cascade-stale to compare against). The second creates a pending that transitions to stale, where the JCS comparison finds the prior cascade-stale's input bag matches — the second is dropped. B's in-flight run settles. B dispatches exactly ONCE more (the first cascade-stale; the second was dropped). Observable as: B's lineage shows 2 runs total, not 3.

Under `idempotent-settled`: same setup, but the first cascade-stale dispatches and settles BEFORE the second cascade arrives. The second cascade's input bag is then compared against the settled predecessor's input bag — equal, so the second is dropped. B dispatches exactly once more (the first cascade-stale), not twice. Observable as: B's lineage shows 2 runs total, not 3, even though the dedup window crossed a settle boundary.

## Falsifier

B's executor is invoked with the duplicate bag — observable by counting executor invocations and comparing bag values across invocations. OR a non-cascade run (`operator_invalidate`, `recalculate`, or `message_delivery` creation reason) is dropped due to bag equality — observable by inspecting the lineage for a non-cascade row that vanished.

## Proof

An executable scenario test where A re-runs twice with byte-identical bags, B is configured with `cascade_mode=idempotent-queue`, the test asserts B's lineage shows only the original dispatch plus one cascade re-run (not two). A separate test for `idempotent-settled` runs the dedup across a settle boundary.
