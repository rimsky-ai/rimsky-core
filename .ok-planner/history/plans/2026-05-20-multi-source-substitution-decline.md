# Multi-source attribute substitution: decline — Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-20-multi-source-substitution-decline-design.md`
**Goal:** Decline the multi-source attribute substitution proposal: lock the load-bearing per-field-arity rationale into the design docs, archive the sketch, and record the change in CHANGELOG.
**Architecture:** Pure documentation change. Two concept-file edits under `.ok-planner/design/concepts/`, one sketch file move, one CHANGELOG bullet. No source code is touched.
**Tech Stack:** Markdown only.

---

## Context for the implementer

You are picking up a finished design conversation. The user considered, and **declined**, a proposal to lift the per-field attribute-substitution grammar from single-source (`source: "{{X}}"`) to multi-source (`source: ["{{A}}", "{{B}}"]`). The rationale, summarized:

1. A first-non-missing fallback semantic loses signal, which runs against the cascade's "no dropped signals" discipline.
2. An array-as-value semantic collapses to today's 1:1 schema with optional fields plus auto-subscribe — already supported, no grammar change needed.
3. The arity asymmetry between many-to-many subscriptions and per-field 1:1 substitution is intentional: subscriptions sum signals, substitution names values.

The full reasoning is in the spec (`.ok-planner/specs/2026-05-20-multi-source-substitution-decline-design.md`). Read it before starting. The spec's `## Design changes` section is the source of truth for what to mutate; this plan turns those mutations into mechanical edit tasks with exact text and exact insertion points.

**You are NOT writing any Go code.** No `go build`, no test runs, no lint. The verification at the end is grep-based file content checks plus a directory-state sanity check.

**Convention notes derived from the live files:**
- `.ok-planner/design/concepts/*.md` Notes sections use **chronological-ascending** order (oldest entry first, newest at the bottom). Appending to a Notes section means adding at the bottom of the bulleted list, after the last existing entry.
- `CHANGELOG.md`'s `## Unreleased` section uses **newest-at-top** order. Existing top-level bullets are unrelated changes; new top-level bullets go immediately after the `## Unreleased` heading line, before the existing first top-level bullet.
- Concept files start with a YAML frontmatter block delimited by `---` lines. Do not modify the frontmatter.

**Working directory:** The rimsky submodule root, `/Users/patrick/Documents/projects/research/zonebase/submodules/rimsky/`. All paths in this plan are relative to that root.

---

## Task 1: Append per-field-arity Invariant to `concepts/attribute.md`

**Files:** `.ok-planner/design/concepts/attribute.md`

### Step 1.1 — Verify current state

Run `grep -n 'Errors omit value bytes' .ok-planner/design/concepts/attribute.md`. Expect exactly one match. The line will look like this (line number ≈ 30):

```
- Errors omit value bytes (cite path tokens only) to preserve `@blessed-invariant 20`/`21`.
```

This is the last bullet of the existing `## Invariants` list and the anchor for the new bullet. The blank line immediately following it separates the Invariants list from the `## Aliases and historical names` heading.

### Step 1.2 — Append the new Invariant bullet

