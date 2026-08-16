---
assessment: peer-tls-enforced--store-side-startup-refusal
subject: story:peer-tls-enforced
way: store-side-startup-refusal
release: d977250c
outcome: held
warrant: experiment:peer-tls-enforced
---
# The store side refuses to start rather than run unauthenticated

Where the peer is a store rather than an executor, the failure is louder still. A claim producer that cannot present credentials against an entry requiring them stopped the deployment from starting at all: the container exited non-zero with a log naming the producer and the setting. An operator therefore cannot end up with a running deployment quietly talking to an unauthenticated store — the requirement is enforced before any work is dispatched.

## Unverified remainder

One store-side peer was driven, at startup. The way does not establish what happens when a store peer that was reachable loses its ability to present credentials while the deployment is already running.
