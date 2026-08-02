---
decision: release-distribution
---

# Distribution channels

## Choice

Four channels: container-registry images with SBOM and provenance attestations, the protocols npm package, Go modules consumed from a full checkout, and GitHub Releases carrying prebuilt CLI archives built by goreleaser for Linux and macOS on Intel and ARM, each archive accompanied by a published SBOM. The CLI ships as prebuilt archives only: Windows is unsupported (the CLI transitively embeds Unix-only system calls), and network `go install` is unsupported (the workspace's modules are wired with local-path redirects that resolve only inside a full checkout).

## Rationale

Multiple consumption patterns need multiple channels — images for deployment, npm for protocol consumers, Go modules for embedders, prebuilt archives for CLI users without a Go toolchain. Naming the two non-channels in the Choice keeps each from being rediscovered as a bug: the Unix-only matrix and the go-install gap are consequences of deliberate choices (system-call usage, workspace module layout), not oversights.

## Alternatives

- A Homebrew tap — deferred, not rejected: broader macOS/Linux reach at the cost of a standing extra repo to maintain, with no requester yet.
- Publishing the workspace's sub-modules as independently versioned Go modules so `go install` works — rejected: a packaging overhaul far larger than CLI distribution itself, motivated by nothing current.
