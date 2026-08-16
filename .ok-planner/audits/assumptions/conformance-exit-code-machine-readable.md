---
assumption: conformance-exit-code-machine-readable
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# every conformance subcommand returns a distinct non-zero exit code for "failed a check" versus "could not reach the endpoint", and supports `--json` for per-scenario results.

As service author wiring CI, I would take it that every conformance subcommand returns a distinct non-zero exit code for "failed a check" versus "could not reach the endpoint", and supports `--json` for per-scenario results.

## Source

sibling-symmetry — `--json`, `--scenarios`, `--skip`, and `--warnings-as-errors` present in the flag set

## What a run would observe

run a conformance subcommand against an unreachable endpoint and against a deliberately broken one, comparing exit codes and `--json` output.

## Measured

The experiment `assumption-conformance-exit-code-machine-readable` asked every
subcommand for JSON output and compared exit codes across four outcomes.
Neither half of the prior holds. No subcommand accepts `--json`: all eight
answered `flag provided but not defined: -json` and exited 2, and none carries
`--warnings-as-errors` either, so CI must parse the printed `ok` / `FAIL` rows
and the closing `N/M checks failed` line as text. Three exit codes exist and
one covers every runtime outcome: a conforming producer exited 0;
`rimsky conformance validation --role publisher` against the bundled verifier
printed `FAIL PublisherHappy` and `1/2 checks failed` and exited 1; the same
subcommand against a closed port printed `connection refused` and exited 1; and
`blob-backend` against an unopenable root exited 1. Only a usage error is
distinct, at 2. A CI job reading the status alone cannot tell "your
implementation failed a check" from "I never reached your endpoint".
