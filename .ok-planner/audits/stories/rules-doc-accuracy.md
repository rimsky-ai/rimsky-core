---
audit: rules-doc-accuracy
artifact: story:rules-doc-accuracy
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095804-rules-doc-jsonl-citation-ungated
---

# Contributor trusts rules citations

Unsupported. The rules-citation gate exists and correctly validates every path-shaped citation it currently recognizes in the rules file — all resolve to real files. But one citation, naming the issue queue's location, points to a path that no longer exists on disk (the project migrated to a per-file directory), and this stale citation is invisible to the gate because its file-extension recognizer does not cover that path's extension and the curated dead-reference list was never extended to catch it. A contributor trusting the rules as written today would look for a file that is not there.
