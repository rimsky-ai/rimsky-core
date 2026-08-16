---
audit: auth-anonymous-via-empty-key-ledger
artifact: decision:auth-anonymous-via-empty-key-ledger
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:24:31Z
---

# The one-shot self-hosted run admits every request as anonymous admin because its fresh ledger is empty

Supported. The self-host run verb writes a synthetic configuration pointing at a brand-new SQLite file inside a per-run directory, binds the control API to loopback on a kernel-picked port, and then drives the whole run — register, deploy, create, poll — through a client it never gives an api key. On that stack the auth middleware counts active api keys, finds zero, and hands back the synthetic anonymous identity, whose grant is the unrestricted wildcard at execute mode; the count is cached briefly and invalidated on any auth mutation, so minting the first key ends anonymous mode immediately. Checked every key-minting site in the tree: the only two are the create-key and rotate-key request handlers, so no launcher, entrypoint, or role binary provisions a key at startup, which is what the decision claims. The self-host path is exercised end to end by scenario tests that boot the stack and drive a node to terminal with no credential.
