---
issue: published-images-are-single-platform-arm64
kind: audit
category: conflicting
artifacts:
  - decision:release-distribution
  - decision:release-chain
  - decision:image-set-four-core
  - decision:image-set-bundled-services
status: verified
opened: 2026-08-16T09:35:04Z
---

# Every published image is arm64-only

The release chain pushes fifteen images with provenance and SBOM attestations, and the distribution decision commits the CLI archives to both operating systems and both architectures. The push flags carry no platform list, so every image is built for the release machine's native architecture alone; all fifteen published tags are single-platform arm64 — an operator on an ordinary x86-64 host cannot run any of them. No artifact says images are multi-arch, so this is not a text contradiction; it is a distribution gap beside a matrix the archives already honour. The ruling decides whether images join that matrix.

## Options

- Declare an image platform matrix in the distribution decision and build every image as a multi-platform index; cost: cross-compilation or multi-arch builders in the release chain.
- Add a release gate that reads back each pushed tag's platforms and fails on mismatch; cost: catches the symptom, fixes nothing alone.
- Narrow the distribution decision to arm64 images and document x86-64 as archives plus local build; cost: shrinks what the project ships.

The ruling decides whether images are multi-platform.

## Ruling

> Recommended ruling (/verify-issues): Build multi-platform images (amd64 and arm64) as part of the release chain, declare the platform matrix in the distribution decision beside the archive matrix, and add the read-back gate so a single-platform push fails the release.
>
> Rationale: an orchestration platform whose images cannot run on x86-64 cloud hosts is not shipped; the archives already prove the matrix, and the images are pure-Go and cross-compile. Flip case: if the release machine cannot cross-build in acceptable time, the gate plus a documented arm64-only posture is the honest interim.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
