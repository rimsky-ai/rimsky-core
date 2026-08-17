---
issue: nominate-protocol-error-and-event-surface-experiments
kind: audit
category: test
artifacts:
  - concept:conformance
status: verified
opened: 2026-08-16T09:39:07Z
---

# Nominate the 13 audit experiments over the gRPC protocols and conformance CLI, the bundled executors, error classes, and event kinds for the project's suites

The audit built 13 experiments to measure user assumptions about the gRPC protocols and conformance CLI, the bundled executors, error classes, and event kinds. Each experiment is a self-contained probe. A probe boots its own containers from the tree's image tag, picks free ports, and exits non-zero when a check fails. All 13 pass at the audited tree: 0 show the product honouring a prior (held), and 13 show it contradicting one (trap). The project's suites hold none of them. The audit adopts nothing itself, and it re-runs the probes only at the next audit. A held probe is a candidate ordinary regression test. A trap probe is a candidate expected-fail test. It starts failing the day someone closes the trap. The decision on scenario tests fixes how the project writes such tests, not which probes it promotes. One member, the event-kinds pairing probe, reproduces the dead-event-kinds finding filed as its own issue. Rule that issue first, because retiring the two kinds would rewrite that probe. The ruling sets the adoption policy for this family. The same policy fits the four sibling families.

## Options

- Adopt each held probe as a scenario test and each trap probe as an expected-fail scenario test, and retire each experiment directory when its test lands; cost: 13 maintained tests, each rewritten to the scenario harness.
- Adopt only the held probes, keep the trap probes as audit instruments, and let the trap registry document them; cost: nobody sees a closed trap until the next audit.
- Adopt no probe and keep all as audit instruments; cost: no regression signal between audits.

The ruling decides whether these probes become tests.

## Ruling

> Recommended ruling (/verify-issues): Adopt the held probes as ordinary scenario tests now. Keep each trap probe as an audit instrument until the owner rules its trap issue. A trap the owner decides to close becomes a regression test with the fix. A trap the owner accepts as documentation stays an instrument.
>
> Rationale: an expected-fail test that encodes accepted behaviour is unneeded in a suite the project keeps deterministic and lean. A held probe is a scenario the project writes anyway. Tying trap adoption to the trap's own ruling keeps the suite from asserting behaviour nobody wants. Flip case: if the owner wants every measured trap to fail loudly the moment it changes, adopt all as expected-fail tests and accept the maintenance.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
