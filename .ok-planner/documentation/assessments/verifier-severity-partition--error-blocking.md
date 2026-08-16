---
assessment: verifier-severity-partition--error-blocking
subject: story:verifier-severity-partition
way: error-blocking
release: d977250c
outcome: held
warrant: experiment:verifier-severity-partition
---
# Relabelling the same check error makes it block the commit

Relabelling that same check error, over the same rows and with nothing else changed, flipped the outcome to one failed run, no fresh run, and a terminal naming the escalated check — so the label is what decides, not the data. With rows tripping both, the blocking terminal named the error-severity check rather than the warning-severity one and carried the non-blocking warning beside the blocking failure in the same record, which is the partition the story asks for.

## Unverified remainder

None: the passing run demonstrates the way as promised.
