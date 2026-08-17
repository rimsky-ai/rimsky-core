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

Writing a planner record moves the image source tag, though no shippable code changed. The tag derives from a hash of the working tree, so an unchanged tree always yields the same tag and a stale image is unrepresentable. The hash covers every tracked and untracked file, with no exclusions. The planner estate holds 1,831 of the repo's 3,748 tracked files. Any write there, whether an audit, a filed issue, or a sketch, moves the tag. This run measured at one tag. Hundreds of estate files later it derives another, with no code touched. It froze the tag by hand for the duration. The derivation script is suite-owned, and every converge rewrites it. The project's workspaces profile carries only the script's path, no exclusion list. The ruling decides whether the estate is part of "the tree" for addressing.

## Options

- Exclude the ceremony estates from the hash. This takes a suite feature (an exclusion list in the workspaces profile), not a local edit, because the converge overwrites the script; cost: taking it up with the suite.
- Keep whole-tree addressing and document the operating rule this run followed: a ceremony that writes to the estate freezes the tag for its measuring run; cost: a habit every measuring run must remember.

The ruling decides what "the tree" means for content addressing.

## Ruling

> Recommended ruling (/verify-issues): Exclude the estates from the derivation, as a suite feature: the workspaces profile gains an exclusion list and the materialized script honours it. An image built from unchanged code should keep its address while records are written beside it.
>
> Rationale: the tag exists to make staleness unrepresentable, and a tag that moves without a code change represents nothing. The estate is by its own rules a record, not the tree. Flip case: if the suite maintainers decline the knob, the second option becomes the standing rule and the ceremony contributions should state it.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
