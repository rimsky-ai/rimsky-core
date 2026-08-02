---
story: claim-producer-filesystem
---

# Operator uses filesystem-backed claim-producer

## Story

As an operator wiring a workflow whose claims persist on a POSIX filesystem, I can use the bundled filesystem claim-producer to acquire directory-per-scope claims with synchronous in-place write semantics and partition fan-out work from the store's own contents, so that I get production-grade claim semantics on plain files without standing up a database.
