---
issue: src-tag-hashes-the-planner-estate
kind: audit
category: unclear
artifacts:
  - concept:module-layout
status: retired
opened: 2026-08-16T09:15:26Z
---

# The image source tag hashes the planner estate, so writing a record moves the tag

Writing a planner record moves the image source tag, though no shippable code changed. The tag derives from a hash of the working tree, so an unchanged tree always yields the same tag and a stale image is unrepresentable. The hash covers every tracked and untracked file, with no exclusions. The planner estate holds 1,831 of the repo's 3,748 tracked files. Any write there, whether an audit, a filed issue, or a sketch, moves the tag. This run measured at one tag. Hundreds of estate files later it derives another, with no code touched. It froze the tag by hand for the duration. The derivation script is suite-owned, and every converge rewrites it. The project's workspaces profile carries only the script's path, no exclusion list. The ruling decides whether the estate is part of "the tree" for addressing.

## Options

- Exclude the ceremony estates from the hash. This takes a suite feature (an exclusion list in the workspaces profile), not a local edit, because the converge overwrites the script; cost: taking it up with the suite.
- Keep whole-tree addressing and document the operating rule this run followed: a ceremony that writes to the estate freezes the tag for its measuring run; cost: a habit every measuring run must remember.

The ruling decides what "the tree" means for content addressing.

## Ruling

Retired: superseded. Content addressing is the wrong instrument for verification. A test needs two properties: concurrent runs do not collide, and a run uses a fresh image. A per-run tag gives both. It needs no definition of which files count. The ok-workspaces suite replaces its content-addressed tag with a per-run tag (`run-<12 hex>`, minted once per verification run and handed to tests through one environment variable). This project adopts it through its Makefile, CI shard, and services harness when the suite converges. That work rides the sprint planned in this session.
