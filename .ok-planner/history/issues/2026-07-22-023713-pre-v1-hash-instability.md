---
issue: pre-v1-hash-instability
kind: human
category: unspecified
artifacts:
  - concept:template
status: promoted
sprint: 2026-07-25-issue-drain-2026-07-22-batch.md
opened: 2026-07-22T02:37:13Z
---

# A routine dependency bump could silently split every template's identity — and no promise says it can't

Every rimsky template (a registered, reusable workflow definition) gets a permanent identifier by canonicalizing its JSON into a fixed byte order and hashing it: `sha256-<hex>`. The canonicalization step is done by a third-party library pinned to one exact version. Nothing stops a routine dependency update from moving that pin — and if the library's output bytes ever change, every *newly computed* hash changes while every *already-persisted* template keeps its old id. Same spec, two identities: a live-data split, not a discardable dev artifact. Today the project's blanket pre-v1 stance ("no compatibility guarantees") technically covers this, but no document says what the commitment becomes at v1, and anyone building outside tooling that re-derives template hashes — a subscriber service, a diff tool — has nothing to bind to.

The sharp edges: the id format has no version marker (it names the hash algorithm, not the canonicalization scheme, so a scheme change can't be told apart from the original), no check guards the pin (stability rests on nobody touching a lockfile line), and no migration tooling exists to rehash persisted templates if the pin ever must move. The design corpus records the pin as a present-tense fact and templates as durable content-addressed identities; on the post-v1 question it is simply silent.

## Options

- **Freeze the pin permanently.** Never bump the canonicalization library, enforced by a check that fails if the pinned version string changes. Simple and cheap; permanently forecloses taking that library's own future bug fixes.
- **Version-prefix the id** (e.g. `sha256-jcs1-<hex>`) so a scheme change ships as a structurally new identity instead of a silent collision. Real forward agility — but it's itself a breaking format change that must land before v1 to be cheap, and every hash consumer pays parsing complexity forever.
- **Keep the format, commit to a migration procedure** (rehash-and-repoint, exercised only if the pin ever moves). No format churn now; defers hard design (what happens to instances bound to old hashes) to the worst possible moment, and the interim stays unguarded.
- **Freeze mechanically now, defer the rest.** Take the freeze plus its enforcement check today; decide versioned-format-vs-migration only if a concrete need to move the pin ever appears.

The ruling decides which commitment holds after v1 — freeze, versioned format, or migration path — whether the freeze is mechanically enforced or documentation-only, and whether any format change must land before v1.

## Ruling

> Recommended ruling (/recommend-rulings): Freeze now, mechanically: a
> check that fails when the pinned JCS canonicalization dependency
> version moves (asserting the go.mod pin), stated as the current
> commitment in concept:template. Defer the versioned-id-format vs
> migration-path question until a concrete need to move the pin
> arises.
>
> Rationale: Make-wrong-fail-mechanically is the house ethos; the
> check removes the silent-orphan risk (an accidental dependency bump)
> at near-zero cost, while committing to neither format churn nor
> migration tooling that may never be exercised.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
