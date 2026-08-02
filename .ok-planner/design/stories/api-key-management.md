---
story: api-key-management
---

# Operator administers api-key lifecycle

## Story

As an operator, I can bootstrap the first admin key on a fresh deployment, mint scoped keys with roles, list and inspect existing keys without seeing plaintext, revoke a key so it stops being accepted, rotate one (new plaintext now, old key kept usable through a grace window), and check the current auth status, so that I administer credentials end-to-end.
