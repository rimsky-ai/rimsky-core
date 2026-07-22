---
tension: unreachable-service-row-stall
category: unspecified
status: open
affects:
  - executor
  - claim-producer
  - supervisor
  - error-policy
---

# Dispatch rows for unknown / unreachable services sit in queue with no error class

## What is muddy

When a dispatch row is claimed by a supervisor whose resolver cannot find the named executor, a synthetic `unresolved_executor` error class now fires and the run settles to a terminal error — that specific hosted-executor case (a misconfigured or dead executor a supervisor did attempt to dispatch to) is no longer a silent queue stall. What remains muddy: (1) the proxy-mediated case, where the user's agent simply isn't connected yet, is a normal transient state rather than a hard failure, and there is still no threshold-based escalation into an error/alert after repeated misses; (2) a row whose `executor_name` matches no supervisor's `accepted_executors` at all may still never be claimed by any supervisor in the first place, so the `unresolved_executor` path — which only fires once a supervisor has picked the row up — never triggers, and the row can still sit in queue indefinitely.

## Why it matters

In the proxy case, the user's agent not being connected is a normal transient state, and the rimsky operator may want to alert on it after a threshold. There is no mechanism for that today. A row no supervisor's `accepted_executors` ever matches has the same problem: it may never be claimed at all, so it never reaches the resolver check that now catches a claimed-but-unresolvable executor name.

## Resolution candidates (do NOT pick)

- Extend `concept:error-policy` so that dispatch rows targeting unreachable services are escalated into a documented error class after a threshold, and capture in `concept:claim-producer` / `concept:executor` that an acquire-side reachability outcome is observable on the row. The threshold knob lives on the template per `concept:template`.

## Evidence

- The runtime dispatch path now emits an `unresolved_executor` event and settles the run to a terminal error with error class `unresolved_executor` when a claimed row's resolver lookup fails — the "dead executor silently swallows the dispatch" case is closed for rows a supervisor actually picks up. Neither `concept:executor` nor `concept:claim-producer` documents a threshold-alert mechanism for the proxy-transient case, and there is no described guarantee that a row matching no supervisor's accepted set is ever claimed in the first place, so both sub-cases remain a queue-state condition rather than an observable error.
