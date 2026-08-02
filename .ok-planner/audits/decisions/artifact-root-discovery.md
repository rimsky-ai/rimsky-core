---
audit: artifact-root-discovery
artifact: decision:artifact-root-discovery
determination: supported
commit: b767a27d
audited: 2026-08-02T09:44:16Z
---

# Walk-up-to-marker artifact root discovery, with an explicit override

Supported. `DiscoverArtifactRoot` (`cmd/rimsky/cli/compose/artifact.go`)
walks parent directories from the current working directory looking for a
`.rimsky` marker directory and returns the first ancestor that has one;
finding none, it returns the (absolute) starting cwd unchanged, and the
marker plus run tree get created lazily by the first `EnsureRunDir` call —
so a first run creates one where it's invoked. A non-empty `workdirOverride`
bypasses the walk entirely, creating the override path directly. All three
paths are covered by `TestDiscoverArtifactRoot_FindsAncestor`,
`TestDiscoverArtifactRoot_StopsAtRoot`, and
`TestDiscoverArtifactRoot_WorkdirOverride` in
`cmd/rimsky/cli/compose/artifact_test.go`; the `--workdir` flag wiring is
visible in `cmd/rimsky/cli/compose/run.go`, which passes it straight through
as `flags.workdir` and never falls back to the walk when it is set.
