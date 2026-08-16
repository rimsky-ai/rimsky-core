---
assessment: claim-producer-postgres--verifier-check
subject: story:claim-producer-postgres
way: verifier-check
release: d977250c
outcome: held
warrant: experiment:claim-producer-postgres
---
# Checking a staged batch before it is allowed to land

A row-count-ratio check ran over the staged content from a co-holding node, against a baseline of ten, and passed for the batch that wrote ten rows. A second staged claim that wrote only two rows failed the same check: the checking node settled on the producer's own per-check error class, and a node subscribed to that producer's class namespace ran on the signal. The check is therefore both a gate on the batch and an event a template can react to, so an operator can wire a response to a bad batch rather than only reading about it afterwards.

## Unverified remainder

The per-check class fires and is subscribable, but it is not among the error classes this producer advertises, so an operator enumerating the producer's advertised classes will not find it there.
