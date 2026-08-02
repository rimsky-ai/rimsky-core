---
audit: doc-accuracy-gates
artifact: decision:doc-accuracy-gates
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095804-rules-doc-jsonl-citation-ungated
---

# Enumerating documentation is gated against the code facts it enumerates

Unsupported for one of the decision's two named gate instances. The substitution-doc gate is sound, with no gap found between its documented source-kind list and the runtime resolver's actual dispatch set. The rules-citation gate is real and runs, but does not in fact keep the rules file honest: it currently carries a stale citation to a retired single-file issue-queue path, invisible to the gate's extension recognizer and absent from its curated dead-reference list — the exact silent-rot failure mode the decision's rationale says a mechanical diff is supposed to catch when it is introduced. One of the two named instances is not delivering its claimed outcome today.
