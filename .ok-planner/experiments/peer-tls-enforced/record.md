---
experiment: peer-tls-enforced
commit: d977250c
---

# The transport setting on a peer entry is enforced, both ways

## What it ran against

Three stacks from the tree's own image tag and one peer process. The peer is the
third-party peer built for permissive-peer-build, run plaintext in an alpine
container. The first stack declares two executor entries pointing at that same
plaintext peer — one with the transport setting off, one with it required — so
the setting is the only difference between them. The second stack declares a
claim producer at the same plaintext peer with the setting required. The third
stack runs under `peer_auth: mtls` with a second copy of the peer that enrolls
and serves mutually-authenticated TLS. Re-run unchanged at this tree.

## What was observed

Against one and the same plaintext peer, the entry with the setting off was
reported reachable at `tls: off`, and the entry with the setting required was
reported unreachable at `tls: required` with the failure naming both the peer and
the setting: `peer "plain-required (plainpeer:9400)" (tls: required): ...
authentication handshake failed: tls: first record does not look like a TLS
handshake`.

The same setting on the store side is louder still: a claim producer that cannot
present credentials stops the stack starting at all — the container exited 1 and
its log named the producer and the setting in the same shape.

The refusal reaches the work: a node dispatched at the off entry settled fresh
with a success terminal, and a node dispatched at the required entry — the same
peer, the same process — settled failed with `terminal/error/executor_dial_failed`.

Against a peer that can present credentials, the required setting is satisfied
rather than merely enforced: the stack reported it reachable at `tls: required`,
the certificate it presents verifies against the deployment CA (probed from the
host with a client leaf the enrollment route issued), and a node driven over that
connection settled fresh with the credentialed peer's own writeback.
