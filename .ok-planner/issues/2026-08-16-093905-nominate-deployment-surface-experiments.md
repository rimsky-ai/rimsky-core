---
issue: nominate-deployment-surface-experiments
kind: audit
category: test
artifacts:
  - concept:conformance
status: verified
opened: 2026-08-16T09:39:06Z
---

# Nominate the 19 audit experiments over environment variables, configuration keys, images and ports, persistence backends, and sensors for the project's suites

The audit measured user assumptions about environment variables, configuration keys, images and ports, persistence backends, and sensors by building 19 experiments — each a self-contained probe that boots its own containers from the tree's image tag, picks free ports, and exits non-zero when a check fails. All 19 pass at the audited tree: 4 demonstrate the product honouring a prior (held), 15 demonstrate it contradicting one (trap). None is in the project's suites; the audit adopts nothing itself and re-runs them only at the next audit. A held probe is a candidate ordinary regression test; a trap probe is a candidate expected-fail test that starts failing the day the trap is closed. The decision on scenario tests fixes how such tests are written, not which probes are promoted. The ruling decides the adoption policy for this family — one policy applied to four sibling families is the natural shape.

## Options

- Adopt held probes as scenario tests and trap probes as expected-fail scenario tests, retiring each experiment directory as its test lands; cost: 19 maintained tests, each rewritten to the scenario harness.
- Adopt only the held probes; leave traps as audit instruments and let the trap registry document them; cost: a closed trap is noticed only at the next audit.
- Adopt none; keep all as audit instruments; cost: no regression signal between audits.

The ruling decides whether these probes become tests.

## Ruling

> Recommended ruling (/verify-issues): Adopt the held probes as ordinary scenario tests now, and leave the trap probes as audit instruments until their trap issues are ruled — a trap the owner decides to close becomes a regression test with the fix; a trap the owner accepts as documentation stays an instrument.
>
> Rationale: expected-fail tests encoding accepted behaviour are dead weight in a suite the project keeps deterministic and lean, while a held probe is exactly a scenario the project already writes; tying trap adoption to the trap's own ruling keeps the suite from asserting behaviour nobody wants. Flip case: if the owner wants every measured trap to fail loudly the moment it changes, adopt all as expected-fail tests and accept the maintenance.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
