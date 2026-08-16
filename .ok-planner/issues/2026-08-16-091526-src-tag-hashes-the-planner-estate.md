---
issue: src-tag-hashes-the-planner-estate
kind: audit
category: unclear
artifacts:
  - concept:module-layout
status: verified
opened: 2026-08-16T09:15:26Z
---

# The image source tag hashes the planner estate, so writing a record moves the tag

Images are addressed by a tag derived from a hash of the working tree — every tracked and untracked file, no exclusions — so an unchanged tree always yields the same tag and a stale image is unrepresentable. The planner estate is 1,831 of the repo's 3,748 tracked files, and any write there (an audit, a filed issue, a sketch) moves the tag though no shippable code changed; this run measured at one tag and, hundreds of estate files later, derives another, with no code touched — it froze the tag by hand for the duration. The derivation script is suite-owned and rewritten on every converge, and the project's workspaces profile carries only its path, no exclusion list. The ruling decides whether the estate is part of "the tree" for addressing.

## Options

- Exclude the ceremony estates from the hash — a suite feature (an exclusion list in the workspaces profile), not a local edit, since the script is overwritten on converge; cost: taking it up with the suite.
- Keep whole-tree addressing and document the operating rule this run followed: a ceremony that writes to the estate freezes the tag for its measuring run; cost: an operational habit every measuring run must remember.

The ruling decides what "the tree" means for content addressing.

## Ruling

> Recommended ruling (/verify-issues): Exclude the estates from the derivation, as a suite feature — the workspaces profile gains an exclusion list and the materialized script honours it — because an image built from unchanged code should keep its address while records are written beside it.
>
> Rationale: the tag exists to make staleness unrepresentable, and a tag that moves without a code change represents nothing; the estate is by its own rules a record, not the tree. Flip case: if the suite maintainers decline the knob, the second option becomes the standing rule and the ceremony contributions should state it.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
