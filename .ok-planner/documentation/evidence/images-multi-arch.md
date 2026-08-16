---
trap: images-multi-arch
release: d977250c
---
# Evidence set — the published images are multi-architecture (amd64 and arm64), because the release archives ship both architectures for both operating systems.

Source of the prior: sibling-symmetry — four `rimsky_<version>_{linux,darwin}_{amd64,arm64}` release archives

## What the audit ran and observed (assumption record)

Experiment `assumption-images-multi-arch` (four checks, none failing) read the
manifest of each of the fifteen published images from the registry the release
chain pushes to, `docker.io/rimskyai`, at the `latest` tag — the manifest as a
puller sees it. The prior does not hold. Every one of the fifteen carries
exactly one platform, `linux/arm64`; none carries `linux/amd64` and none is a
multi-platform index over both. The images built locally from this tree behave
the same way: one architecture per image, the builder's own. The release
archives do ship four `{linux,darwin}_{amd64,arm64}` combinations, so the CLI
covers both architectures while the images cover one. An operator on Apple
silicon or Graviton can pull and run; an operator on x86-64 — the ordinary case
for a cloud host — gets no runnable image at all, and finds out at `docker run`.

## Experiment record (experiment:assumption-images-multi-arch)

# Which architectures the published images carry

## What it ran against

The fifteen image names the release pushes, read from the registry the release
chain publishes to (`docker.io/rimskyai`) at the floating `latest` tag, through
`docker buildx imagetools inspect` — the manifest as a puller sees it. The same
fifteen images built locally from this tree are then inspected for the
architecture a local build produces.

## What was observed

Four checks, none failing.

Every one of the fifteen published manifests carries exactly one platform:
`linux/arm64`. Not one carries `linux/amd64`, and not one is a multi-platform
index over both — the only other entry in each index is the attestation
manifest, which has no platform of its own. The list is the whole shipped set:
`rimsky`, `rimsky-all-in-one`, `rimsky-host-agent-proxy`, `rimsky-conformance`,
the two claim producers, the four sensors, the openlineage subscriber and the
four executors.

The local build behaves the same way: every image built from this tree carries
one architecture, the builder's own.

Runnables: `src:.ok-planner/experiments/assumption-images-multi-arch/` at the stamped commit.
