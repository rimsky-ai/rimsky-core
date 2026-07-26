---
issue: unconfigured-citation-tags-shell
kind: human
category: enforcement-gap
status: promoted
sprint: 2026-07-25-issue-drain-2026-07-22-batch.md
opened: 2026-07-22T02:38:08Z
---

# The comment linter goes blind partway through shell scripts — and eleven banned tags prove it

Rimsky's codebase enforces a near-zero-comments rule: a source comment may exist only as a sanctioned machine directive (license header, lint suppression) or a citation linking code to the design documentation. A vendored linter called Plumbline checks the whole tree. Yet two example shell scripts carry eleven comments in a tag vocabulary (`@constraint:`, `@deliberate:`) that was deliberately retired from this codebase once already — and the linter reports clean. The tags themselves are a small cleanup; the interesting part is *why* they survived.

Investigation found a real bug, with a reproduction: the linter tracks character-by-character whether it's inside a quoted string (so a `#` in a string isn't mistaken for a comment), but its model of shell quoting is naive — ordinary quoting desyncs it partway through a long file, after which it believes it's inside a string forever and silently swallows every later comment, tagged or not. So this isn't "shell scripts unsupported"; shell is a registered grammar with a defect that can blind the linter to *any* quote-heavy script — including legitimate citations. The linter is vendored from a sibling project (also owned by this project's maintainer), so the root fix lives upstream, not in this repo. Meanwhile the eleven comments carry genuine rationale (why a check tolerates a timing window, why a request needs a header), so disposing of them needs per-site judgment: convert to a real citation where the content maps to a design-doc entry, delete where it doesn't — which is exactly what the standing comment rule already prescribes.

## Options

- **Sweep the eleven sites now** per the existing rule (delete by default, convert where a citation target exists). Settles the content; leaves the blind spot.
- **File the bug upstream** with the reproduction — the real fix, in a tool the same owner controls; lands on that project's timeline.
- **Add a local shell-only guard** (a narrow grep check) so this repo doesn't wait — at the cost of permanently duplicating what the fixed linter will do.

The ruling decides: sweep now or wait; upstream report, local guard, or accept the limitation; and whether any local guard changes what the project's "lint reports clean" promise means.

## Ruling

> Owner ruling (2026-07-25, live): fix plumbline upstream now rather
> than filing — EXECUTED. The shell tokenizer was rewritten
> (quote-context nesting through $(...) command substitution,
> single-quote/ANSI-C/backtick handling, heredocs, word-boundary #
> detection, arithmetic-expansion guard), a second bug found in the
> same pass was fixed (the CLI silently linted only the first of
> several path arguments), fixture tests were added for both, and
> plumbline v8.1.0 is vendored into this repo. The fixed linter now
> reveals seventeen shell-comment sites repo-wide (the eleven
> @constraint:/@deliberate: tags plus six prose comments in other
> example scripts). Remaining for the sprint: sweep all seventeen per
> the comment-hygiene rule — delete by default, converting only a
> site whose content maps squarely onto an existing
> concept/story/decision. No local shell-grep mitigation.
>
> Rationale: Plumbline is Fall Guy's own tool — fixing beat filing.
> The comment-hygiene rule already determines site disposition, so
> only the sweep needs sprint work.
