---
issue: license-lint-ignores-third-party-imports
kind: audit
category: decision-drift
artifacts:
  - decision:licensing-enforced-by-license-lint
status: verified
opened: 2026-08-02T09:58:13Z
---

# The license lint never looks at a third-party dependency's license

Rimsky is dual-licensed, and the split is load-bearing: some modules ship Apache-licensed and must stay free of copyleft contamination (`decision:licensing-dual-apache-agpl`). The decision guarding that boundary claims a build-step license check constrains Apache packages to import "only the standard library plus permissive-licensed and Apache-licensed dependencies" — and its own Rationale rejects plain import-path deny rules as "blind to the licenses of third-party dependencies, which are exactly where contamination enters unnoticed" (`decision:licensing-enforced-by-license-lint`).

The implementation is precisely the rejected shape. The checker's import loop skips every import that isn't an internal rimsky package before any classification happens (`code:tools/license-check/imports.go::verifyImports`), so it enforces internal import *direction* only; no tool anywhere in the repo — lint config, Makefile, either CI workflow — classifies an external module's license. An Apache-classified package importing a copyleft third-party module today passes the license lint clean.

The ruling decides whether third-party classification gets built or the decision is rewritten to own the narrower check — noting the rewrite must also fix the Alternatives section, since the amended Choice would equal the alternative the decision currently rejects.

## Options

- Build third-party license classification: a curated module→license allowlist (or a license-file scan) that the check consults, failing on copyleft-incompatible imports from Apache-licensed packages. Cost: genuine new tooling with maintenance burden — license detection is heuristic, transitive dependencies multiply the surface, and the allowlist needs tending as deps churn.
- Amend the decision to claim only internal import direction, recording third-party licensing as unowned or out of scope. Cost: the decision would then be the thing its own Rationale rejects, so the Rationale and Alternatives must be rewritten too — and the dual-license boundary is left guarded by nothing.

## Ruling

> Recommended ruling (/verify-issues): build the third-party check, in its simplest defensible form — a curated, committed module→license allowlist covering the Apache-surface modules' dependency closure, consulted by the existing checker, with an unknown module failing the build until someone classifies it. The decision stands as written.
>
> Rationale: the dual-license split is a legal posture, not a style preference, and it is currently unguarded on exactly the axis the decision's Rationale says matters most; the amend option resolves the prose contradiction by deleting the protection. A curated allowlist dodges the heuristic-scanning cost (fail-closed on unknowns, classify by hand) and the Apache surface's dependency closure is small enough to curate. Flip case: if the allowlist proves noisy enough that people rubber-stamp additions, replace it with a real license-metadata scanner and accept that tooling cost.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
