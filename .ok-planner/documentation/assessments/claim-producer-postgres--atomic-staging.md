---
assessment: claim-producer-postgres--atomic-staging
subject: story:claim-producer-postgres
way: atomic-staging
release: d977250c
outcome: held
warrant: experiment:claim-producer-postgres
---
# A batch becomes visible whole, or not at all

The second producer was configured for staged-async writes. A claim on the canonical schema resolved to a staging address distinct from it, and the claim handle recorded staged semantics rather than quietly downgrading to synchronous — the advertised behaviour is the realized one. The node wrote ten rows into the staging area. After commit, the canonical schema held those ten rows in place of the single row it had started with, and the staging area no longer existed: the commit swapped the content in rather than copying it row by row. A reader of the canonical schema therefore sees the old batch or the new one, never a half-written mixture.

## Unverified remainder

None: the passing run demonstrates the way as promised.
