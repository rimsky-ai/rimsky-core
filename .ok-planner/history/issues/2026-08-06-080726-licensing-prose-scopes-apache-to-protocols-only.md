---
issue: licensing-prose-scopes-apache-to-protocols-only
kind: human
category: licensing
artifacts:
  - decision:licensing-dual-apache-agpl
  - decision:licensing-enforced-by-license-lint
status: answered
opened: 2026-08-06T08:07:26Z
github: https://github.com/rimsky-ai/rimsky-core/issues/54
---

# Do `COPYRIGHT` and `COPYING.md` still scope the Apache layer to `lib/protocols` only, contradicting `licensing.yml`?

No — both filed claims have already rotted off the current tree.

1. **Apache scope.** `COPYRIGHT:5-11` now names the Apache layer as "the
   `lib/protocols/` module ... plus the `examples/` carve-out", matching
   `licensing.yml`'s `apache:` list (`lib/protocols/`, `examples/`) and the
   `SPDX-License-Identifier: Apache-2.0` headers every `examples/**/*.go`
   file carries. `COPYING.md`'s "one bright line" section states the same
   two-entry scope. Per `decision:licensing-dual-apache-agpl`'s Choice
   ("a permissive open-source license covers the protocols module and the
   examples module"), this is the corpus's settled commitment, and the
   prose already reflects it.
2. **Verification count.** `COPYING.md`'s licensing-boundary paragraph now
   describes four `tools/license-check` verifications (per-file header,
   Apache-import-boundary, path-existence, third-party-license-closure),
   matching the four `verify*` calls in `tools/license-check/main.go`
   (`verifyHeaders`, `verifyImports`, `verifyEntriesExist`,
   `verifyApacheClosure`) one-for-one.

Both gaps were closed by prose work between the cited v0.13.0 tag and the
current tree. No corpus or code change made.
