---
audit: peer-tls-enforced
artifact: story:peer-tls-enforced
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:15:00Z
---

# Requiring TLS on a peer entry is enforced on both sides, and satisfied by a peer that can present credentials

Supported. One plaintext peer process was consumed by two executor entries
differing only in the transport setting: the entry with it off was reported
reachable at that setting, and the entry with it required was reported
unreachable, the reported failure naming both the peer and the setting. The
refusal reaches the work — a node dispatched at the off entry settled fresh with
a success terminal, and a node dispatched at the required entry, the same peer
and the same process, settled failed with a dial-failure terminal. The store
side is louder: a claim producer that cannot present credentials stopped the
stack from starting at all, the container exiting non-zero with a log naming the
producer and the setting. Against a peer that can present credentials the
setting is satisfied rather than merely enforced: the stack reported it
reachable at required, the certificate it presents verified against the
deployment CA when probed from outside with a leaf the enrollment route issued,
and a node driven over that connection settled fresh carrying the credentialed
peer's own writeback.

## Compliance

The benefit clause is about the configuration surface rather than a user need — "so that the TLS config key means what it says" — and names that key; compliant text states what the operator gets, e.g. "so that I know traffic to that peer is authenticated, and I find out at once when it is not".
