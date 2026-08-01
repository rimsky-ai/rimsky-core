# Intent Dossier: rimsky

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Rimsky is project-agnostic and domain-agnostic: domain needs are expressed by composing primitives (nodes, claims, attributes, events, executors, MCP servers); a new platform primitive is justified only when genuinely cross-cutting. No consumer-specific features, names, or bundled-in-binary consumer conveniences.
- Rimsky must be conceptually very clear and very simple: special cases eliminated, behavior boiled down to distinct code paths that always execute; duplicative systems, remnants, workarounds, and back-compat shims are defects to hunt and remove (2026-06-18, user).
- Pre-v1 stance is pure removal, not fail-loud: no back-compat, no remnants, no code that detects retired shapes to emit migration errors; retired surfaces fail through generic paths. Breaking changes take minor version bumps pre-v1.
- Every concept has exactly one canonical name across code, schema, proto, YAML, binaries, and concept docs.
- The CLI binary is `rimsky` (renamed from rimsky-cli, no shim). `rimsky run <template>` self-hosts by default (boots the full in-process all-in-one stack on loopback, zero-config, one-shot); `--endpoint` preserves the remote dev-loop. `rimsky compose run <manifest>` is the one-shot for compose stacks. Verb-to-input is 1:1, no autodetect.
- In self-host/all-in-one mode the CLI talks to itself through its own control-api over loopback HTTP — one client code path, so every self-hosted run proves the real API surface; bundled service dispatch stays in-process through registries.
- rimsky-entrypoint honors a role command argument: no command → all three roles in one process (with migrate); single role → that role only, migrating only when the role is rimsky-control-api; unknown command exits non-zero. RIMSKY_ENTRYPOINT_MIGRATE=1/0 overrides.

## Required behaviors (open promises)

- One-shot orchestration: a single CLI invocation takes a rimsky-compose file and executes its instances inline in one process — no Docker image, no standing infrastructure — until they resolve to terminal, then exits (2026-06-13, 65667e33, transcript): "rimsky as a one-shot orchestrator: the simplest usage model." Verb: `rimsky compose run <manifest>` (2026-06-13, user).
- `rimsky run <template>` self-hosts: with no endpoint configured, boots scheduler + supervisor + control-api + SQLite + bundled in-proc executors inside the CLI process on a free loopback port, drives the template to terminal, tears down on exit. Explicit --endpoint/env/context keeps remote; --self-host overrides a context; --endpoint plus --self-host is a usage error; self-host is one-shot (--keep rejected) and zero-config (no sibling rimsky.yml pickup) (2026-07-04, 3f71f90a, transcript).
- Zero-config local orchestration does real work: an ad-hoc template through the all-in-one process with no rimsky.yml, no docker, no external services drives to terminal against real bundled services (real claude CLI spawn, real filesystem side effects) — a stub or canned reply is a falsifier (2026-07-03, 8a8539a4, transcript). The all-in-one process includes the bundled executors so rimsky is "trivially easy to use for running ad hoc local templates" (2026-07-01, user).
- Entrypoint role/migrate contract: no-command runs all three roles and migrates once before any role starts; a single-role container migrates only for rimsky-control-api so a three-container split migrates exactly once — never racing, never skipped; unknown command or multiple args exits non-zero; RIMSKY_ENTRYPOINT_MIGRATE=1 forces, =0 skips (2026-06-08, corpus-bootstrap, artifact; correcting the 2026-06-02 remediation finding).
- `rimsky run --service <name>=<path>` runs arbitrary local binaries as rimsky services per-invocation: binaries spawned on the user's machine in the supplied cwd, held for the run-scope's lifetime, reaped on close (2026-05-24, host-agent-and-proxy, artifact): "rimsky just works — the binary runs on their machine … and gets cleaned up automatically."
- `rimsky run` flag surface: `--template <name>` (mutually exclusive with the positional file form), repeatable `--param k=v` mixable with `--params <json>` (later overrides earlier), `--service <name>=<path>`; the existing `rimsky run <file>` register+deploy+create shape unchanged (2026-05-24, host-agent-and-proxy, artifact) `(artifact-only)` — note the self-host default (2026-07-04) changed what `rimsky run` does with no endpoint; the flag grammar itself was never retracted.
- `rimsky auth init` bootstraps via anonymous mode (server anonymous-mode predicate is the authoritative gate; CLI refuse-if-keys-exist is a UX nicety); break-glass is a documented direct-DB operation, no CLI verb (2026-05-15, control-plane-mcp-and-auth, artifact). `rimsky auth login` is a sibling convenience verb writing the api-key into the active CLI context (2026-05-24, artifact).
- Template source_file inlining: any string-valued position in a template spec may be `{source_file: <relative-path>}`, resolved by the CLI at register time in a single pass; the server only sees resolved bytes (2026-05-19, multi-instance-template-ergonomics, artifact). Resolution is confined to the template directory subtree; absolute paths and escapes are security errors, CLI exit code 2 (2026-05-19, artifact).
- Client-side alias files (~/.rimsky/aliases.yml, .rimsky/aliases.yml) resolve `--service` shorthand; the server never sees aliases (2026-05-24, host-agent-and-proxy, artifact) `(artifact-only)`.
- Sophisticated multi-loop orchestrator patterns (build/validate loops, strategist agents, review cycles) must be expressible through generic composable primitives only — no use-case-specific features (2026-06-14, 752fe200, transcript): "without adding overly specific features."

