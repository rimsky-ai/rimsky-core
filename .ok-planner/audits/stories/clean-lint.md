---
audit: clean-lint
artifact: story:clean-lint
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# The whole tree lint-clean with no check switched off

Supported. The repository vendors the lint binary a maintainer runs, so the
verification needs nothing installed beyond a node interpreter. Run over the
repository root under the project's own configuration, it exited 0 with no
output. That configuration switches off neither of the 2 checks the lint
carries, and declares 5 citation tags. Both checks were shown live under that
same configuration rather than inert: a throwaway tree carrying a stray comment
was reported by comment-hygiene, a tree carrying an unresolvable citation was
reported by citation-resolution, and a tree carrying a citation that resolves
exited 0, so the citation check discriminates rather than always failing.

## Compliance

Two defects. The benefit clause makes a design artifact's accuracy the
maintainer's reason for acting, where a story owes what the user accomplishes.
The body also names the third-party methodology whose tool does the enforcing,
which is the decision's territory. Compliant text: "As a rimsky maintainer, I
can check the whole tree against every coding convention the project mechanises,
in one run, so that I know a convention has not quietly stopped being enforced."
