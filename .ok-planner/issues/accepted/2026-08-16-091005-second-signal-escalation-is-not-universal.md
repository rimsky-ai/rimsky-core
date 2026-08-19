---
issue: second-signal-escalation-is-not-universal
kind: audit
category: conflicting
artifacts:
  - decision:graceful-shutdown
status: promoted
opened: 2026-08-16T09:10:05Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# Nineteen of twenty-two rimsky processes absorb a second interrupt instead of exiting

The graceful-shutdown decision promises two things. A first interrupt starts an orderly drain with a fixed grace window, 5s or 30s by process kind, deliberately not configurable. A second interrupt on every path, without exception, escalates to a hard exit. That escalation is the operator's escape hatch when a drain hangs. Only 3 of the 22 production entry points install that escalation. The other 19 register a signal handler that consumes the first signal and never rearms: all ten bundled-service mains, the host agent and its proxy, the migrate binary, and the CLI's long-running verbs. Registering a handler disables the runtime's default die-on-signal. Those 19 processes therefore absorb a second interrupt an operator would otherwise use to kill them. Two smaller claims also fail: a third grace value (10s) governs the bundled services, and the environment already configures the host agent's spawned-child grace. The ruling decides whether the code restores the promise or the decision shrinks to what exists.

The 19 uncovered processes are a live operability defect, not only a stale sentence. An operator watching a wedged bundled service presses Ctrl-C twice, gets no exit, and reaches for kill -9.

## Options

- Install the second-signal escalation at every entry point and choose each grace value; cost: 19 files change, and each needs a chosen window.
- Extract one shared signal helper that every entry point uses, so the guarantee is structural and a future 23rd process cannot omit it; cost: the largest change, and the only one that keeps "every path" true by construction.
- Narrow the decision to the three covered paths and the three real grace values, and drop the "not configurable" claim; cost: ratifies the absorbed second interrupt as intended behaviour.

The ruling decides whether "every path" is repaired or retracted.

## Ruling

> Recommended ruling (/verify-issues): Restore the promise. Give every entry point the shared drain-then-escalate behaviour through one helper, keep the three grace values as the documented set, and say plainly that the host agent's child grace is the one operator-tunable window. Keep the decision at every path rather than narrowing it to the three covered paths.
>
> Rationale: the decision justifies its fixed grace by saying the second signal is always there. That reason holds only where the escalation is installed. One helper makes the guarantee structural instead of remembered, which is how this project pins every other universal. Flip case: if the owner decides bundled services fall outside the shutdown discipline, because an orchestrator kills those containers, then narrowing the decision to core roles and saying so is the honest text.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
