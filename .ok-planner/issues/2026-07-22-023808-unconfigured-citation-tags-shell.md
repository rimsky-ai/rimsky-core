---
issue: unconfigured-citation-tags-shell
kind: human
category: enforcement-gap
status: verified
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

> Recommended ruling (/recommend-rulings): Sweep the eleven
> @constraint:/@deliberate: sites now per the comment-hygiene rule —
> delete by default, converting only a site whose content maps
> squarely onto an existing concept/story/decision. File the quote-
> tracker tokenizer bug upstream against plumbline with the
> reproduction; no local shell-grep mitigation.
>
> Rationale: The comment-hygiene rule already determines site
> disposition (default action for unconfigured tags is delete).
> Plumbline is Fall Guy's own tool, so the upstream report is the real
> fix; a local guard would duplicate what the fixed tokenizer will
> enforce.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
