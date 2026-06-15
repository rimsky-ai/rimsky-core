---
story: api-key-management
status: as-is
---

# Operator administers api-key lifecycle

## Role

As an operator, I can bootstrap the first admin key on a fresh deployment, mint scoped keys with roles, list and inspect existing keys without seeing plaintext, revoke a key so it stops being accepted, rotate one (new plaintext now, old key kept usable through a grace window), and check the current auth status, so that I administer credentials end-to-end.

## Capability

Operator-driven api-key lifecycle: bootstrap, mint, list, revoke, rotate with grace, status.

## Business value

Operators administer credentials end-to-end — from the bootstrap of the first admin key on a fresh deployment through scoped mint, revoke, and rotate with a grace window — without ever exposing plaintext after the mint moment.

## Acceptance

Against a fresh deployment with no keys, the operator bootstraps an admin key through the bootstrap surface and receives plaintext exactly once. With the admin key, the operator mints scoped keys; subsequent metadata reads never expose plaintext. Revoking a key causes subsequent requests bearing that key to be refused. Rotating a key produces new plaintext and the old key keeps working through its grace window, then stops. The auth-status surface reports the current mode and active key count.

## Falsifier

Revoke leaves the old plaintext still accepted, OR the rotated key's grace window collapses to zero (old key dies immediately) or never expires, OR auth-init succeeds when the keys table is non-empty.

## Proof

Executable proof.