## Intentional absences

- Human review is not a platform primitive: composed from indefinite park + async executor + terminal verdicts + external UI; the planned rimsky_review_history table was never created (2026-05-08, platform-extensions, artifact).
- Bundled reference stores (parquet / geo-parquet / geo-postgis): CUT in full mid-execution, explicitly not deferred, with instruction that no follow-up revive them — specialized format stores belong with the users who need them (2026-05-15, data-platform-extensions, artifact).
- Bulk-instance manifest CLI subcommand: declined; at that scale bulk loaders are the right tool, not YAML manifests (2026-05-19, multi-instance-template-ergonomics, artifact).
- Bundled Go executors linked into the CLI binary by short name: rejected as a layering violation of project-agnosticism; consumers ship them as binaries via --service (2026-06-13, 65667e33, transcript). (The later all-in-one direction (2026-07-01+) bundles executors into the all-in-one process/self-host stack — that is the self-hosted runtime registering bundled handlers, not short-name CLI linking; both decisions stand on their own terms.)
- Coding-agent orchestration for local dev: lives in a standalone consumer project bundling rimsky; rimsky-core contributes only the platform primitives (compose run, local-binary spawning) (2026-06-13, 65667e33, transcript).
- Fail-loud detection of retired shapes: rejected; retired surfaces (coalesce, frame: modifier, message topic kind, kind: invalidate) fail through generic paths; the code carries no knowledge the features existed (2026-06-14, bfc9febb, transcript, user).
- Major version bump for pre-v1 breaking changes: rejected; v0.10.0 shipped sweeping breaks as a minor bump under break-freely (2026-06-23, f983dd41, transcript).
- CLI binary distribution in the formal release flow (GitHub Release assets, install story): explicitly deferred/parked, to be revisited — a known gap, not drift (2026-06-13, 65667e33, transcript, user: "not yet. we'll come back to that.").

## Corrections and restorations (drift-fight record)

- rimsky-entrypoint hard-coded the three-role spawn and ignored the container command, so multi-container deploys ran all roles everywhere, supervisors advertised 127.0.0.1, and cross-container callbacks failed — while CLAUDE.md and the Dockerfile claimed role-by-command worked. Ruled fix-code plus fix-docs; role honoring and the migrate story were settled (2026-06-02, rimsky-core-remediation, artifact).
- CLI auth subcommands landed in package main with duplicate HTTP-client helpers, inconsistent with the shared control CLI client — a known organizational inconsistency caused by literal plan file placement; feature-index.md also kept a stale module row (2026-05-15, control-plane-mcp-and-auth, artifact).
- The 2026-06-11 stabilization diagnosis: remaining instability was hand-paired duplicate code paths plus wire-contract-vs-runtime gaps, resolved by consolidating duplicated paths and closing gaps in the contract's favor (2026-06-11, last-mile-stability, artifact).

## Superseded / historical

- `--key` meaning instance key on `instance create`/`run` → renamed --instance-key; --key means API key globally (2026-05-15, artifact).
- rimsky-cli binary name → `rimsky`, no alias shim (2026-05-15, artifact).
- concept:rimsky's CLI definition as thin HTTP client only → mutated to "thin HTTP+JSON control-api client plus an embedded one-shot orchestration mode that self-hosts the runtime stack" (2026-06-13, transcript).
