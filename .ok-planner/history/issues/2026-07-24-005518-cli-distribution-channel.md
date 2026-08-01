---
issue: cli-distribution-channel
kind: human
category: release-process
artifacts:
  - RELEASING.md
status: promoted
sprint: 2026-08-01-guidance-realignment-drain.md
opened: 2026-07-24T00:55:18Z
---

# CLI binaries now ship — what's left is three optional follow-ups and a corpus home

This issue's original problem is solved: rimsky's command-line tool used to have no downloadable binary, and the release process now builds Linux and macOS archives (Intel and ARM) via goreleaser and publishes them to GitHub Releases. Two limitations were discovered and deliberately accepted along the way: the CLI cannot build for Windows (it transitively embeds Unix-only system calls), and Go's network-install command (`go install <module>@<version>`) cannot work, because the repo's sub-modules are wired together with local-path redirects that only resolve inside a full checkout.

What remains is smaller, and one piece of it has moved since filing: a `decision:release-distribution` now exists but is minimal — it names the four channels (container registry, npm, Go modules, GitHub Releases) without any of the CLI-specific tradeoffs that currently live only in the release runbook (goreleaser, the Unix-only matrix, go-install-unsupported as a module-layout consequence). So the "no corpus home" gap has become an "underfilled corpus home" gap. The three optional follow-ups stand as filed: a Homebrew tap (an extra repo to maintain forever, no requester yet); SBOMs on the CLI archives (the container images already ship build-contents manifests, so this is a consistency completion); and real `go install` support, which would require publishing the sub-modules as independently versioned Go modules — a packaging overhaul far larger than CLI distribution itself.

## Options

- **Enrich `decision:release-distribution`** with the CLI tradeoffs; take SBOMs after the first tagged release proves the publish step; defer the tap until someone asks; leave `go install` unsupported.
- **Take the tap now too** — broadest reach, standing maintenance cost with no requester.
- **Pursue `go install`** — only worth it as a deliberate module-publishing decision, not a distribution afterthought.

The ruling decides which follow-ups run, on what trigger, and whether the decision gets enriched.

## Ruling

> Recommended ruling (/verify-issues): enrich the existing
> decision:release-distribution with the CLI-channel tradeoffs
> (goreleaser prebuilt archives, the Unix-only build matrix,
> go-install-unsupported as a module-layout consequence); take up
> CLI-archive SBOMs once the first tagged release proves the publish
> step; defer the Homebrew tap until there is a requester; go
> install stays unsupported.
>
> Rationale: the tradeoffs are real decision content living only in a
> runbook — the audit would flag the gap anyway — and enriching the
> existing decision beats authoring a duplicate; SBOMs complete an
> attestation posture the images already have, while the tap is a
> standing cost with no demand. The flip case: a first user asking
> for brew install flips the tap from deferred to worth its repo.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
