---
issue: published-images-are-single-platform-arm64
kind: audit
category: conflicting
artifacts:
  - decision:release-distribution
  - decision:release-chain
  - decision:image-set-four-core
  - decision:image-set-bundled-services
status: promoted
opened: 2026-08-16T09:35:04Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# Every published image is arm64-only

The release chain pushes fifteen images with provenance and SBOM attestations. The distribution decision commits the CLI archives to both operating systems and both architectures. The push flags carry no platform list, so the chain builds every image for the release machine's native architecture alone. All fifteen published tags are single-platform arm64, and an operator on an ordinary x86-64 host cannot run any of them. No artifact says images are multi-arch, so no text contradicts another. The gap is in distribution: the archives honour a platform matrix and the images do not. The ruling decides whether images join that matrix.

## Options

- Declare an image platform matrix in the distribution decision and build every image as a multi-platform index; cost: cross-compilation or multi-arch builders in the release chain.
- Add a release gate that reads back each pushed tag's platforms and fails on a mismatch; cost: it catches the symptom and fixes nothing on its own.
- Narrow the distribution decision to arm64 images and document x86-64 as archives plus a local build; cost: shrinks what the project ships.

The ruling decides whether images are multi-platform.

## Ruling

> Recommended ruling (/verify-issues): Build multi-platform images for amd64 and arm64 in the release chain, declare the platform matrix in the distribution decision beside the archive matrix, and add the read-back gate so a single-platform push fails the release.
>
> Rationale: an orchestration platform whose images cannot run on x86-64 cloud hosts is not shipped. The archives prove the matrix works. The images are pure-Go and cross-compile. Flip case: if the release machine cannot cross-build in acceptable time, the gate plus a documented arm64-only posture is the honest interim.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
