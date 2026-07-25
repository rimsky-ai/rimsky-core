---
issue: concept-rimsky-enumerates-cli-verbs
kind: audit
category: other
artifacts:
  - concept:rimsky
status: verified
opened: 2026-07-25T03:18:31Z
---

# The CLI's concept doc lists every command while claiming it doesn't

Rimsky's design corpus forbids concept documents from enumerating their own implementation instances — including CLI command names — because the inventory belongs to the code and its help output, and a doc that carries it must chase every added command forever. The concept describing the `rimsky` CLI organizes the tool into capability "surfaces" (the local dev loop, multi-environment compose, authentication, host-agent control) and then, per surface, lists nearly every subcommand: the authentication bullet names all seven of its verbs, the host-agent bullet all three. A disclaimer precedes the lists — the verbs are "illustrative… not an exhaustive or owned contract" — but a list that happens to name everything isn't made non-exhaustive by saying so; the disclaimer describes an intent the text doesn't practice.

The self-undermining part is the crux: the document *itself* declares "the CLI code and its operator-facing reference are authoritative for exact verbs and flags." By its own text, the lists are redundant. What the enumeration buys — and what trimming costs — is that a reader currently gets a fairly complete mental model of the CLI from this one file.

## Options

- **Trim every surface to at most one illustrative verb** — the surfaces stay (they're the durable model); the inventory defers to help/code.
- **Keep the disclaimer-qualified lists as a deliberate exception** — completeness on a small surface read as incidental, not ownership.
- **Partial trim** — cut the big reference-like lists (dev loop, authentication), leave the three-verb host-agent bullet alone.

The ruling decides which of the three, knowing the corpus's recent precedent is deferral ("membership owned by the code").

## Ruling

> Recommended ruling (/recommend-rulings): Trim every capability-
> surface bullet to its surface description plus at most one
> illustrative verb; the CLI's help and code own the verb inventory.
>
> Rationale: The concept's own disclaimer already cedes authority to
> the code — the lists are redundant by the file's own text, and the
> corpus's deferral posture (signal, transition-reason) is the
> established answer to exactly this shape.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
