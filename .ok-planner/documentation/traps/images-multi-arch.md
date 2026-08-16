---
trap: images-multi-arch
release: d977250c
demonstration: experiment:assumption-images-multi-arch
---
## Assumption

As operator on Apple silicon or Graviton, I would take it that the published images are multi-architecture (amd64 and arm64), because the release archives ship both architectures for both operating systems.

sibling-symmetry — four `rimsky_<version>_{linux,darwin}_{amd64,arm64}` release archives

## Actual behavior

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
