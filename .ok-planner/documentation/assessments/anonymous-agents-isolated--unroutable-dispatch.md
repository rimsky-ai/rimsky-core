---
assessment: anonymous-agents-isolated--unroutable-dispatch
subject: story:anonymous-agents-isolated
way: unroutable-dispatch
release: d977250c
outcome: held
warrant: experiment:anonymous-agents-isolated
---
# A dispatch aimed at an agent nobody is running fails rather than landing on somebody else

A third instance was created naming an agent that no one on the machine was running. It settled failed with no writeback, and the execution counts on both connected agents stayed at one apiece. An unroutable dispatch is therefore not absorbed by whichever agent happens to be connected; a developer's work does not silently execute in a colleague's agent because a target name was wrong.

## Unverified remainder

None: the passing run demonstrates the way as promised.