Use Edit to insert one new bullet **immediately after** the "Errors omit value bytes ..." bullet (keeping the existing blank line and `## Aliases and historical names` heading intact below). The new bullet text (exact, verbatim from the spec's `## Design changes` section):

```
- Per-field `source:` arity is 1 — each attribute property declares exactly one substitution directive. Many-to-many fan-in across upstreams lives in the cascade vocabulary (subscriptions over multiple senders, plus optional schema fields whose dispatch-time `ErrMissingSource` is silently omitted at `code:runtime/runner_dispatch.go::substituteAttributesSchema`). Enforced at registration by `code:graph/node/template_validator.go::checkAttributeSource` (rejects any `source:` that isn't exactly one `{{...}}` directive with no surrounding text). The arity asymmetry between subscriptions (many-to-many) and substitution (per-field 1:1) is intentional: subscriptions sum signals across upstreams; substitution names a single value per field.
```

Recommended Edit shape: match the existing "Errors omit value bytes" bullet plus the blank line and `## Aliases and historical names` heading that follow, and replace with the existing bullet, the new bullet, then the same blank line and heading. The new bullet is a single Markdown line (no internal newlines).

### Step 1.3 — Verify

Run:

```
grep -nF 'Per-field `source:` arity is 1' .ok-planner/design/concepts/attribute.md
```

Expect exactly one match. The matched line number should be exactly one greater than the line number returned by Step 1.1's grep (the new bullet immediately follows the anchor).

Also re-run `grep -n 'Errors omit value bytes' .ok-planner/design/concepts/attribute.md` and confirm the original bullet is still present, unchanged.

---

## Task 2: Append Boundaries arity clarification to `concepts/attribute.md`

**Files:** `.ok-planner/design/concepts/attribute.md`

### Step 2.1 — Verify current state

Run `grep -n 'Clarifying note (per 2026-05-15' .ok-planner/design/concepts/attribute.md`. Expect exactly one match. The matched line is the existing closing paragraph of the `## Boundaries` section, ending with `... Don't conflate.` A blank line follows, then the `## Invariants` heading.

### Step 2.2 — Append the new Boundaries paragraph

Use Edit to insert a new paragraph **after** the "Clarifying note (per 2026-05-15 ...)" paragraph and **before** the blank line that precedes `## Invariants`. Format: one blank line, then the new paragraph as a single Markdown line.

The new paragraph text (exact, verbatim from the spec):

```
Clarifying note on arity: per-field substitution is 1:1 by design — one `source:` directive names one value. Multi-upstream fan-in is the cascade vocabulary's job, expressed through `concept:node-subscription` (N upstreams per receiver) and optional schema fields (the dispatch path omits non-required fields on `ErrMissingSource`). The arity asymmetry is load-bearing — see the per-field-arity invariant.
```

Recommended Edit shape: find the existing "Don't conflate." sentence (unique in the file — verify with `grep -n "Don't conflate" .ok-planner/design/concepts/attribute.md` returning one match) plus the blank line and `## Invariants` heading that follow it, and replace with the existing closing-sentence-of-paragraph, blank line, new paragraph, blank line, `## Invariants` heading.

### Step 2.3 — Verify

Run:

```
grep -nF 'Clarifying note on arity' .ok-planner/design/concepts/attribute.md
```

Expect exactly one match. Run:

```
grep -nF "Don't conflate" .ok-planner/design/concepts/attribute.md
```

Expect exactly one match. Confirm the "Clarifying note on arity" line number is greater than the "Don't conflate" line number (the new paragraph follows the existing one).

---

## Task 3: Append Notes entry to `concepts/attribute.md`

**Files:** `.ok-planner/design/concepts/attribute.md`

### Step 3.1 — Verify current state

Read the bottom of `.ok-planner/design/concepts/attribute.md` (last 20 lines is enough). Confirm the file ends with:

- A `## Notes` heading,
- A blank line,
- Two existing dated bullets (both 2026-05-19), each one Markdown line plus their following blank lines.

The chronologically last existing Notes bullet starts:

```
- 2026-05-19 — Embedded-mode `Substitute` (the string-returning entry point) now JSON-encodes ...
```

It is the bottom of the Notes list. The new bullet goes immediately after it (newer dates go at the bottom — chronological-ascending).

### Step 3.2 — Append the new Notes bullet

Use Edit to insert one new bullet at the bottom of the `## Notes` section, immediately after the existing "2026-05-19 — Embedded-mode `Substitute` ..." bullet. The new bullet text (exact, verbatim from the spec):

```
- 2026-05-20 — Multi-source attribute substitution proposal declined. Sketch archived to `.ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md`; the per-field-arity invariant and Boundaries clarification above were added by this spec. Rationale: a first-non-missing fallback semantic loses signal (subscriptions fire on each upstream transition, but substitution would collapse to one candidate); an array-as-value semantic collapses to today's 1:1 schema with optional fields plus auto-subscribe; the read-vs-cascade arity split is the load-bearing distinction. See `.ok-planner/history/specs/2026-05-20-multi-source-substitution-decline-design.md` for the full reasoning trail.
```

The bullet ends the file. Preserve a trailing newline at end of file (standard POSIX text-file convention).

### Step 3.3 — Verify

Run:

```
grep -nF '2026-05-20 — Multi-source attribute substitution proposal declined' .ok-planner/design/concepts/attribute.md
```

Expect exactly one match. Also confirm the file still ends with this new bullet (the new entry is at the bottom, not the top of Notes):

```
tail -3 .ok-planner/design/concepts/attribute.md
```

Should show the new 2026-05-20 bullet.

---

## Task 4: Append Notes entry to `concepts/node-subscription.md`

**Files:** `.ok-planner/design/concepts/node-subscription.md`

### Step 4.1 — Verify current state

Read the bottom of `.ok-planner/design/concepts/node-subscription.md`. Confirm the file ends with:

- A `## Notes` heading,
- Three existing dated bullets (2026-05-14, 2026-05-15, 2026-05-17) in chronological-ascending order.

The chronologically last existing Notes bullet starts:

```
- 2026-05-17: renamed `concept:subscription` → `concept:node-subscription` ...
```

### Step 4.2 — Append the new Notes bullet

Use Edit to insert one new bullet at the bottom of the `## Notes` section, immediately after the existing "2026-05-17: renamed ..." bullet. The new bullet text (exact, verbatim from the spec):

```
- 2026-05-20 — The arity split between node-subscriptions (many-to-many over upstreams) and per-field attribute substitution (1:1) is load-bearing, not an inconsistency. Subscriptions sum signals; per-field `source:` names a single value. See `concepts/attribute.md` (per-field-arity invariant + Boundaries clarification) for the rationale; companion to the declined multi-source-substitution sketch (`.ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md`).
```

Preserve a trailing newline at end of file.

### Step 4.3 — Verify

Run:

```
grep -nF '2026-05-20 — The arity split between node-subscriptions' .ok-planner/design/concepts/node-subscription.md
```

Expect exactly one match.

```
tail -3 .ok-planner/design/concepts/node-subscription.md
```

Should show the new 2026-05-20 bullet at the bottom.

---

## Task 5: Archive the sketch via `git mv`

**Files moved:**
- From: `.ok-planner/sketches/2026-05-19-multi-source-attribute-substitution.md`
- To: `.ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md`

### Step 5.1 — Verify preconditions

```
ls .ok-planner/sketches/2026-05-19-multi-source-attribute-substitution.md
ls -d .ok-planner/history/sketches/
ls .ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md 2>&1
```

Expect the source path to exist, the destination directory to exist, the destination path to **not** exist (the third `ls` should fail with "No such file or directory").

### Step 5.2 — Run `git mv`

Move the file unchanged using git, so the move is tracked as a rename (matches precedent commit `cce2f1d`):

```
git mv .ok-planner/sketches/2026-05-19-multi-source-attribute-substitution.md .ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md
```

Do **not** edit the sketch file before or after the move. The rationale lives on the concept Notes entries (Tasks 3 and 4) and in the CHANGELOG bullet (Task 6); the moved sketch is preserved verbatim.

### Step 5.3 — Verify

```
ls .ok-planner/sketches/2026-05-19-multi-source-attribute-substitution.md 2>&1
ls .ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md
git status --short .ok-planner/sketches/ .ok-planner/history/sketches/
```

Expect the source path to no longer exist (first `ls` fails), the destination path to exist (second `ls` succeeds), and `git status --short` to show one `R` (rename) entry covering the two paths.

---

## Task 6: Append CHANGELOG bullet under `## Unreleased`

**Files:** `CHANGELOG.md`

### Step 6.1 — Verify current state

```
grep -n '^## Unreleased$' CHANGELOG.md
```

Expect exactly one match at line 3. Read lines 1–6 of `CHANGELOG.md` to confirm the structure:

```
# Changelog

## Unreleased

- **Multi-instance template ergonomics — post-review fixes.**
  ...
```

The current top-level bullet "Multi-instance template ergonomics — post-review fixes." is the first item under `## Unreleased`. New top-level bullets go **above** it (newest-at-top, matching the file's convention — see the existing pair where "post-review fixes" sits above "five quality-of-life items + design-doc updates").

### Step 6.2 — Insert the new top-level bullet at the top of `## Unreleased`

Use Edit to insert the new top-level bullet **immediately after** the `## Unreleased` heading line and its trailing blank line, and **before** the existing "- **Multi-instance template ergonomics — post-review fixes.**" line. After the new bullet, insert one blank line, then the existing top-level bullet continues.

The new bullet text (exact, verbatim from the spec):

```
- **Multi-source attribute substitution proposal declined.** `concept:attribute` gains a per-field-arity invariant ("`source:` arity is 1 — one substitution directive per field") and a Boundaries clarification spelling out the read-vs-cascade arity split. `concept:node-subscription` gains a companion Notes cross-reference. Sketch (`.ok-planner/sketches/2026-05-19-multi-source-attribute-substitution.md`) archived to `.ok-planner/history/sketches/`. Rationale: a first-non-missing fallback semantic loses signal (subscriptions fire on each upstream transition, but substitution would collapse to one candidate); an array-as-value semantic collapses to today's 1:1 schema with optional fields plus auto-subscribe (`code:runtime/runner_dispatch.go::substituteAttributesSchema` already omits non-required fields on `ErrMissingSource`); the arity asymmetry between subscriptions (many-to-many) and per-field substitution (1:1) is intentional — subscriptions sum signals, substitution names values. See `.ok-planner/history/specs/2026-05-20-multi-source-substitution-decline-design.md`.
```

Recommended Edit shape: find the unique three-line sequence

```
## Unreleased

- **Multi-instance template ergonomics — post-review fixes.**
```

and replace with

```
## Unreleased

- **Multi-source attribute substitution proposal declined.** ...(full bullet text above)...

- **Multi-instance template ergonomics — post-review fixes.**
```

(The new bullet is a single Markdown line — no soft-wrap. The blank line between the new bullet and the next top-level bullet matches the convention used between the existing two top-level bullets.)

### Step 6.3 — Verify

```
grep -nF 'Multi-source attribute substitution proposal declined' CHANGELOG.md
```

Expect exactly one match. Run:

```
grep -n '^- \*\*Multi' CHANGELOG.md | head -3
```

Expect the first match to be the new "Multi-source attribute substitution proposal declined" bullet, the second to be "Multi-instance template ergonomics — post-review fixes.", confirming the new bullet sits above the existing top-level bullet (newest-at-top order).

---

## Task 7: Final whole-plan verification

**Files:** none modified.

### Step 7.1 — Full edit-state sanity check

Run the following commands and confirm the output matches expectations:

```
grep -cF 'Per-field `source:` arity is 1' .ok-planner/design/concepts/attribute.md
```

Expect `1`.

```
grep -cF 'Clarifying note on arity' .ok-planner/design/concepts/attribute.md
```

Expect `1`.

```
grep -cF '2026-05-20 — Multi-source attribute substitution proposal declined' .ok-planner/design/concepts/attribute.md
```

Expect `1`.

```
grep -cF '2026-05-20 — The arity split between node-subscriptions' .ok-planner/design/concepts/node-subscription.md
```

Expect `1`.

```
grep -cF 'Multi-source attribute substitution proposal declined' CHANGELOG.md
```

Expect `1`.

```
ls .ok-planner/sketches/2026-05-19-multi-source-attribute-substitution.md 2>&1 | grep -c 'No such file'
```

Expect `1` (source path is gone).

```
ls .ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md 2>&1 | grep -c 'No such file'
```

Expect `0` (destination path exists).

### Step 7.2 — Confirm no stray code edits

```
git diff --name-only HEAD | sort
```

Expect exactly these paths (in any order; modified plus renamed):

```
.ok-planner/design/concepts/attribute.md
.ok-planner/design/concepts/node-subscription.md
CHANGELOG.md
```

The git rename (sketch → history/sketches) appears separately in `git status --short` as a single `R` entry (verified in Task 5.3). Other tracked files should be untouched. Untracked files (other items in `.ok-planner/specs/`, `.ok-planner/plans/`, etc.) are unrelated and may be present.

### Step 7.3 — Confirm no Go files were touched

```
git status --short | grep -E '\.go$' | wc -l
```

Expect `0`. This plan does not touch Go source — if any `.go` file shows up here, something went wrong.

---

## Manual checks after completion

None. This is a documentation-only change. All verification is grep-based and runs in the implementer's session.

The user will commit the working tree when they are ready. They may choose to write the commit message referencing the spec (per the convention of the precedent decline commit `cce2f1d`); that is the user's call, not the implementer's.
