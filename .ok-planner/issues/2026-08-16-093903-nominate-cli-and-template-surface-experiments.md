---
issue: nominate-cli-and-template-surface-experiments
kind: audit
category: test
artifacts:
  - concept:conformance
status: verified
opened: 2026-08-16T09:39:04Z
---

# Nominate the 20 audit experiments over the CLI verbs and flags, template keys, and compose manifests for the project's suites

The audit measured user assumptions about the CLI verbs and flags, template keys, and compose manifests by building 20 experiments. Each experiment is a self-contained probe that boots its own containers from the tree's image tag, picks free ports, and exits non-zero when a check fails. All 20 pass at the audited tree: 2 show the product honouring a prior (held), and 18 show it contradicting one (trap). The project's suites hold none of them. The audit adopts nothing itself and re-runs the probes only at the next audit. A held probe is a candidate ordinary regression test. A trap probe is a candidate expected-fail test that starts failing when someone closes the trap. The decision on scenario tests fixes how such tests are written, not which probes are promoted. The ruling decides the adoption policy for this family, and one policy should cover the four sibling families.

## Options

- Adopt held probes as scenario tests and trap probes as expected-fail scenario tests, and retire each experiment directory as its test lands; cost: 20 maintained tests, each rewritten to the scenario harness.
- Adopt only the held probes, leave traps as audit instruments, and let the trap registry document them; cost: a closed trap shows up only at the next audit.
- Adopt none and keep all as audit instruments; cost: no regression signal between audits.

The ruling decides whether these probes become tests.

## Ruling

> Recommended ruling (/verify-issues): Adopt the held probes as ordinary scenario tests now, and leave the trap probes as audit instruments until the owner rules their trap issues. A trap the owner decides to close becomes a regression test alongside the fix, and a trap the owner accepts as documentation stays an instrument.
>
> Rationale: an expected-fail test that encodes accepted behaviour is unneeded in a suite the project keeps deterministic and lean, while a held probe is the kind of scenario the project writes. Tying trap adoption to the trap's own ruling keeps the suite from asserting behaviour nobody wants. Flip case: if the owner wants every measured trap to fail loudly the moment it changes, adopt all as expected-fail tests and accept the maintenance.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
