---
issue: stub-mode-runtime-only-gate
kind: human
category: unclear
artifacts:
  - concept:conformance
  - concept:executor
status: promoted
sprint: 2026-07-25-issue-drain-2026-07-22-batch.md
opened: 2026-07-22T02:37:13Z
---

# Pointing the conformance suite at a live LLM executor spends real money by default

Rimsky ships a conformance CLI that exercises an executor — a service that does the real work behind a graph node, such as calling an LLM — against the standard protocol test suite. Run it against a production LLM executor without one particular flag and nothing stops you: the suite dispatches real requests and spends real API money. The safety exists, but it's opt-in — it only fires when the operator already knows to ask for it. The question is whether to flip that default.

How it actually works (confirmed in code): the CLI always probes whether the target is in "stub mode" — a safe test double that fakes its work. The probe result already skips the handful of scenarios that specifically need a stub. What it does *not* do by default is stop the run: only the `--require-stub-mode` flag makes a non-stub endpoint a hard failure, and the CLI has no way of knowing on its own whether the endpoint it was pointed at is a harmless test double or a production service with an API key. The codebase already has a posture for exactly this shape of risk — its outbound-HTTP services block dangerous destinations by default and require an explicit opt-in to relax — which is the opposite polarity from this flag.

## Options

- **Reverse the default.** Refuse a non-stub endpoint unless the operator passes an explicit allow-live override; retire the now-redundant opt-in flag. Cost: one extra flag on the rare, deliberate live-endpoint run.
- **Recognize "dangerous" executors and require the gate only for them.** Needs a registry or naming convention that doesn't exist and must track every current and future LLM-calling executor, including third-party ones.
- **Have executors self-declare stub status in the connection handshake.** New wire-protocol surface for information the existing probe already gets by asking.

The ruling decides: flip the default, build one of the narrower detection schemes, or accept the current opt-in as-is.

## Ruling

> Recommended ruling (/recommend-rulings): Reverse the default: the
> conformance runner probes stub mode on every executor run and
> refuses a non-stub endpoint unless the operator passes an explicit
> allow-live override; --require-stub-mode is retired as redundant.
>
> Rationale: Fail-closed matches the egress-guard precedent (dangerous
> defaults are closed, opt-out is explicit), and the cost asymmetry
> decides it: one extra flag on the rare deliberate live run versus
> real API spend on an accidental one. A protocol-level self-
> declaration adds handshake surface for what the existing probe
> already answers.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
