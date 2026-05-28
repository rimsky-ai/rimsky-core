# Releasing rimsky

This document covers cutting a release of rimsky's container images and
related publish targets. It is the operator-side companion to the build
targets in `Makefile`.

## Release flow

Rimsky cuts releases via one of two paths.

### Formal releases (`/release` skill)

For human-cut releases that carry SemVer judgment and notes, invoke
`/release` from a Claude Code session in the repo root. The skill
walks:

1. Preflight (verify clean tree, branch on `main`, docker/npm/gh
   logins active, tooling available).
2. Diff inspection — reads the diff since the last stable tag,
   classifies against high-signal surfaces (proto files, persistence
   migrations, exported Go symbols, CLI flags, env vars), proposes
   a SemVer bump.
3. Bumps `lib/protocols/package.json` in the working tree.
4. Drafts `releases/vX.Y.Z.md` against the template (below).
5. Notes review loop — reviewer subagent + skill self-review against
   a rubric (every entry maps to a diff hunk, every breaking surface
   appears in the Breaking changes section, bump matches content).
6. Single user gate — presents bump rationale, full notes, action
   manifest, and any flagged judgment questions. Operator replies
   `go` / `revise <what>` / `abort`.
7. On `go`: stages the release commit (`package.json` +
   `releases/vX.Y.Z.md`), creates both git tags, invokes `make
   release` (which runs the extended `lint → license-lint →
   test-all → core-images → service-images → scan → push-images`
   chain), pushes git tags, runs `make publish-protocols`, creates
   the GitHub Release via `gh release create`.

If scan finds CVEs, the skill attempts mechanical patch-level base-
image remediation (per `docker scout recommendations`); anything
bigger bails to the operator.

See `.claude/skills/release/SKILL.md` for the full skill prose.

### Dev / nightly releases (`make dev-release`)

For pre-release / community-testing builds, run `make dev-release`.
The target derives a SemVer-2.0 pre-release version of the form
`v<next-minor>.0-dev.<YYYYMMDD>.g<sha>` from the latest stable tag,
then runs the same `make release` chain with `LATEST_TAG=dev`. Result:

- Hub images get `:vX.Y.Z-dev.YYYYMMDD.gSHA` plus the floating `:dev`
  tag. `:latest` is never moved.
- Both git tags (`vX.Y.Z-dev.YYYYMMDD.gSHA` and
  `lib/protocols/vX.Y.Z-dev.YYYYMMDD.gSHA`) are pushed.
- `@rimsky-ai/protocols` is published under the `dev` npm dist-tag.
- GitHub creates a prerelease (with auto-generated notes).

No release-notes file is written; dev consumers track the `:dev` Hub
tag, the `@rimsky-ai/protocols@dev` npm dist-tag, or pin to a specific
version.

Consumers opt in:

- Docker: `docker pull docker.io/rimskyai/rimsky-all-in-one:dev`
- npm: `npm install @rimsky-ai/protocols@dev`
- Go: `go get github.com/rimsky-ai/rimsky-core/lib/protocols@v0.X.0-dev.YYYYMMDD.gSHA`

The SHA is dot-joined into the SemVer pre-release segment rather than
carried as `+gSHA` SemVer build metadata. `+` is invalid in Docker tag
grammar, is silently stripped by `npm version`, and is rejected by
`go get`; folding the SHA into the pre-release segment keeps all three
toolchains happy while preserving SemVer-2.0 precedence (a pre-release
identifier still sorts below the corresponding stable).

The dev path is mechanical — no SemVer judgment, no notes, no review.
Trigger it manually, from cron, from a CI hook on push to `main`, or
anywhere a clean tree is available.

### Shared chain

Both paths invoke `make release`, which runs:

```
lint → license-lint → test-all → core-images → service-images → scan → push-images
```

- `lint` and `license-lint` are cheap host-side gates.
- `test-all` runs the full Go test suite across all four modules,
  including testcontainer-using tests (requires Docker daemon).
