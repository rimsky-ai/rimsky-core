---
experiment: assumption-images-multi-arch
commit: PENDING
---

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
