---
audit: work-completed-emitted
artifact: story:work-completed-emitted
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095808-stale-recovery-duplicate-work-started-events
---

# Operator pairs every work-started event with a work-completed event

Unsupported. The terminal-application path pairs work-started and work-completed correctly for ordinary completion, error, in-place retry, and park — each directly tested as a singleton. But the executor-deadline liveness-recovery sweep resets a stalled dispatch's row to its identical pre-dispatch state rather than replacing it, and the next successful acquisition of that same row unconditionally appends a fresh work-started event with no check for a prior one against the same dispatch id — so a node-run recovered this way can accumulate two or more work-started events against one terminal work-completed. No test in the project's suites asserts a singleton count across this specific path, unlike the retry and park variants, which are.