- `core-images` and `service-images` build the 4 + 11 images locally.
- `scan` runs `docker scout cves --only-severity critical,high
  --exit-code` against every locally-built image. Blocks on
  unaddressed critical or high CVEs.
- `push-images` uses `docker buildx build --push
  --provenance=mode=max --sbom=true` so SBOM + provenance
  attestations attach to each manifest. Pushes `:$(VERSION)` plus
  `:$(LATEST_TAG)` — the latter defaults to `latest`, overridden to
  `dev` for dev releases.

Refuses to run from a dirty tree (via `check-clean`).

## Image set

Published under `docker.io/rimskyai/`:

| Image | Source |
|-------|--------|
| `rimsky` | `dockerfiles/Dockerfile.rimsky` — all role binaries + entrypoint |
| `rimsky-all-in-one` | `dockerfiles/Dockerfile.all-in-one` — zero-config SQLite stack |
| `rimsky-host-agent-proxy` | `dockerfiles/Dockerfile.go-base` |
| `rimsky-conformance` | `dockerfiles/Dockerfile.conformance` — protocol conformance runners |
| `rimsky-store-filesystem` | `lib/services/stores/filesystem/Dockerfile.filesystem` |
| `rimsky-store-postgres` | `lib/services/stores/postgres/Dockerfile.postgres` |
| `rimsky-sensor-cron` | `lib/services/sensors/sensor-cron/Dockerfile.sensor-cron` |
| `rimsky-sensor-http` | `lib/services/sensors/sensor-http/Dockerfile.sensor-http` |
| `rimsky-sensor-object-store` | `lib/services/sensors/sensor-object-store/Dockerfile.sensor-object-store` |
| `rimsky-sensor-webhook` | `lib/services/sensors/sensor-webhook/Dockerfile.sensor-webhook` |
| `rimsky-subscriber-openlineage` | `lib/services/subscribers/openlineage/Dockerfile.openlineage` |
| `rimsky-executor-http-node` | `lib/services/executors/http-node/Dockerfile.http-node` |
| `rimsky-executor-verifier-http` | `lib/services/executors/verifier-http/Dockerfile.verifier-http` |
| `rimsky-executor-verifier-shape-checks` | `lib/services/executors/verifier-shape-checks/Dockerfile.verifier-shape-checks` |
| `rimsky-executor-claude-agent` | `lib/services/executors/claude-agent/Dockerfile` |

Each lands on Hub at both `:$(VERSION)` and `:latest`.

Hub namespace is `rimskyai` (no hyphen). The hyphenless form is a Docker
Hub constraint — namespaces disallow hyphens — and is intentional. The
GitHub org (`rimsky-ai`) and the npm scope (`@rimsky-ai`) keep the hyphen;
the image registry does not. Do not "correct" `rimskyai` to `rimsky-ai`.

## Release-notes template

The `/release` skill writes one Markdown file per formal release to
`releases/vX.Y.Z.md`, filled in from the diff analysis. Skeleton:

````markdown
# rimsky vX.Y.Z

<one-paragraph release summary>

## Breaking changes

- <surface-by-surface enumeration; omit section if empty>

## What's new

- <user-facing features, additions, new behaviors>

## Fixes

- <bug fixes worth surfacing to consumers>

## Internal

- <refactors, build changes, test additions; brief>

## Image set

`docker.io/rimskyai/rimsky:vX.Y.Z` and 14 sibling images, all at
`:vX.Y.Z` and `:latest`. See [`RELEASING.md`](../RELEASING.md) for
the full list.

## Go module

```
go get github.com/rimsky-ai/rimsky-core@vX.Y.Z
go get github.com/rimsky-ai/rimsky-core/lib/protocols@vX.Y.Z
```

## npm

```
npm install @rimsky-ai/protocols@X.Y.Z
```
````

Section rules: empty sections are omitted; every entry references a
real diff hunk. The skill's review loop catches fabrications and
omissions before the operator sees the draft.

## Docker Scout integration

`make scan` calls `docker scout cves` per image. Scout is installed as a
docker CLI plugin (bundled with Docker Desktop, available standalone
elsewhere). The local scan does not require Hub enrollment — it analyzes
the local docker daemon's image and queries Scout's vulnerability
database directly.

