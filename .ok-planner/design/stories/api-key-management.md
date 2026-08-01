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

