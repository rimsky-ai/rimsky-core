---
issue: cli-archive-sboms-after-first-release
kind: sprint
category: inconsistent
artifacts:
  - decision:release-distribution
status: open
opened: 2026-08-01T08:41:46Z
---

# Add SBOMs to the CLI release archives once the first tagged release proves the publish step

## Problem

The container images ship with SBOM and provenance attestations; the prebuilt CLI archives published to GitHub Releases carry no build-contents manifest. The owner's accepted ruling on `cli-distribution-channel` (promoted into sprint 2026-08-01-guidance-realignment-drain) deliberately postponed closing this consistency gap until the first tagged release has proven the goreleaser publish step end to end. That trigger has not yet fired, and nothing else holds the follow-up.

## Candidates

- Once a tagged release has published successfully, amend `decision:release-distribution`'s Choice to state that CLI archives carry build-contents manifests, and add the generation step to the release chain.
- Retire the question if the attestation posture changes before the trigger fires.
