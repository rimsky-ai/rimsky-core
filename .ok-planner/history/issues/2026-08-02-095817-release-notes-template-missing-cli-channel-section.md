---
issue: release-notes-template-missing-cli-channel-section
kind: audit
category: doc-drift
artifacts:
  - decision:release-notes-template
status: repaired
opened: 2026-08-02T09:58:17Z
---

# The release-notes template had no section for the CLI/GitHub-Release distribution channel

## Question

Does the release-notes template in `RELEASING.md` / `.claude/skills/release/SKILL.md` cover "each distribution channel" as `decision:release-notes-template`'s Choice claims, given `decision:release-distribution` names four channels (images, npm, Go modules, CLI archives via GitHub Releases)?

## Repair

Re-verified: the template fixed seven sections (Breaking changes, What's new, Fixes, Internal, Image set, Go module, npm) — three of the four named channels, missing CLI. `decision:release-distribution` (counterpart artifact) is unambiguous about there being four channels including the CLI-archive channel; the template's own commitment ("each distribution channel") already covers it. The fix is fully determined by mirroring the three existing sibling channel sections' shape (heading + one short description of the artifact and where to get it) and the CLI channel's already-documented facts (`RELEASING.md`'s own "CLI distribution" section: linux/darwin, amd64/arm64, per-archive SBOM). No commitment changes — the template already claimed to cover every channel; this completes the enumeration. Past shipped release notes under `releases/` are historical records and were left untouched (the candidate's "apply it going forward" framing, not retroactive).

Changed: added an eighth `## CLI` section, identical text, to both `RELEASING.md` (Release-notes template skeleton) and `.claude/skills/release/SKILL.md` (step 4, Draft release notes) — the two copies the issue confirmed were already kept identical.

Verified: manual review of both files for fence balance and consistency (no automated gate currently pins this template's shape — none existed before this repair, and adding one is a separate, judgment-bearing scope decision left untouched here).
