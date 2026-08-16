---
assessment: clean-lint--comment-check-live
subject: story:clean-lint
way: comment-check-live
release: d977250c
outcome: held
warrant: experiment:clean-lint
---
# The comment check fires rather than sitting inert

A clean run proves nothing if the checks are switched off in practice, so the run put a throwaway tree carrying a stray comment in front of the same lint under the same configuration. The comment check reported it and the lint exited non-zero. The green verdict over the repository is therefore a statement about the tree rather than about a check that never fires.

## Unverified remainder

None: the passing run demonstrates the way as promised.
