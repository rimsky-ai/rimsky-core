---
trap: conformance-exit-code-machine-readable
release: d977250c
---
# Evidence set — every conformance subcommand returns a distinct non-zero exit code for "failed a check" versus "could not reach the endpoint", and supports `--json` for per-scenario results.

Source of the prior: sibling-symmetry — `--json`, `--scenarios`, `--skip`, and `--warnings-as-errors` present in the flag set

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-conformance-exit-code-machine-readable)

# Reading a conformance run from CI

## What it ran against

The shipped `rimsky` CLI at this tree, a `rimsky-claim-producer-filesystem`
container as a conforming implementation, and a
`rimsky-executor-verifier-shape-checks` container as an implementation that
fails a check when asked to validate a role it does not support. The run asks
every conformance subcommand for JSON output, then compares exit codes across
four outcomes.

## What was observed

No conformance subcommand accepts `--json`. All eight — `executor`,
`claim-producer`, `publisher`, `validation`, `data-processing`,
`blob-backend`, `lifecycle-subscriber`, `probe` — answered `flag provided but
not defined: -json` and exited 2. None carries `--warnings-as-errors` either.
The per-check report is the printed `ok` / `FAIL` rows and a closing
`N/M checks failed` line, which CI must parse as text.

Three exit codes exist and one of them covers every runtime outcome. The
conforming producer exited 0. `rimsky conformance validation --role publisher`
against the verifier printed `FAIL PublisherHappy` and `1/2 checks failed`,
and exited 1. `rimsky conformance claim-producer` against a closed port
printed `connection refused` and exited 1. `rimsky conformance blob-backend`
against an unopenable root exited 1. A usage error, such as an unknown flag,
exits 2. A caller reading the status alone cannot tell a failed check from an
endpoint the run never reached; the message text is the only distinction.

Runnables: `src:.ok-planner/experiments/assumption-conformance-exit-code-machine-readable/` at the stamped commit.
