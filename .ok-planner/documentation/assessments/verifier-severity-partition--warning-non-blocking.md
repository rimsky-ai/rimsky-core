---
assessment: verifier-severity-partition--warning-non-blocking
subject: story:verifier-severity-partition
way: warning-non-blocking
release: d977250c
outcome: held
warrant: experiment:verifier-severity-partition
---
# A failing check labelled warning is recorded but does not block

The audit ran three templates differing only in the severity labels on their declared checks (`catalog:executor-attribute-keys/verifier-shape-checks: checks`). A failing warning-severity check did not block: the node settled fresh with no failed run, while still counting the failure and naming it with its kind and its severity. An author can therefore observe a quality issue without stopping the pipeline for it.

## Unverified remainder

One warning-severity check was exercised. The demonstration does not establish how many warnings accumulate on a node before an author loses sight of them.
