# Intent Dossier: module-layout

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- The repo root is collapsed into four idiomatic top-level code directories — `cmd/` (binaries), `lib/` (shippable library code), `test/` (out-of-tree tests + machinery), `tools/` (dev tooling) — with no `src/`, no top-level `internal/` (2026-05-27, root-folder-reorg, artifact). The module-layout concept owns only these four code groups; non-code top-level entries (e.g. `releases/`, image build inputs) coexist outside its invariants (2026-05-27, release-skill, artifact).
- The Go workspace (go.work) ties root, `lib/foundation`, `lib/protocols`, `lib/services` — plus an `examples` module promoted to a full workspace module that builds/lints/tests in the workspace gate, holding copy-and-modify reference implementations, protocols-only, Apache-licensed (2026-06-08, corpus-bootstrap, artifact-only for the examples module).
- The root module is layered foundation → graph → runtime → control; each layer's purity depguard forbids importing higher layers; a documented per-file exemption lets graph/scheduler orchestrate runtime sweeps, plus a full exemption for the scenario-boot test scaffolding (2026-05-13, nomenclature-resolution, artifact).
- `lib/protocols` is the single public Go surface a service implementer imports: dependency-lean (stdlib + grpc + protobuf + uuid + yaml.v3 only, no DB driver, no test infra), enforced by protocols-purity. Contract packages keep bare names; convenience scaffolding takes a `kit` suffix (2026-05-26, collapse-sdk-into-protocols, artifact).
- `lib/services` is its own module requiring only `lib/protocols`, so the module graph itself enforces consumption-side isolation; the consumption-side-isolation depguard is retained as defense-in-depth. Its integration tests live inside the module at `lib/services/test/`; per-service Dockerfiles are co-located (2026-05-27, services-reintegration, artifact).
- `cmd/` stays at the repo root and is the acceptable "top level that references everything": for all-in-one it imports the services module to register bundled handlers in-process (2026-05-27, artifact; 2026-07-01, 8a8539a4, transcript).
- Bundled executors are ported to Go; gRPC is a shim over the standard in-process interface, so one handler codebase serves both in-process (all-in-one) and standalone-image deployments (2026-07-01, 8a8539a4, transcript).
- Enforced boundaries are lint-authoritative: if prose and lint disagree, lint wins. Depguard covers layer ordering, pgx isolation, foundation/internal privacy, protocols purity, consumption-side isolation (2026-06-08, corpus-bootstrap, artifact).
- Validation discipline must sweep every module (make test-all or equivalent); `go test ./...` from root silently skips submodules under go.work (2026-06-24, 8a8539a4, transcript).
- Tracked-duplication-with-`@source` is the sanctioned pattern for types that cannot cross a depguard boundary (e.g. the verifier's checks.Severity duplicate of the foundation enum) (2026-06-06, comprehensive-gap-closure, artifact).

## Required behaviors (open promises)

- Layer boundaries are compile-enforced via the multi-module go.work split, not conventions (2026-05-04, layer-crystallization, artifact): "Enforce the foundation/protocols boundaries via Go module structure, not just package conventions."
- Protocols module carries zero non-stdlib deps beyond grpc/protobuf (later widened to + uuid + yaml.v3), so external authors implement a protocol without rimsky's transitive deps; protocols-purity additionally denies testcontainers (2026-05-04 artifact; tightened 2026-05-26, collapse-sdk-into-protocols, artifact).
- protocols/claimproducer is the canonical and only Go home for claim-producer contract types; no internal module re-exports them (the 17 foundation/locks aliases removed) (2026-05-26, artifact).
- Depguard rule set stands: foundation → graph → runtime → control ordering; pgx confined to the postgres driver + services/cmd/test; foundation/internal private; lib/services imports only lib/protocols; graph pure except the documented scheduler exemption (2026-06-08, corpus-bootstrap, artifact).
- Consumption-side-isolation globs are root-anchored (and lib/services-anchored) so rimsky-internal scenario tests are not falsely flagged; the rule is kept even while it has no violating matches, as a guard against re-bundling (2026-05-24, repo-reorganization, artifact).
- The load-bearing test-infrastructure carve-out: stub executor, stub store, and the filesystem/postgres test-fixture packages are rimsky-internal fixtures and stay in core, distinct from user-facing reference impls (2026-05-24, repo-reorganization, artifact).
- Public import paths carry the `lib/` segment (github.com/rimsky-ai/rimsky-core/lib/protocols); no compatibility shims; settled with the user and marked do-not-re-litigate (2026-05-27, root-folder-reorg, artifact).
- Conformance runners are `rimsky conformance <protocol>` subcommands of the single CLI binary; the conformance image runs them; no standalone conformance binaries (2026-05-27, root-folder-reorg, artifact).
- Generated proto bindings must be regenerated from .proto sources (make proto-gen), never string-rewritten — textual editing corrupted the embedded descriptor during the reorg (2026-05-27, root-folder-reorg-divergences, artifact).
- CI/branch protection on main runs the multi-module Makefile targets (build-all / test-all / lint), golangci-lint pinned v1.64.8 for the v1-style config (2026-06-02, rimsky-core-remediation, artifact).
- Every validation pass runs all tests across all modules — make test-all, never a root-only `go test ./...` (2026-06-24, 8a8539a4, transcript): "we thought that all tests got run every vallidation pass ... how is it that we are now discovering failing tests?"
- Bundled-service handler code lives in handler packages alongside each service's main within the services module; cmd imports services to register in-process for all-in-one (2026-07-01, 8a8539a4, transcript): "not unreasonable for the cmd layer to be the 'top level' that references everything."
- In-proc registration: bundled executor handlers reuse the utility-node InProcessRegistry, a parallel in-proc registry exists for claim producers, and a `lib/services/bundled` RegisterAll entrypoint registers all bundled handlers — unconfigured services skip with a log line, present-but-invalid config aborts boot naming the handler, config-declared names always win over bundled handlers, bundled discovery entries are static so the refresh loop cannot wipe them (2026-07-01 and 2026-07-03, 8a8539a4/3f71f90a, transcript).
- The `{"list": [...]}` pass-through unmarshal helper lives in lib/services/stores/shared as a de facto cross-producer shape; substrate-specific shapes stay per-store (2026-06-18, 9fb55f08, transcript).
- Examples module: builds/lints/tests as part of the workspace gate, protocols-only dependency, Apache-licensed, one reference implementation per rimsky-implementable protocol (2026-06-08, corpus-bootstrap, artifact-only).
- Comment-hygiene sweep decomposes per top-level module root, with `.plumbline.json` flipping comment_hygiene true only in the final pass (2026-06-13, c41b7afe, transcript).

## Intentional absences

- **A Go SDK module** — sdk/go (born 2026-05-24) was dissolved into protocols; "the SDK was a satellite layer on top of the contract — backwards from the dependency reality." A future development kit is Python-first and must not reintroduce a satellite Go module (2026-05-26, collapse-sdk-into-protocols, artifact).
- **Standalone MCP-server Go module** (mcp-servers/control-api) — retired into control-api proper, deleted with no alias or shim (2026-05-15, control-plane-mcp-and-auth, artifact).
- **Bundled reference stores parquet-store / geo-parquet-store / geo-postgis-store** — CUT in full mid-execution, explicitly not deferred, "no follow-up dispatch revive it": project-agnostic core must not ship naive specialized-format stores (2026-05-15, data-platform-extensions, artifact).
- **testpg as a public/standalone module** — demoted to a plain root-module package at test/support/testpg, reclassified Apache→AGPL (2026-05-27, root-folder-reorg, artifact; supersedes the 2026-05-26 opt-in-module decision).
- **internal/ops** — dead after the services split; deleted, not relocated (2026-05-27, root-folder-reorg, artifact).
- **Backwards-compat shims** for any module reshape (protocols split, lib/ path change) — pre-v1 clean breaks (2026-05-04 and 2026-05-27, artifact).
- **internal/-fencing of foundation and renaming foundation** — fencing explicitly deferred as sequencing-sensitive; rename rejected outright (2026-05-26, artifact).
- **DRYing the foundation-internal pgtest helper against testpg** — explicit non-goal; tracked byte-identical duplication via @source/@diverged (2026-05-26, artifact).
- **The four-layer model on the public docs surface** — retired per user direction; internal framing only (2026-05-04, public-docs-architecture, artifact): "the four-layer model: who cares?"
- **A separate Go module for bundled handlers** — rejected alternative to Approach A (2026-07-01, 8a8539a4, transcript).
- **Calling-side wire code in the public surface** — rimsky's gRPC clients toward services stay rimsky-internal (runtime/peer, renamed from runtime/remote) (2026-05-24, repo-reorganization, artifact).
- **Four dropped standalone concepts** — licensing-boundary folded into module-layout, mcp-server into control-api, userdata-overrides into userdata, scenario-harness dropped with no fold (2026-05-11, log-convergence, artifact).

## Corrections and restorations (drift-fight record)

- Foundation back-imported graph packages (persistence/integration) despite the claimed boundary — eliminated by extracting persistable row-type primitives into foundation/spec with graph-side aliases, making foundation-purity unconditional (2026-05-13, nomenclature-resolution-notes, artifact).
- Textual rewriting of generated .pb.go import paths corrupted the descriptor; forced correction: regenerate via make proto-gen (2026-05-27, root-folder-reorg-divergences, artifact).
- The module-layout concept doc wrongly claimed a separate MCP-server Go module; the operator MCP shim actually lives at control/controlapi/mcp in the root module — pre-existing concept-doc error corrected by spec (2026-05-24, repo-reorganization, artifact).
- Validation-coverage drift: many passes had run root-only `go test ./...`, silently skipping lib/services and siblings; user ruled every pass must sweep every module (2026-06-24, 8a8539a4, transcript).
- Known recorded warts, unfixed at recording: the CLI auth subcommands landed in package main with duplicate HTTP-client helpers inconsistent with the shared control-CLI client; feature-index.md kept a stale row for the deleted MCP module (2026-05-15, control-plane divergences, artifact).
- The 2026-05-05..05-13 accepted layering violations (supervisor package inside foundation/integration importing modeling) were resolved by the four-layer split moving that machinery into runtime/ (2026-05-13, artifact).
- Annotation note: module-layout was recorded as having no in-repo @concept: site rather than fabricating one (2026-05-25, concept-doc-self-containment, artifact).

## Superseded / historical

- Three modules (root/foundation/protocols, 2026-05-04) → + sdk/go (2026-05-24) → sdk dissolved, + testpg (2026-05-26) → testpg demoted, three modules under cmd/lib/test/tools (2026-05-27) → + lib/services (2026-05-27) → + examples = five (2026-06-08, artifact-only endpoint of the chain).
- Two-way graph/ + control/ split of modeling (2026-05-12 spec) → four ordered layers foundation/graph/runtime/control; graph-control-isolation depguard retired as subsumed (2026-05-13).
- Three-way graph/runtime/control split rejected 2026-05-12 → executed as the four-layer split 2026-05-13.
- Five-repo carve (rimsky-core / rimsky-services / rimsky-docs / crimefinder / rimsky-dashboard, 2026-05-24) → rimsky-services reintegrated as lib/services via plain file copy (2026-05-27); docs remain external.
- `apps/` top-level consumer directory outside the split (2026-05-19) → mooted by the repo carve moving the consumer app out (2026-05-24).
- Per-developer uncommitted go.work for cross-repo dev (spec) → implementer committed replace directives in sibling repos, explicitly overriding the spec (2026-05-24, divergences) → mooted by reintegration (2026-05-27).
- Lockstep root+sdk tagging (2026-05-24) → moot with sdk dissolution (2026-05-26).
- Sensors/subscribers as top-level bundled directories with pgx allowlist extensions (2026-05-15) → subsumed by the services carve-out and reintegration under lib/services (2026-05-27).
- pgtest at top-level internal/ (2026-05-13) → test/support/ under the reorg (2026-05-27).
- Claim-producer Registry stranded in foundation/locks with a planned interface extraction (2026-05-04) → superseded by the contract-type consolidation into protocols/claimproducer, with genuinely internal lock machinery staying in foundation/locks (2026-05-24/26).

## Conflicts needing human ruling

- **RESOLVED 2026-07-14 (user ruling, transcript tier): the workspace is FIVE modules — root, lib/foundation, lib/protocols, lib/services, examples.** go.work and the populated examples/ tree corroborate the artifact claim; nothing was lost. Verified same day: build-all, lint, and test-all all cover the examples module (test-all builds core+service images first so the examples cross-stack proofs run against current source). User mandate ratified: all tests, proofs, and examples run before the codebase is considered correct — `make test-all` (examples included) is the correctness gate. Fix-doc: CLAUDE.md says "four Go modules" and omits examples from its coverage sentence — stale, correct it.
