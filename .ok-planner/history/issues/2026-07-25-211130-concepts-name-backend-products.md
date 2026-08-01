---
issue: concepts-name-backend-products
kind: audit
category: compliance
artifacts:
  - concept:advisory-lock
  - concept:persistence-database
status: repaired
opened: 2026-07-25T21:11:30Z
---

Question: should `concept:advisory-lock` and `concept:persistence-database`, whose invariants genuinely fork per backend, name Postgres and SQLite directly, or describe the fork in role language and leave the product identities to the decisions catalog?

Rule that determined the fix: `SELF-CONTAINMENT-RULE`'s decision exemption states plainly that "the artifact name in a concept would be implementation detail" — the general rule, not a judgment call, since the corpus already carries the product identities at their licensed altitude in `decision:persistence-dual-backend`, `decision:postgres-pgx-v5`, and `decision:sqlite-modernc-pure-go`, and `concept:blob-backend` already demonstrates the same behavioral fork described in role language ("distinguished by where bytes live") with zero loss of the load-bearing invariants. Substituting role language for product names changes no commitment: every per-backend behavioral fact in both files survives the rewrite verbatim, just renamed.

What changed: in `.ok-planner/design/concepts/advisory-lock.md`, replaced every "Postgres" with "the client-server backend" and every "SQLite" with "the embedded (file) backend" across What it is, Purpose, and all four Invariants bullets touching per-backend mechanics — no invariant content dropped or altered. In `.ok-planner/design/concepts/persistence-database.md`, the same substitution across What it is ("a client-server-backend adapter" / "an embedded-file-backend adapter"), the adapter-selector sentence (dropped the literal `"postgres"` / `"sqlite"` config-string quoting, itself a smaller instance of the same wire-identifier violation), Purpose, and the two Invariants bullets naming the driver isolation rule and the safe-multi-process claim.

How verified: `grep -in "postgres\|sqlite" .ok-planner/design/concepts/advisory-lock.md .ok-planner/design/concepts/persistence-database.md` returns no matches in either file; the `concepts.md` TOC lines for both slugs were already product-neutral and needed no change. Markdown-only change, no build/test impact.
