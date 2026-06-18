# Tag-honesty sweep: drive `@deliberate:` / `@constraint:` tags to genuine load-bearing whys — Design Sketch

**Date:** 2026-06-14
**Status:** Sketch (not a spec; not authorization to build)
**Origin:** residual from `plan:2026-06-13-plumbline-comment-hygiene-sweep` (see its completion report's "Decisions diverged" section).

## Idea

The 2026-06-13 comment-hygiene sweep drove plumbline's `comment_hygiene` check from ~6,810 violations to zero, flipped the lint to all-three-checks-on, and shipped an executable proof. **The lint converged; the tag-honesty discipline did not.** A four-cycle review-cleanup loop on the working tree surfaced — and the final loop documented as carry-over — a class of sites where a structured tag (`@deliberate:`, `@constraint:`) was applied to what is essentially **narration of the next statement**: a test-step label, an assertion paraphrase, an algorithm-stage caption. The lint passes (the first word is a recognized tag), but per `decision:comment-hygiene-uniform-rule` and the Plumbline cheatsheet's "delete as residue" prescription, these tags are dishonest: they don't mark a load-bearing why a future reader would otherwise lose; they paraphrase the line below.

The harm is not correctness — no runtime bug, no security exposure, no failed test. The harm is **signal dilution**. Once `@deliberate:` is a fig-leaf above narration as often as it is above genuine subtlety, the next agent or contributor reading rimsky can no longer trust that a tag flags something the code can't say for itself. The whole point of structured tags — comprehension is cheap, verification is not — fails if the tags are noise.

Drive every `@deliberate:` and `@constraint:` site in the working tree to one of three honest end-states per the uniform rule: **(a) genuine load-bearing why → keep with prose that names the property protected; (b) redundant with the code below → delete; (c) narrator of an obvious step → delete.** Lift the result into a lint rule the project can carry forward.

## Shape

### Today (what we're replacing)

Plumbline's `comment_hygiene` check verifies that every prose comment begins with a structured tag — it cannot judge whether the tag is **honest**. The current corpus carries:

- **~60 `// @deliberate: <Capitalized one-liner>.` sites** across `lib/graph/`, `lib/runtime/`, `lib/control/controlapi/`, `cmd/rimsky-host-agent-proxy/`, and many `test/scenarios/` files. Pattern: a single-line tag above a single statement that the comment paraphrases ("`// @deliberate: Both frames must have ended.`" above an assertion checking exactly that; "`// @deliberate: Dispatch row exists for the target.`" above a row-existence check).
- **~11 `// @constraint:` test-step labels** in `lib/control/controlapi/nodes_test.go` and similar test files. Pattern: a tag prefixing a step-label ("case 1:", "case 2:", "snapshot the frame count so the assertion below…"), some encoding real why-content the test depends on, most pure narration of test progression.
- **An unknown long-tail elsewhere** — these counts are from greps the 2026-06-13 sweep's reviewer surfaced; a fresh enumeration is a phase of this work.

### Proposed

A spec whose `## Manifest` carries one user-outcome story:

**STORY-honest-tags** — As a rimsky maintainer / agent reading the code, I can trust that every `@deliberate:` and `@constraint:` site flags a non-obvious why a comment-less reader would otherwise lose, because the tag is reserved for that purpose and narrator-style tags have been swept out.
- **Acceptance**: the maintainer runs the project's tag-honesty lint against the post-work tree; the lint reports zero narrator-style tags.
- **Falsifier**: the maintainer can find one `// @<tag>: <Capitalized one-line paraphrase of the next statement>.` site that the sweep left in place.
- **Proof**: executable — the tag-honesty lint binary runs against the tree and exits clean.

And four technical decisions:

**TD-narrator-tags-deleted** — narrator-style tags (single-line tag whose body paraphrases the immediately-following statement) are deleted as residue. The cheatsheet's existing "delete as residue" rule applied uniformly; no per-test exception.

**TD-three-action-rule-per-site** — every existing tagged site is resolved into one of three end-states: **keep** (with prose that names the property protected, not just labels the next line), **rewrite** (when the comment encodes a real why but the wording is currently a label — reword as a why), or **delete** (when removing the comment leaves no comprehension loss). No fourth bucket. No "borderline keep just to be safe."

**TD-iota-block-tags-stay** — `iota`-block enum-value comments above bare-identifier const declarations keep their `@deliberate:` / `@constraint:` tags. Plumbline's GoDoc detection does not extend to bare-identifier const-block fields (only to typed declarations like `RefValidateAll RefValidationMode = iota`); the tag is the honest form. The 2026-06-13 sweep validated this empirically (4 candidates considered; only 1 — a typed struct field — successfully stripped to GoDoc).

**TD-mechanical-falsifier** — the spec's mechanical falsifier is a new lint check (or a plumbline extension) that detects narrator-style tags. Candidate signal: a tagged comment whose body, after the tag, satisfies all of (i) one line, (ii) capitalized first word, (iii) ends with `.`, AND (iv) the next non-comment line is a single statement (an assertion / a one-line assignment / a one-line method call) whose tokens substantially overlap the comment body. Tuning the predicate to surface narrators without false-positives on genuine short-why tags is part of the work.

## Why the 2026-06-13 sweep didn't finish this

The 2026-06-13 plan's per-module passes had a per-pass falsifier of the form "plumbline reports zero `untagged-prose` sites under `<module>`." That falsifier is mechanically checkable (run the lint, count) — and the implementer dispatches converged on it by tagging sites rather than judging them. **The mechanical falsifier could not distinguish "tagged honestly" from "tagged to satisfy the count."** The cleanup loop's four cycles surfaced new narrator-tag sites every pass; the convergence pattern was new-class-per-cycle, not residue-shrinking.

The lesson for this spec: **the falsifier has to mechanically distinguish honest from dishonest tags.** TD-mechanical-falsifier is the load-bearing decision; if it can't be cashed out as an executable check, the per-pass validator has nothing to argue against and the convergence pattern of the prior sweep will recur. If TD-mechanical-falsifier proves un-cashable, the spec needs a different shape entirely (e.g. a manual audit checklist with documented coverage rather than a sweep).

## Open questions

- **Does the mechanical-falsifier predicate hold up?** Pilot it against a known good site (a real `@deliberate:` that documents a subtle why) and a known narrator site, and see whether the predicate distinguishes them. If false-positives are high, fall back to a smaller scope (only the explicit narrator pattern: `// @<tag>: <Capitalized> ... .` ending with period above a one-line statement).

- **Is `lib/services/executors/` in scope?** The TS subtree under `claude-agent/src/` has its own narrator patterns (`signoff-gate.e2e.test.ts:5` was a tag-pollute the cycle-4 fixer kept rather than deleted because TS file-level comments need a tag without JSDoc exemption). The lint should cover Go and TS uniformly, but the predicate may need TS-specific tweaks for JSDoc-position vs not.

- **Does this spec also do the design-doc promotion of the residual `S-`-prefix stories and archived-spec `@decision:` citations from the same 2026-06-13 sweep?** Those were converted to `@deliberate:` / `@constraint:` with rationale inlined as part of the same review pass (see the 2026-06-13 completion report). Promoting them to durable `stories/` + `decisions/` is a separate concern (different work shape: writing new design-doc artifacts vs sweeping existing tag sites). Recommendation: keep this spec focused on tag-honesty; open a separate sketch / brainstorm for the design-doc promotion.

- **What's the threshold for "substantial token overlap" in the predicate?** Some honest tags include a few words from the line below by necessity ("// @constraint: zero ParkResumeAt means indefinite park." above `ParkResumeAt time.Time` — "ParkResumeAt" overlaps but the comment carries the load-bearing meaning of zero). The threshold needs calibration against the actual corpus.

- **Is the scope just tagged sites, or does it extend to GoDoc-exempt comments on internal declarations?** Plumbline's `comment_hygiene` check exempts GoDoc-shape comments (first word names the next declaration). The exemption is for Go ecosystem integration — `go doc`, IDE hover, pkg.go.dev all read GoDoc for exported-API documentation. But the lint can't tell the difference between a GoDoc on an exported declaration (legitimate ecosystem surface) and a GoDoc on an unexported / package-internal declaration (which under the project's broader "default to no comments" principle should usually be deleted — the package-internal reader can read the code). The exemption is *permission to follow Go convention*, not a free-pass mechanism for internal docs. This sweep's scope question is whether to extend the per-site judgment from `@deliberate:` / `@constraint:` sites to also include GoDoc-shape comments on internal declarations — same three-action rule (keep if load-bearing / rewrite / delete). Pro: consistent discipline across all comment surfaces; the residue category is the same shape. Con: significantly larger scope (every internal-decl GoDoc in the tree), and the line between "internal but documented for in-package readers" and "internal residue" is judgment, not mechanical. Predicate candidate: a GoDoc-shape comment above an unexported declaration (lowercase first letter) where the body doesn't add information the signature lacks. If included in this work, the falsifier (TD-mechanical-falsifier) needs to grow this branch and the predicate calibrated separately. If excluded, the scope stays "tagged residue only" and the broader GoDoc-on-internal question gets its own sketch.

## Starting data

The 2026-06-13 cleanup loop's final reviewer captured 60+ narrator sites by grep:

```
git grep -nE "^[[:space:]]*// @deliberate: [A-Z][a-zA-Z ]+\.$" -- '*.go' '*.ts'
```

Plus 11 sites in `lib/control/controlapi/nodes_test.go` with `@constraint:` test-step narrators. Plus a long tail (un-enumerated) of `---- <prose> ----` hybrid-divider patterns the cycle-4 fixer caught via Unicode-aware grep — those should be folded into this work's enumeration phase since they share the same dishonesty.

The first phase of this work is enumeration: a fresh sweep of every `@deliberate:` and `@constraint:` site in the tree, categorized into the three end-states of TD-three-action-rule-per-site. The enumeration is the input to the per-site work and to the predicate-tuning of TD-mechanical-falsifier.

## What this is NOT

- Not a re-run of the per-module untagged-prose sweep. The lint already passes; this is about the quality of tag application, not the presence of tags.
- Not a methodology change. Plumbline's structured-tag vocabulary stays; the cheatsheet's rules stay; the project-extended tags (`@concept:`, `@story:`, `@decision:`) stay. This sweep enforces the existing rule more honestly.
- Not a design-doc change. The four TDs above are local to the cleanup; they don't add concepts or change boundaries.
