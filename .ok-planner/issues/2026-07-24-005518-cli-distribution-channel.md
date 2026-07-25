---
issue: cli-distribution-channel
kind: human
category: release-process
artifacts:
  - RELEASING.md
status: verified
opened: 2026-07-24T00:55:18Z
---

# CLI binaries now ship — what's left is three optional follow-ups and a documentation home

This issue's original problem is already solved: rimsky's command-line tool used to have no downloadable binary (build-from-source was the only path), and the release process now builds Linux and macOS archives (Intel and ARM) via goreleaser and publishes them to GitHub Releases — verified locally, with the real publish awaiting the first tagged release. Two limitations were discovered and deliberately documented along the way: the CLI cannot build for Windows (it transitively embeds Unix-only system calls), and Go's standard network-install command (`go install <module>@<version>`) cannot work here, because the repo's internal sub-modules are wired together with local-path redirects that only resolve inside a full checkout — an earlier claim that it worked was simply wrong.

What's genuinely still open is smaller. Three optional follow-ups: a Homebrew tap (macOS/Linux `brew install`, at the cost of an extra repo to maintain forever); SBOMs on the CLI archives (build-contents manifests for supply-chain verification — the container images already ship them, so this is a consistency completion); and making `go install` work, which would require publishing the sub-modules as independently versioned Go modules — a packaging overhaul far larger than CLI distribution itself. And one unfiled question: the whole mechanism — goreleaser, the Unix-only matrix, the `go install` non-support — currently lives only in the release runbook, despite being exactly the kind of tradeoff-bearing choice the design corpus's decision catalog exists to record.

## Options

- **Promote the mechanism into a decision document**; take SBOMs after the first release proves the publish step; defer the tap until someone asks; leave `go install` unsupported.
- **Take the tap now too** — broadest reach, standing maintenance cost with no requester yet.
- **Pursue `go install` support** — only worth it as a deliberate module-publishing decision, not as a distribution afterthought.

The ruling decides which follow-ups, on what trigger, and whether the mechanism gets its corpus home.

## Ruling

> Recommended ruling (/recommend-rulings): Promote the distribution
> mechanism into design/decisions (goreleaser prebuilt binaries, Unix-
> only matrix, go-install-unsupported as the module-layout
> consequence). Take up CLI-archive SBOMs after the first tagged
> release proves the publish step; defer the Homebrew tap until
> there's demand; go install stays unsupported.
>
> Rationale: The mechanism is real tradeoff-bearing decision content
> living only in RELEASING.md — a future audit would flag it anyway.
> SBOMs are a consistency completion of the existing attestation
> posture; the tap is a standing maintenance cost with no requester
> yet.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
