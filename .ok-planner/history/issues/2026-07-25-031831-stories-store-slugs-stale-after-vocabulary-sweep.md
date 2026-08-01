---
issue: stories-store-slugs-stale-after-vocabulary-sweep
kind: audit
category: inconsistent
artifacts:
  - story:store-filesystem
  - story:store-postgres
status: repaired
opened: 2026-07-25T03:18:31Z
---

# Two stories still point users at service names that no longer exist

Question: should `story:store-filesystem` and `story:store-postgres` be renamed to track the shipped components' current names (`claim-producer-filesystem`, `claim-producer-postgres`), or keep their old slugs and only fix the prose?

Re-verification found the corpus already carries a live precedent settling this in one direction: `design/concepts/claim-producer.md` exists and no `claim-store` concept does — the concept catalog already renamed the same vocabulary shift, so a story slug standing pat while the concept catalog moved on is itself the inconsistency needing alignment, not a genuinely open policy question. The specific harm the issue raised — a story instructing a template to reference a since-renamed component literally named `store-filesystem` — has independently rotted: that language lived in the story's `Acceptance` section, which the 2026-07-31 suite converge stripped from every story file; the current merged `## Story` text names no broken component identifier. A corpus-wide grep found zero `@story:store-filesystem` / `@story:store-postgres` citations anywhere in code or design docs to sweep (the two hits in `design/intent/atomic-staging.md` and `design/intent/error-policy.md` are unrelated: they name a gRPC `ErrorInfo` domain string, `rimsky.store-postgres`, a wire-protocol identifier in a different namespace, out of this issue's scope). With no downstream citations at stake and the target identity already established by the concept catalog, the rename changes no commitment — it aligns a stale identifier to what the code and `concept:claim-producer` already agree on.

What changed: `design/stories/store-filesystem.md` → `design/stories/claim-producer-filesystem.md` (frontmatter `story:` field, title, and the two prose uses of "filesystem store" naming the component updated to "filesystem claim-producer"; the story's promise is unchanged). `design/stories/store-postgres.md` → `design/stories/claim-producer-postgres.md` (same treatment, "postgres store" → "postgres claim-producer"). `design/stories.md`'s auto-generated TOC had its two stale `store-*` lines removed and replaced with `claim-producer-filesystem` / `claim-producer-postgres` entries in their correct alphabetical position among the other `claim-producer-*` entries — the stale-TOC-line case the mechanical-repair rule names directly.

How verified: post-rename repo-wide grep for `store-filesystem` / `store-postgres` confirms zero remaining `@story:` or `story:` citations anywhere in code, design docs, or the TOC (the two unrelated `rimsky.store-postgres` gRPC-domain hits left untouched, out of scope); the two renamed files' frontmatter `story:` slug matches their new filename per the citation-resolution convention.
