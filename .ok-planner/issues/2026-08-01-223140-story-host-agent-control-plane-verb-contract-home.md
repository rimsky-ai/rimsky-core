---
issue: story-host-agent-control-plane-verb-contract-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:host-agent-control-plane
  - concept:rimsky
status: verified
opened: 2026-08-01T22:31:40Z
---

# Where do the host-agent CLI verb contracts live once the story reduces?

The host agent (the daemon that spawns local service binaries on an operator's machine) is controlled through CLI verbs — start, status, stop — and the story covering that surface carries per-verb behavior in its prose: start launches connected to the configured proxy or refuses with a diagnostic; status reports connection state, the configured proxy, and spawned children. The format rules force the story down to its sentence, and the question is whether those verb contracts deserve a durable home first.

Re-verification confirms the code honors every claim (`code:cmd/rimsky/cli/agent.go`). But the corpus has an explicit stance against homing this altitude of detail: the CLI concept's capability-surface section states that each surface's verb set is owned by the CLI code and its operator-facing reference, not enumerated in the corpus (`concept:rimsky`). A decision is a weak fit too — refusing to start without a required config value is expected behavior, not a choice with an identifiable rejected alternative.

## Options

- Home the verb contracts in the CLI concept — contradicts that concept's own stated non-enumeration policy.
- Home them in the host-agent concept — the daemon's side, but that concept doesn't describe its own CLI control surface either, so this opens a new enumeration the corpus has avoided.
- Rule the contracts below corpus altitude and reduce the story — the verb behavior stays owned by the CLI code, its tests, and the operator reference.

The ruling decides whether per-verb CLI behavior is corpus content at all.

## Ruling

> Recommended ruling (/verify-issues): rule the verb contracts below corpus altitude and reduce the story to its sentence. The corpus already decided CLI verb sets are code-owned; verb *behavior* is the same altitude, and carving an exception for the host agent would erode that line.
>
> Rationale: both homing options fight the corpus's existing grain — one contradicts an explicit non-enumeration clause, the other starts a new enumeration — and there is no tradeoff here worth a decision, just expected refuse-on-missing-config behavior the tests already pin. Flip case: if another surface starts depending on a specific verb's behavior (say, the proxy's registration flow keys on start's refusal semantics), that contract becomes a boundary between components and earns concept altitude.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
