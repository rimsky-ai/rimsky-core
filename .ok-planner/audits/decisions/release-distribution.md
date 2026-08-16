---
audit: release-distribution
artifact: decision:release-distribution
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 4
unaccounted: 0
---

# Whether the four distribution channels exist and the two named non-channels hold

Supported. All four channels are wired: the push target ships 15 attested images, a publish target publishes the protocols package to the npm registry under the project scope (with a dev-tagged sibling), the Go-module channel is the full-checkout workspace whose manifests redirect siblings to local paths, and the archive configuration builds the CLI for exactly the two operating systems and two architectures named, emits a per-archive SBOM, and creates the GitHub Release. A fitness test pins the platform matrix, fails if Windows is ever added, and fails if the per-archive SBOM step disappears. The go-install non-channel is real and follows directly from those local-path redirects, which that install path ignores. The Windows non-channel is real as a distribution fact — nothing publishes a Windows archive — but its stated cause is not visible in the tree: each of the three operating-system-specific spots in the CLI's own tree carries either a Windows sibling file or a non-Unix fallback, so the Unix-only-system-call reasoning would need a cross-compile to confirm and is not what currently holds the line.