The gate is set to `--only-severity critical,high --exit-code`: any
unaddressed critical or high CVE blocks the release. To downgrade the
gate to advisory (warn but don't fail), remove the `--exit-code` flag
from the `scan` target in the Makefile.

Two related Scout commands are useful but not wired into the release
chain:

- `docker scout recommendations <image>` — base-image upgrade suggestions.
- `docker scout compare <new> <old>` — diff CVEs between two image tags.

## Supply chain attestations

Every `push-images` run attaches two attestations to each image manifest:

- **SBOM** (`--sbom=true`) — software bill of materials generated by
  buildx via syft. Lists every OS package, Go module, and npm package
  in each layer.
- **Provenance** (`--provenance=mode=max`) — build-context metadata:
  source repo, builder version, dockerfile path, build args.

Both are visible in the Hub UI's image-detail tab and consumed by Docker
Scout for the per-repo health grade. Without them, Hub reports
"Missing supply chain attestation(s)" and downgrades the grade
regardless of CVE state.

The buildx instance used for push is named `rimsky-builder` and is
created on first use by `make buildx-builder`. It uses the
`docker-container` driver, which gives consistent attestation support
across Docker Desktop, OrbStack, and headless CI runners.

## Hub-grade gotcha: "No AGPL v3 licenses" green check

Docker Hub's AGPL check scans the **third-party supply chain** —
OS packages, Go module dependencies, npm packages — for AGPL-licensed
code that would contaminate downstream consumers. The green check does
**not** mean "this image is AGPL-free." It means "this image's
dependencies do not include AGPL-licensed code."

Rimsky's own application binaries are AGPL-3.0-or-later (per
`licensing.yml`), but they're built from this repo, not pulled as a
third-party dep. Consumers of these images run AGPL software,
regardless of what Docker's check reports.

This is purely a quirk of how Docker scans images. The COPYING.md and
each image's `org.opencontainers.image.licenses` label (where present)
remain the authoritative license signal.

## Docker-Sponsored Open Source (DSOS)

Docker offers a sponsorship program for qualifying open source projects:
unlimited Scout-enabled repos, no Hub rate limits, removal of Hub UI
ads, and other operational perks. Application is via form, with manual
review.

### Eligibility

- OSI-approved license (AGPL-3.0-or-later qualifies — rimsky's primary
  license).
- Public source repository.
- Project is non-commercial.
- Active maintenance signal.

### Application materials

Project description (paste into the Docker form's description field):

> Rimsky is a project-agnostic reactive node-graph orchestration
> platform written in Go. It provides a control API, supervisor,
> scheduler, and a set of bundled service implementations
> (claim-producer stores, sensors, subscribers, executors) for building
> reactive data workflows. The project is licensed under
> AGPL-3.0-or-later with permissive carve-outs for the wire protocols
> (Apache-2.0) and the TypeScript executor reference implementation
> (Apache-2.0). Public source lives at github.com/rimsky-ai/rimsky-core;
> container images ship from docker.io/rimskyai.

Form fields you'll need:

- Project name: `rimsky`
- Organization: `rimsky-ai`
- Source URL: https://github.com/rimsky-ai/rimsky-core
- License: AGPL-3.0-or-later
- License URL: https://github.com/rimsky-ai/rimsky-core/blob/main/COPYING.md
- Docker Hub namespace: `rimskyai`
- Maintainer: (you fill in)

### Apply

The application form lives under Docker's community page (the URL has
moved over time — search "Docker-Sponsored Open Source program" if the
link below has rotted):

https://www.docker.com/community/open-source/application/

Submit; approval is manual and operator-driven, not automated.

## Pre-v1 caveats

Rimsky is pre-v1 (see `.claude/rules/rules.md`). Image tags are not yet
considered backward-compatible — a release may drop a binary, rename a
config key, or change an env-var contract without a deprecation window.
Operators tracking `:latest` should expect to read the commit message
on every release.

When v1 ships, this section gets replaced with deployed-stage
compatibility commitments.
