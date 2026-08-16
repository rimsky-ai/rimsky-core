---
audit: untagged-prose-by-module
artifact: decision:untagged-prose-by-module
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T05:21:13Z
---

# Nothing at this commit carries the per-module sweep decomposition

Unsupported: the artifact's own text does not settle what would count as support at this commit, and nothing in the tree carries it. The decision decomposes a remediation program — untagged-prose comment-hygiene violations — into one sweep per top-level module root, but the backlog it decomposes is empty: the lint runs over the whole repo in the project's own test suite and a violation fails that test, there is no violation budget or ratchet file, and both lint checks are asserted active. No tool, test, configuration, or annotation splits violations by module root; the slug appears nowhere in the tree among the 624 decision annotations. The one part that is checkable holds — the module-layout concept names four top-level code groups, so the pass count the decision fixes is well defined at four — but that is arithmetic about a neighbouring concept, not evidence of this choice. The counter-evidence points the other way: the two comment-hygiene sweeps in the project's history each landed as a single change spanning every module root at once rather than as one pass per root, and the earlier of the two is the change that introduced this decision file.
