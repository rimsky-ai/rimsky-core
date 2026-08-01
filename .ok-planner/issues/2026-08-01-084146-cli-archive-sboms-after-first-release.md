---
issue: cli-archive-sboms-after-first-release
kind: sprint
category: inconsistent
artifacts:
  - decision:release-distribution
status: verified
opened: 2026-08-01T08:41:46Z
---

# Add SBOMs to the CLI release archives — the trigger has fired

Rimsky ships two kinds of release artifacts: container images and prebuilt CLI archives on GitHub Releases. The images carry SBOMs (build-contents manifests) and provenance attestations; the CLI archives carry neither — a consumer auditing their supply chain can verify what went into an image but not what went into the CLI binary they downloaded. The owner already ruled on this gap: close it, but only after the first tagged release proved the goreleaser publish path end to end. This issue existed to hold that follow-up until the trigger fired.

It has fired. `v0.12.0` (tagged 2026-07-27) published successfully through goreleaser — the release on GitHub carries `checksums.txt` and four platform tarballs. Meanwhile the release chain still generates SBOMs only on the image-push step; the goreleaser config and Makefile have no SBOM step for the CLI archives, and `decision:release-distribution`'s Choice still describes the CLI channel as bare prebuilt archives.

## Options

- Do the deferred work now: add SBOM generation for the CLI archives to the release chain and extend the distribution decision to say the archives carry build-contents manifests. Cost: tool selection and release-chain wiring — sprint work, not a mechanical repair.
- Wait for more than one proven release before committing. Cost: the attestation gap persists with no holder other than this issue.

The ruling decides whether the owner's already-made deferral now converts into scheduled work.

## Ruling

> Generated ruling (/verify-issues): Carry the deferred work into the
> next sprint — generate SBOMs for the prebuilt CLI archives in the
> release chain and extend the release-distribution decision to say
> the archives carry build-contents manifests. The owner's accepted
> ruling on the CLI distribution channel already decided this,
> conditional only on the first tagged release proving the publish
> step; v0.12.0 proved it, so the condition is met and the resolution
> is forced by the standing decision, not new judgment.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
