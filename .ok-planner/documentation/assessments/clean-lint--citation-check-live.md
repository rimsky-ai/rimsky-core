---
assessment: clean-lint--citation-check-live
subject: story:clean-lint
way: citation-check-live
release: d977250c
outcome: held
warrant: experiment:clean-lint
---
# The citation check fires, and discriminates

The same treatment was applied to the citation check, in both directions. A throwaway tree carrying a citation that resolves to nothing was reported and the lint exited non-zero; a third tree carrying a citation that does resolve was accepted and exited clean. The check therefore discriminates rather than always failing or always passing, so a clean citation verdict over the repository means the citations resolve.

## Unverified remainder

None: the passing run demonstrates the way as promised.
