---
decision: release-distribution
---

# Distribution channels

## Choice

Four channels: container-registry images with SBOM and provenance attestations, published as multi-platform indexes covering Linux on Intel and ARM; the protocols npm package; Go modules consumed from a full checkout; and GitHub Releases carrying prebuilt CLI archives built by goreleaser for Linux and macOS on Intel and ARM, each archive accompanied by a published SBOM. The CLI ships as prebuilt archives only: Windows is unsupported (the CLI transitively embeds Unix-only system calls), and network `go install` is unsupported (the workspace's modules are wired with local-path redirects that resolve only inside a full checkout).

## Rationale

Multiple consumption patterns need multiple channels — images for deployment, npm for protocol consumers, Go modules for embedders, prebuilt archives for CLI users without a Go toolchain. Images and archives carry the same processor matrix, so an operator on an Intel host runs the published images rather than building them. The binaries inside are pure Go and cross-compile, so the second platform costs build time and nothing else. Naming the two non-channels in the Choice keeps each from being rediscovered as a bug: the Unix-only matrix and the go-install gap are consequences of deliberate choices (system-call usage, workspace module layout), not oversights.

## Alternatives

- Publish images for the release machine's own processor and direct everyone else to build locally — rejected: an operator on an Intel host could then run no published image, and images exist to spare an operator a local build.
- Publishing the workspace's sub-modules as independently versioned Go modules so `go install` works — rejected: a packaging overhaul far larger than CLI distribution itself, motivated by nothing current.
