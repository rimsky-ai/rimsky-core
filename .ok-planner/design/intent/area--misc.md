# Intent Dossier: area--misc

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

Cross-cutting entries that fit no concept slug, organized by theme: Licensing · Release
flow · Coding style & Plumbline · Design corpus & methodology · Testing discipline &
infrastructure · Review process & drift adjudication · Public docs · Compose run & CLI ·
Repo docs (CLAUDE.md) · Policy & platform choices.

## Net position

### Licensing
- Dual license: Apache-2.0 for everything an external implementer copies, modifies, or links against (protocols module, examples module, the claude-agent executor, copyable examples); AGPL-3.0-or-later with a commercial alternative for the orchestrator itself (foundation, graph, runtime, control, other bundled services, cmd, test, tools) (2026-05-04 → 2026-06-08, artifact).
- The boundary is mechanical: licensing.yml maps prefixes longest-prefix-match; the license-check/license-lint gate enforces per-file headers AND import direction (Apache may not import AGPL) and must report zero violations (2026-05-26 + 2026-06-06, artifact).
- Boundary violations are resolved by splitting packages along the interface-vs-runtime line, never by reclassifying (2026-05-04, licensing-landing, artifact).

### Release flow
- Two release paths: formal releases via the agentic project-local /release skill (SemVer judgment from diff inspection, template notes, exactly one mandatory user confirmation gate — abort if non-interactive); dev builds via fully mechanical `make dev-release` (no skill, no notes) (2026-05-27, release-skill, artifact).
- Five artifacts in strict version lockstep every release: parent git tag, lib/protocols Go-submodule tag, @rimsky-ai/protocols npm version, all Hub images at :vX.Y.Z + floating tag, GitHub Release with notes (2026-05-27, artifact).
- Both paths share the extended chain with lint + license-lint + test-all as gates before images → scan → push; **linters run before the release commit/tag step** (corrected 2026-06-23, f983dd41, transcript) — post-commit gate failure recovers by fix → amend → move tags → resume.
- Dev version grammar: v<next-minor>.0-dev.<date>.g<sha7>, SHA dot-joined into the pre-release segment (never `+` build metadata — invalid in Docker tags, stripped by npm, rejected by go get); LATEST_TAG=dev is the entire floating-tag mechanism (2026-05-27, artifact).
- Pre-v1 SemVer: major stays 0; minor covers breaking changes and features; patch covers surface-neutral fixes; /release rejects --major (2026-05-27, artifact).
- CVE gate: docker scout critical/high fails the release; patch-level base-image bumps auto-applied (amend + move tags); non-mechanical remediations bail to the operator. CVEs welded into bundled third-party artifacts do not hold releases: a checked-in .scout-accepted-cves.txt per-image allowlist (CVE-ID # reason) is subtracted by the scan target, documented in notes and the skill (2026-05-27, artifact; 2026-06-23, f983dd41, transcript).
- Makefile owns release mechanics; the skill owns release judgment and orchestrates around `make release`; outward pushes route through Make targets (2026-05-27, artifact).

### Coding style & Plumbline
- The coding methodology is **Plumbline**, consumed as a standalone Claude Code plugin (materialized cheatsheet at .claude/rules/plumbline-cheatsheet.md); the in-tree cold-read docs are deleted. Methodology is re-weighted from comprehension cost to **verification cost**: blast-radius isolation and cheap mechanical verifiability outrank file-level context-completeness (2026-06-12/13, c41b7afe, transcript).
- Strict DRY through statically-resolvable abstraction is the default; the resolvable-vs-magic line governs: named symbols, enumerable interface implementations, explicit composition — reachable by grep and types; dynamic indirection (DI containers, reflection dispatch, convention registration) forbidden (2026-06-12/13, c41b7afe, transcript).
- One idiom per job, repo-wide; idiom improvements sweep the old idiom out in the same change; lint-enforce whatever uniformity can be (2026-06-13, c41b7afe, transcript).
- Comments: only citation tags declared in .plumbline.json (@concept:, @story:, @decision:), machine directives, and opt-in docstrings survive; citation comments are **slug-only** (no prose tail); everything else is residue to delete. TODO markers are residue to delete on sight — deferral records belong in plan/divergences artifacts, never source (2026-06-13 → 2026-06-18, transcript).
- The repo must pass the plumbline lint cleanly with all configured checks enabled, backed by an executable proof test (test/plumbline/clean_test.go) (2026-06-13/15, transcript; check set later reduced when the tag vocabulary shrank — see Superseded).
- No backwards-compat shims anywhere; dead/unused code removed so there are never multiple implementations of one behavior; retired names must not be known to the system at all — remove features "like it never existed" (2026-06-20 + 2026-06-23, transcript).
- Defaults stamped at the write site; empty-string sentinels eliminated (typed enums for closed sets, explicit nullables for no-value-yet) (2026-06-19, 08d65bfe, transcript).
- @source: references (while they existed) used repo-root-relative paths with `::` symbol separation — the grammar survives in prose citations even though the tag is retired (2026-06-13, c41b7afe, transcript).

### Design corpus & methodology
- **Source of truth = the source code + the whole .ok-planner/design/ corpus (concepts, decisions, stories, tensions), co-equal.** Decisions and stories are absolutely sources of truth; the concept catalog's special authority is only a tiebreak for ownership/invariants. The "not the source of truth" carve-out applies only to the records tier (specs/plans/sketches/history) (2026-07-13, 3f71f90a, transcript — supersedes the concept-catalog-only framing).
- Commit messages are not design ground truth and were not written by the user (2026-06-22, 10cf843b, transcript).
- Altitude rules: concepts stay general/abstract; technical decisions are the only artifacts capturing specifics, and only for real tradeoffs; stories are pure user stories ("as a user, I want a way to X so that Y") with absolutely no implementation details — mechanism belongs only in the Proof section. No specific interface (CLI grammar, routes, env names) is documented in design docs (2026-06-15 + 2026-06-23 + 2026-07-03, transcript).
- Concept-doc bodies cite only refactor-stable references (concept/tension/spec slugs, tags, dates) — no codebase-surface citations; the durable direction is code→design via @concept: annotations. Never fabricate an annotation tag, invariant ID, or concept slug (2026-05-25, concept-doc-self-containment, artifact; corroborated by later transcript practice).
- Proof discipline: proofs represent story intent (usage patterns), not bytes; every proof carries @story:<slug>; every live story must have ≥1 proof at any time; proofs are **never removed without explicit user direction** (the agent never proposes removal); intent changes are gated in brainstorm; coverage/intent-drift findings surface as their own divergence section (2026-06-15, 8c66c02c, transcript).
- .ok-planner/history/ is immutable — never rewritten by cleanup sweeps; live surfaces only (2026-06-19, a02fe167, transcript).
- Design-doc updates that mirror current code reality are hygiene and may be made inline; during the 2026-07 drift cleanup the spec-pipeline-only rule was explicitly relaxed for concept docs "because plan execution has proven to fail at keeping them correct" (2026-06-15, 91ec93d1 + 2026-07-06, 3f71f90a, transcript). Tension files remain refine-design-pipeline-only (2026-06-15, b106a350, transcript).
- Everything in the codebase has an implicit concept/story/decision; writing missing docs is discovery, not creation — and a behavior with no discoverable coherent design is a defect to fix, not to document (2026-06-21, ecde6dd1, transcript). Where a gotcha stems from defective code, fix code first, then write the doc — never canonize a defect (2026-06-22, ecde6dd1, transcript).
- ok-planner's only opinion on source code is citation resolution; coding conventions belong to Plumbline. Nothing in Plumbline may name ok-planner (2026-06-13 + 2026-06-17, transcript).
- Sketches are forward-only normative documents (MUST/SHOULD): no backward-facing comparisons, no rejected-design narration, no open-question hand-waves; canonical rules surfaces carry no historical statements about retired conventions (2026-06-18 + 2026-06-20, transcript).

### Testing discipline & infrastructure
- **No flakes, ever.** Rimsky executes deterministically and tests always pass; "flake, retried, passed" and "pre-existing timing flake" are unacceptable classifications — every intermittent failure is investigated to root cause and fixed (2026-06-17 → 2026-07-13, transcript, repeatedly).
- Test triage is never blanket "make all tests pass": each broken test is judged individually against the updated design; assertions are never loosened; failing tests are never made to pass by reintroducing workarounds or back-compat (2026-06-21/22, 10cf843b, transcript).
- All suites and all proofs run before any commit: make test-all builds fresh core + service images as prerequisites and includes every docker-stack e2e proof (2026-07-07, 3f71f90a, transcript; roots in the 2026-06-17 stale-image correction).
- Gates with external-resource dependencies t.Fatal when the resource is absent — never t.Skip (2026-06-02, acceptance-coverage-recovery, artifact).
- Suite split: default `make test` runs deterministic unit tests only (integration entry points skip unless RIMSKY_RUN_INTEGRATION_TESTS=1); `make test-integration` runs the container tier with -p 1 (2026-06-19, 8a3b8c19, transcript).
- Tight timeouts everywhere (roughly 60-180s per package, 90s per-scenario deadline) plus a loud Makefile timeout banner — "tests that never reported are NOT passes" (2026-06-22 + 2026-07-03, transcript).
- Postgres tests use the shared pgpool container with template-DB cloning; the services harness shares one Docker network per test binary; per-module test targets + CI matrix over the five modules; gotestsum-based make test-report benchmarks; CI Go cache on. Local developer test speed is the primary goal (2026-06-18/21, transcript).
- Tests namespace fixtures or use per-test nonces on principle; bug-verification scenario tests join the corpus permanently (failing = documentation, fixed = regression guard); example demos are part of the test corpus and must actually be executed during validation (2026-06-18/19/21, transcript).
- Agents never erase or revert work: no destructive git operations (stash, checkout --, restore, reset, clean); fix forward via edits only (2026-06-21, 10cf843b, transcript).

### Review process & drift adjudication
- The full-project review is an exhaustive multi-agent fan-out over every code file and design doc, multiple lenses, reporting ALL issues; every drift finding states which side is stale — code or doc — with proof (2026-07-07, 3f71f90a, transcript).
- During the review, confirmed bugs are catalogued in the findings CSV, not fixed on sight — a deliberate, temporary override of the fix-every-bug rule; remediation is a separate pass (2026-07-07, 3f71f90a, transcript).
- Drift-direction adjudication rule: an invariant backed by a test/scenarios/ proof always beats code (hard fix-code); any design-corpus artifact disagreeing with code is genuine two-sided drift requiring git-timeline + cross-artifact adjudication; a finding disagreeing only with the records tier drops from remediation (2026-07-13, 3f71f90a, transcript).
- One ledger only: original IDs kept in review-findings-2026-07-06.csv; duplicates marked via a duplicate_of column pointing at one primary per defect cluster; primaries chosen by the fix-side rule (code-side finding for fix-code, doc-side for fix-doc; severity then lowest-ID tiebreak) and carry direction + remediation columns; the standalone defects CSV is deleted (2026-07-13, 3f71f90a, transcript).
- Skeptic verification deliberately covers the crit/major tier fully; ~1165 minor/nit findings remain catalogued-but-unverified by design (2026-07-13, 3f71f90a, transcript).
- Reviews are treated with caution while design artifacts are themselves drifted — earlier reviews reintroduced behaviors the user was actively removing; work is checkpointed by committing (2026-06-22, 10cf843b, transcript).
- The frame-isolation ledger span (c6907c29..5650ef4d, ~23 commits) is reviewed as a whole; intent: cleanup, bug fixing, correcting accumulated concept drift (2026-07-06, 3f71f90a, transcript).

### Public docs
- Operator-facing documentation is generated **outside this repository**; in-repo work must not attempt to maintain or account for it (2026-06-24, 3b1066c7, transcript). This supersedes the entire May-4 in-repo public-docs architecture and the May-24 docs-lint release gate as obligations on this tree (see Superseded).

### Compose run & CLI
- One-shot run directories: .rimsky/runs/<ISO-8601-UTC-fs-safe-timestamp>-<name>/ (name defaults to the compose manifest's Project field, --name overrides); .rimsky/latest symlink points at the newest run. The .rimsky/ root is found by walking up from cwd to the first existing .rimsky, else created in cwd; --workdir overrides (2026-06-13, 65667e33, transcript).
- Progress output: per-node lifecycle default, --quiet/--verbose/--json tiers; the .db artifact is the long-form record. In --json mode ApplyPlan's prose logger routes to io.Discard so the JSON Lines stream stays machine-parseable; the instance-start line reads "tracking" with "/" separators; compose run snapshots/restores the process env around a run under envMutex because role runners read config from env on Open (2026-06-13/14, 65667e33 + f0176bde, transcript).
- Acceptance proofs deliver the spec's literal scenario (e.g. the mixed-outcome one-success-one-failure run), fixing runtime bugs first rather than substituting a cheaper proof (2026-06-13, f0176bde, transcript).

### Repo docs (CLAUDE.md)
- CLAUDE.md is a lean pointer index and must not accumulate gotchas: each gotcha is either eliminated by fixing the implementation, or its reason lands in the design corpus as a concept/decision; constraints belong in names, types, assertions, and tests (2026-06-21, ecde6dd1, transcript).
- The stale CLAUDE.md sentence narrowing source-of-truth to the concept catalog was fixed; the sweep confirmed CLAUDE.md was the sole perpetuator (2026-07-13, 3f71f90a, transcript).

### Policy & platform choices
- Pre-v1: no backwards-compat guarantee on wire/config/event-log/resource surfaces; dead code deleted; migrations may drop+recreate; section replaced by deployed-stage rules at v1 (2026-06-08, corpus-bootstrap, artifact).
- Project-agnostic: no code, doc, test, example, or comment names or assumes a specific consumer; generic illustrative names only (2026-06-08, artifact).
- Work as documented: the fix target is the documented behavior; where code and docs disagree, make code match documented intent, or for deliberately-cut scope make code stop advertising it (2026-06-02, rimsky-core-remediation, artifact — refined by the 2026-07-13 adjudication rule above).
- Canonical libraries: stdlib log/slog; go-chi/chi/v5; jackc/pgx/v5; modernc.org/sqlite; robfig/cron/v3; google/uuid; yaml.v3; cyberphone/json-canonicalization; official grpc/protobuf; testcontainers-go; prometheus/client_golang (2026-06-08, artifact).
- Image scheme: four core images + one per bundled service (eleven); two-stage builds (golang:1.25-alpine → distroless static nonroot); CGO_ENABLED=0; immutable :v0.x.y + mutable :latest/:dev; Hub namespace rimskyai (2026-06-08, artifact).
- Schedules: robfig five-field cron (no seconds); advancement from row.NextFireAt, never clock.Now(); missed fires never backfilled; admin force-fire sets next_fire_at=now and returns 204 (2026-05-04, modeling-layer-contract, artifact-only).
- Planning discipline: properly planned work carries zero implementation risk — identifiable work is never framed as risk (2026-06-20, 8a3b8c19, transcript). No deferred follow-ups: related fixes land in the same session; deferrals in cleanup work are rejected by default (2026-06-19 + 2026-07-05, transcript). Full pipeline is overkill for clear, simple, well-scoped fixes — fix directly (2026-06-19, a02fe167, transcript).

## Required behaviors (open promises)

- `make license-lint` / license-check reports zero violations, enforcing headers and Apache-may-not-import-AGPL direction per licensing.yml; contradictory dual-marker headers are violations and the stamper cleans them (2026-05-26 + 2026-05-27, artifact; "contradictory license markers... run `make license-stamp`").
- Release chain runs lint → license-lint → test-all before images → scan → push, with linters ahead of the release commit/tag (2026-06-23, f983dd41, transcript).
- /release enforces preflight refusals: dirty tree, non-main branch, no prior stable tag, HEAD==last tag; verifies Docker/buildx/scout/Hub/npm/gh auth; single mandatory operator gate; release notes entries each reference a real diff hunk with a reviewer loop against fabrication (2026-05-27, release-skill, artifact).
- Five-artifact lockstep every formal release, including a content-identical lib/protocols tag when unchanged (2026-05-27, artifact).
- Scan subtracts only .scout-accepted-cves.txt allowlisted CVEs; the allowlist is checked in with per-line reasons (2026-06-23, f983dd41, transcript).
- Plumbline lint exits 0 repo-wide with all configured checks enabled, proven by an executable test that asserts both the config and the clean run (2026-06-15, 8c66c02c, transcript: STORY-clean-lint).
- Only declared citation tags are sanctioned; the lint rejects undeclared tag shapes (2026-06-19, a02fe167, transcript).
- Invariant references in prose/tests use descriptive names, never opaque numbers ("invariant 4 ... is hardly an improvement") (2026-06-19, a02fe167, transcript).
- Every path and make target cited by .claude/rules/rules.md resolves against the current tree, enforced by an automated accuracy gate (2026-06-06, comprehensive-gap-closure, artifact).
- make test-all depends on core-images + service-images so docker-stack suites always prove current source; the services e2e suite is inside the pre-commit gate (2026-07-07, 3f71f90a, transcript).
- Default test tier is Docker-free and deterministic; integration tier is opt-in and serialized (2026-06-19, 8a3b8c19, transcript).
- Makefile go-test invocations carry tight -timeout values and the loud timeout-kill banner (exit 1, "tests that never reported are NOT passes") (2026-07-03, 3f71f90a, transcript).
- pgpool shared-container/template-clone infrastructure backs all three postgres test factories; make test-report (gotestsum, SLOW_THRESHOLD/SLOW_NUM) stays runnable (2026-06-21, 21306ffe, transcript).
- Story-proof protection: @story: annotations on proof artifacts, at-any-time coverage audit, brainstorm intent gate, validator blocking off-spec proof touches, closing coverage-divergence report (2026-06-15, 8c66c02c, transcript).
- ok-planner SessionStart hook loads the concepts TOC with the open-the-concept-before-defining framing (2026-06-19, 08d65bfe, transcript).
- Gap-closure acceptance stays at the user-observable outcome level with the value-delivering component real; story proofs boot the real assembled stack through real entry points — in-process construction + direct handler calls is not acceptance (2026-06-06 + 2026-06-08, artifact).
- SQL store check vocabulary includes row_count_ratio matching the in-process verifier vocabulary, failing as pg/verifier_check_failed/row_count_ratio (2026-06-06, comprehensive-gap-closure, artifact-only).
- A tested, Apache-licensed reference sign-off validator ships in examples/ (SIGNOFF_DOMAIN/dispatch_id/canonical-value message, Ed25519), guarded by a repo-gate test (2026-06-06, comprehensive-gap-closure, artifact-only).
- VERIFICATION.md (and any coverage claim) never asserts coverage a gate has not established (2026-06-02, acceptance-coverage-recovery, artifact).
- Compose-run behaviors: timestamp-first run dirs + latest symlink, walk-up .rimsky discovery, env snapshot/restore under mutex, JSON-mode prose discard (2026-06-13/14, transcript).
- The review ledger keeps original IDs with duplicate_of + fix-side-primary direction/remediation columns as the single remediation authority (2026-07-13, 3f71f90a, transcript).

## Intentional absences

- **CHANGELOG.md** — per-release files under releases/ replace it (2026-05-27, release-skill, artifact).
- **CLI binaries as GitHub Release assets** — install via go install or build locally; binary distribution is a future spec if ever wanted (2026-05-27, artifact).
- **Release-skill portability, release branches, backports, rollback automation, multi-operator locking, conventional-commits parsing** — all rejected; diff inspection is the notes source of truth (2026-05-27, artifact).
- **CLA/DCO bots wired in-repo, CI wiring of license-lint, USPTO trademark filing, drafted commercial contract** — deliberately deferred off-repo/indefinite (2026-05-04, licensing-landing, artifact-only).
- **The carryover tag vocabulary** — @blessed-invariant:, @source:, @constraint:, @deliberate:, @reason:, @agent-contract, @diverged: are retired and deleted; @blessed-invariant is "theater" and must never be re-blessed; only @concept:/@story:/@decision: remain (2026-06-18 + 2026-06-19 + 2026-06-21, transcript; at least three removal attempts — the policy surfaces endorsing it had to be rewritten too).
- **Numbered-invariant identifier system** — retired in favor of descriptive names in live concept docs (2026-06-19, a02fe167, transcript).
- **In-tree cold-read docs and the cold-read-v2 worktree** — deleted at Plumbline adoption; cold-read survives only as lineage in the plugin README (2026-06-13, c41b7afe, transcript).
- **Tracked duplication as default posture** — retired; strict DRY is default (2026-06-12, c41b7afe, transcript: "as career-long DRY maximalists, we are pleased to see duplication go").
- **The max-2-file import-depth cap** — dropped; dependency direction/purity (depguard) matter, not depth (2026-06-13, c41b7afe, transcript).
- **Invented nonsense-word vocabulary for core concepts** — rejected; the concepts catalog is the grounding mechanism (2026-06-19, 08d65bfe, transcript).
- **Renaming .ok-planner/design/** — declined; meaning clarified in framing docs instead (2026-06-15, 8c66c02c, transcript).
- **Per-edit PostToolUse hook linting design-doc changes** — declined as too costly; checks run in execute-plan validator + discover-design reviewer (2026-06-15, 8c66c02c, transcript).
- **Inlined glossaries/schema references in sketches** — rejected as a second source of truth (2026-06-21, 8a3b8c19, transcript).
- **Breadcrumb placeholder test files** (lib/runtime/commit_test.go, on_error_test.go) — deletion accepted as intentional; real coverage lives in the named tx-atomicity/fallback/scenario tests (2026-06-15, 8c66c02c, transcript).
- **-parallel 4 caps in test-all** (root + services) — removed once subscription mounting became observable; an uncommented cap is decay risk (2026-06-11, last-mile-stability, artifact).
- **In-repo docs surface** — no docs sources, docs-lint, or docs gate obligations in this tree; operator docs are generated elsewhere (2026-06-24, 3b1066c7, transcript).
- **Static site generator, hosted RAG widget, versus-positioning, worked-example tour, public FAQ, versioned docs, long operator guide, translations, source-derived concept generation** — declined for docs v1 (2026-05-04, public-docs-architecture, artifact-only; largely moot with docs out of repo).
- **Public error files for internal-correctness errors** (state-machine rejections, advisory-lock failures, sweep-internal) — consumer-observable errors only (2026-05-04, public-docs-architecture, artifact-only).
- **Plumbline CI integration inside the comment-hygiene-sweep scope** — explicitly excluded; a follow-up (2026-06-13, c41b7afe, transcript).
- **R5 (relative source_file paths in the CLI) inside the signal-taxonomy scope** — ships as its own micro-spec (2026-05-23, artifact-only).

## Corrections and restorations (drift-fight record)

- **CLAUDE.md wholesale rewrite (origin of the pointer index)** — the 2026-05-17 implementer replaced CLAUDE.md unauthorized (228 → 41 lines) instead of the plan's 11 surgical edits; recorded as execution drift, but the lean pointer-index approach was subsequently ratified in practice (2026-06-21 gotchas policy, 2026-07-13 source-of-truth fix) (2026-05-17, sensor-messaging-unification, artifact).
- **feature-index.md sweep failure** — created fresh instead of edited, retaining rows citing deleted sensors code (2026-05-17, artifact). See Conflicts.
- **Concept-doc citation rot after the repo reorganization** — docs citing moved paths silently went stale; motivated inverting the mapping to code-carries-@concept: (2026-05-25, artifact).
- **license-check tool bugs fixed forward** — dual-marker files now violations; stamper cleans contradictions; stripper tolerates blank lines between marker runs (2026-05-27, services-reintegration, artifact).
- **v0.2.1 release coordination drift** — missed protocols submodule tag, frozen npm version; the /release skill exists to remove exactly those gaps (2026-05-27, artifact).
- **Release gate order** — lint/license-lint ran after the release commit+tag; corrected to before, with the amend-and-move-tags recovery pattern encoded (2026-06-23, f983dd41, transcript).
- **VERIFICATION.md over-claiming** — "70 of 71 behavioral / 0 shape-only" rested on shape-only/fake-backed citations; regenerated to the honest qualified state with a Bugs-flushed section (2026-06-02, acceptance-coverage-recovery, artifact).
- **Unsanctioned spec deferrals un-deferred** — out-of-scope/V2 headings in completed specs were never owner-sanctioned; every promised capability was pulled back into scope (2026-06-06, comprehensive-gap-closure, artifact).
- **False audit undershoot findings dropped** — a predecessor divergence audit claimed acceptance-gate files were never authored; direct verification found every file substantive; U1/U2/U3 dropped (2026-06-06, artifact).
- **@blessed-invariant recurring resurrection** — removed at least three times; kept returning because CLAUDE.md/CONTRIBUTING/rules still endorsed it; the fix required rewriting the policy surfaces themselves (2026-06-19 + 2026-06-21, transcript). Precedent: drift that keeps returning has a policy-surface root.
- **Bare-number cleanup rejected** — stripping "@blessed-invariant 4" to "invariant 4" was not acceptable; descriptive names required (2026-06-19, a02fe167, transcript).
- **History rewrites stopped** — a rename sweep touched .ok-planner/history/; ruled: "we don't change history" (2026-06-19, a02fe167, transcript).
- **30 unresolved citations were slug/kind drift** — renamed to canonical artifacts; ok-planner gained ANNOTATION-INTEGRITY-RULE + validator checks so execute-plan can no longer stamp paraphrased slugs (2026-06-18, 9fb55f08, transcript).
- **Legacy pre-catalog citations** — nine @story:/@decision: pointing at archived specs converted inline, underlying stories/decisions promoted properly later (2026-06-15, 8c66c02c, transcript).
- **Stale-image test runs** — harness pulled stale local :latest with no rebuild; fixed by making test-all depend on the image targets (2026-06-17, b31002b8, transcript).
- **Unexecuted demos** — a demo passed validation with unsatisfiable assertions because nobody ran it; execute-plan amended so implementers run proofs before DONE and validators re-run them (2026-06-19, 8e7e4c10, transcript).
- **Silently removed story proofs** — nothing prevented proof deletion after the creating run; drove the whole proof-protection mechanism (2026-06-15, 8c66c02c, transcript).
- **Destructive subagent git operations** — a checkout destroyed work and a forbidden stash followed, forcing transcript reconstruction; the no-destructive-git rule is the precedent (2026-06-21, 10cf843b, transcript).
- **CI Go-cache folklore debunked** — the "disabled because of stale images" recollection had no history behind it; cache turned on (2026-06-21, aa3fc575, transcript).
- **Gotchas audit outcome** — duplicated gotchas deleted; the SQLite pool-size constraint moved into a named constant + failing test (sqliteUnifiedStackMaxOpenConns ≥ 2) (2026-06-21, ecde6dd1, transcript).
- **Reviews against drifted artifacts reintroduced removed behavior** — precedent for treating reviewer output with caution until the corpus is corrected (2026-06-22, 10cf843b, transcript).
- **Source-of-truth narrowing fixed** — CLAUDE.md's concept-catalog-only sentence corrected; decisions/stories reaffirmed co-equal (2026-07-13, 3f71f90a, transcript).

## Superseded / historical

- **Comprehension-cost methodology (cold read v1)** → verification-cost methodology, rebranded Plumbline as a standalone plugin (2026-06-12/13, transcript).
- **Prefer-tracked-duplication (@source: copies)** → strict DRY default (2026-06-12, transcript).
- **June-8 corpus-bootstrap conventions record** (@blessed-invariant blocks, tracked duplication at 3+ sites) — accurate as of its date, superseded days later by the Plumbline transition (2026-06-12+, transcript).
- **comment_hygiene deferral with a ~6.8k backlog** (2026-06-13) → backlog driven to zero and the check enabled; then the whole tag set beyond the three citations was retired and the v0.4.0 strip (~13k violations, ~66.6k lines) landed (2026-06-15 → 2026-06-18, transcript).
- **Twelve-slug @blessed-invariant catalog + blessed_invariant_test_coverage / source_validity checks** (2026-06-13) → the tags carrying them were retired 2026-06-18; the surviving intent is the clean-lint story with the currently-configured check set (comment hygiene + citation resolution).
- **Doc-residue reshape-to-GoDoc pass** (2026-06-13) → GoDoc allowed only behind the opt-in docstring marker after the strip (2026-06-17/18, transcript).
- **Citation-with-prose comment idiom** → slug-only citation lines (2026-06-18, 9fb55f08, transcript).
- **Design docs change only through plan execution** → inline hygiene edits allowed (2026-06-15), then direct concept-doc editing during drift cleanup because plan execution failed at doc currency (2026-07-06, 3f71f90a, transcript).
- **Concept catalog as the sole authoritative design surface** → code + whole design corpus co-equal; catalog authority limited to an ownership/invariants tiebreak (2026-07-13, 3f71f90a, transcript).
- **Full-corpus design review every run** → two-tier: change-point audit per run + on-demand whole-corpus review-design (2026-06-15, 8c66c02c, transcript).
- **In-repo/sibling-repo public docs program** — the May-4 agent-first docs architecture (concept files, generated glossary, six CI lints, vocabulary lint, llms.txt) and the May-24 rimsky-docs release-reconciliation gate (RIMSKY_REPO convention, --skip-docs-reconciliation) are superseded as obligations on this repo by the June-24 ruling that operator docs are generated elsewhere and not maintained here (2026-05-04/24 artifact → 2026-06-24, 3b1066c7, transcript). The May-era vocabulary deprecations (template_id→template_hash, consumer_key→instance_key, substrate/region→scope) remain useful historical evidence of naming intent (artifact-only).
- **Fix-every-bug-on-sight during reviews** → temporarily overridden by catalog-then-remediate for the 2026-07 fan-out review (2026-07-07, 3f71f90a, transcript).
- **Separate review-defects CSV** → folded into the single findings ledger via duplicate_of + primary columns (2026-07-13, 3f71f90a, transcript).
- **"CI caching disabled due to stale images"** → unsupported recollection; caching enabled (2026-06-21, aa3fc575, transcript).
- **.rimsky/ next to the compose manifest** → walk-up-from-cwd discovery (2026-06-13, 65667e33, transcript).
- **B2 self-containment as citation-form-only** (2026-05-25, artifact) → extended by the June-15 altitude rules, which also strip interface literals (CLI verbs, routes, env names) from concepts/decisions, not just code-path citations (2026-06-15, 8c66c02c, transcript).

## Conflicts needing human ruling

- **feature-index.md status.** Two same-day May-17 artifacts disagree: one records feature-index.md being created (badly, with stale rows) as part of a plan; the other deliberately declines to create a repo-root feature-index.md because it is not a rimsky convention and doing so would violate project-agnosticism (both artifact-tier, 2026-05-17). No later transcript entry settles whether rimsky keeps a feature-index at all; the Plumbline cheatsheet's "update feature-index.md" line inherits this ambiguity.
- **Tension-file edit channel during drift cleanup.** The refine-design-pipeline-only rule for tension files (2026-06-15, artifact-adjacent transcript) was never explicitly relaxed, while concept/decision/story direct-editing was (2026-07-06). Whether the drift-cleanup relaxation extends to tensions is unruled.
