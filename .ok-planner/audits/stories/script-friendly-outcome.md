---
audit: script-friendly-outcome
artifact: story:script-friendly-outcome
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:22Z
---

# Distinct process exit codes for the run's outcome class

Supported. The one-shot drivers (`compose run` and self-hosted `rimsky run`) share a single shutdown-reason mechanism that maps exactly three run-outcome classes to distinct exit codes — 0 for all-instances-succeeded, 1 for any-instance-failed, 2 for the wall-clock timeout — plus the conventional 130 for an external interrupt signal, which is a process-level event rather than a run-outcome class. A compiled-binary end-to-end test exercises all three outcome-class exit codes directly against `rimsky compose run`: a success manifest exits 0, a manifest with an unresolvable executor exits 1, and a manifest with an unbounded node under a 1s `--timeout` exits 2 — none of it requiring the caller to parse stdout/stderr.
