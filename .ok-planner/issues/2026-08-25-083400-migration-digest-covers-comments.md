---
issue: migration-digest-covers-comments
kind: audit
category: migrations
artifacts:
  - decision:migrations-append-only-numbered
  - decision:design-link-annotations
status: verified
opened: 2026-08-25T08:34:00Z
---

# A comment edit in an applied migration stops the next boot

The migration runner hashes each migration file whole, comment lines included, and refuses to start a database whose recorded hash for an applied file no longer matches. A comment edit therefore stops the next boot even though the SQL that ran is unchanged. The decision that governs migrations, `decision:migrations-append-only-numbered`, records a digest "of each applied file's contents" and gives one reason: two databases must never run different SQL under one filename. A comment carries no SQL, so the guard refuses more than its reason requires.

The edits that reach applied migrations are citation lines. Sixty-one of the seventy-seven migration files carry a `-- @concept:`, `-- @story:`, or `-- @decision:` line naming a design artifact, under `decision:design-link-annotations`. When the project renames an artifact, every citing site is repointed, and a migration is a citing site. This sprint renamed the host-daemon proxy concept; migration 038 cites it; the repoint changed the file's hash; every database that already applied 038 now runs one manual step before its next boot: set that row's recorded digest to NULL, which the runner backfills on start. The same step recurs at every later rename of any artifact a migration cites.

Nothing catches the edit before that boot. The plumbline lint has no comment grammar for `.sql` files, so it neither checks a migration's comments for hygiene nor recognizes `-- @concept:` as a citation; a dangling slug in a migration is invisible to it. The runner compares digests only at startup.

## Options

- Keep the whole-file digest. Cost: every future rename that reaches an applied migration costs one manual per-database step, forever, with no check that catches the edit earlier.
- Digest the SQL alone: hash the file with comment lines and blank lines removed, so a comment edit changes nothing the runner compares. Cost: one global re-digest at rollout, because the hash of every applied file changes shape; each existing database takes one documented step (null every recorded digest, then boot) once, and the problem is gone. `decision:migrations-no-compat-shims` rules out carrying both hash shapes side by side, so the one-time step is the only honest form.
- Exclude migration files from citations. Cost: the sixty-one existing citations go, and a reader tracing a schema change to the concept or decision behind it loses the link.
- Catch the edit where it is cheap: a check at commit time that refuses a change to a migration already released. Cost: the check needs to know which files a release has already applied, which is a released-hash snapshot in CI rather than a static lint; it prevents the boot-time refusal but leaves the whole-file hash in place, so it complements the first two options rather than replacing them.
- Let the runner tell a comment change from a SQL change at refusal time and re-record the hash silently when only comments moved. Cost: the guard becomes a parser rather than a hash comparison, and the mechanical promise the decision's rationale prizes rests on that parser being right.

The ruling decides what an applied migration's recorded digest covers.

## Ruling

> Recommended ruling (/verify-issues): digest the SQL alone. The runner hashes each migration with its comment lines and blank lines removed, so a citation repoint in an applied file changes nothing the runner compares, and migrations keep their citations. The rollout pays the one-time cost the pre-v1 rule licenses: every existing database nulls its recorded digests once and the next boot backfills them, stated in the change the way the rules require.
>
> Rationale: the decision's own reason for the digest is that two databases must not run different SQL under one filename, and hashing the SQL alone is that reason made exact, where the whole-file hash refuses edits the reason never named. Keeping the whole-file hash taxes every future rename forever; excluding migrations from citations spends sixty-one links to avoid that tax; a parser at the refusal point trades a hash comparison for a judgment the guard was built to avoid. A released-hash check at commit time is worth adding beside this ruling, since it catches a real SQL edit to an applied file before any boot, but it does not decide what the digest covers. Flip case: if a database holding data worth keeping exists before this lands, the one-time global re-digest is no longer free, and keeping the whole-file digest with a commit-time check becomes the better trade.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
