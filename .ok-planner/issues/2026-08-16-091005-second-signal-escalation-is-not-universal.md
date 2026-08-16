---
issue: second-signal-escalation-is-not-universal
kind: audit
category: conflicting
artifacts:
  - decision:graceful-shutdown
status: verified
opened: 2026-08-16T09:10:05Z
---

# Nineteen of twenty-two rimsky processes swallow a second interrupt instead of exiting

The graceful-shutdown decision promises two things: a first interrupt starts an orderly drain with a fixed grace window (5s or 30s by process kind, deliberately not configurable), and a second interrupt on every path, without exception, escalates to a hard exit — the operator's escape hatch when a drain hangs. Only 3 of the 22 production entry points install that escalation. The other 19 (all ten bundled-service mains, the host agent and its proxy, the migrate binary, the CLI's long-running verbs) register a signal handler that consumes the first signal and never rearms; because registering a handler disables the runtime's default die-on-signal, those processes do not merely lack the escape hatch — they actively absorb a second interrupt an operator would otherwise use to kill them. Two smaller claims also fail: a third grace value (10s) governs the bundled services, and the host agent's spawned-child grace is already configurable by environment. The ruling decides whether the promise is restored in code or the decision shrinks to what exists.

The 19 uncovered processes are a live operability defect, not only a stale sentence: an operator watching a wedged bundled service hangs on Ctrl-C twice and reaches for kill -9.

## Options

- Install the second-signal escalation at every entry point, choosing each grace value; cost: touches 19 files and each needs a chosen window.
- Extract one shared signal helper every entry point uses, so the guarantee is structural and a future 23rd process cannot omit it; cost: the largest change, but the only one that keeps "every path" true by construction.
- Narrow the decision to the three covered paths and the three real grace values and drop the "not configurable" claim; cost: ratifies the absorbed second interrupt as intended behaviour.

The ruling decides whether "every path" is repaired or retracted.

## Ruling

> Recommended ruling (/verify-issues): Restore the promise — give every entry point the shared drain-then-escalate behaviour through one helper, keep the three grace values as the documented set, and say plainly that the host agent's child grace is the one operator-tunable window. Do not narrow the decision to the three covered paths.
>
> Rationale: the decision's own reason for a fixed grace ("the second signal is always there") only holds if it is; a helper makes it structural instead of remembered, which is how this project pins every other universal. Flip case: if the owner decides bundled services are not part of the shutdown discipline at all (they are containers the orchestrator kills), then narrowing the decision to core roles and saying so is the honest text.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
