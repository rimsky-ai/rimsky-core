# Post-remediation residue: findings and proposed fixes

Status: sketch (proposal, not yet planned)
Source: high-level completeness review of the drift-remediation range `4fd0fce7..bf45f5a8`, run 2026-07-21 with four independent read-only reviewers (ledger audit, retired-idiom sweep, docs-vs-code alignment, fresh-eyes hazard probe).

## Review verdict, for context

The remediation itself held up: the archived ledger reproduces exactly (2,434 CLOSED / 151 STALE / 0 OPEN of 2,585), 12/12 sampled CLOSED rows verified genuinely fixed, 8/8 sampled STALE rows check out, lint and plumbline are green, every checkable claim in CLAUDE.md verified accurate, and all 72 `@concept:` slugs cited from code resolve. Project-agnostic naming is clean; no dead exports; no TODO/comment residue in Go; the legacy callback discriminator is extinct.

What follows is the residue — ten findings that survived, each a potential re-drift vector for future sessions (including the planned rimsky-based continuous-review orchestrator). Ranked by likelihood of misleading a future session.

---

## 1. Wall-clock test verdicts are still the dominant dialect

**Finding.** The determinism rule (`.claude/rules/rules.md`) bans wall-clock constants from a test's pass/fail path, but the retired idiom is the majority dialect, in three forms:

- ~52 fail-on-timeout selects: `select { case <-signal: … case <-time.After(d): t.Fatal }` — across every module (e.g. `lib/runtime/breakpoint_eval_test.go`, `cmd/rimsky/cli/watch_test.go`, `lib/protocols/conformance/executor/callback_receiver_test.go`).
- Negative windows: sleep N then assert nothing happened (e.g. `test/scenarios/subscription_cascade_test.go:173`; the whole `test/scenarios/breakpoints/` suite asserts the executor was never called this way). Load-dependent in the inverted direction: a slow machine can pass a broken system.
- Deadline-bounded poll loops that `t.Fatalf` on expiry — the bulk of ~200 `time.Sleep` sites are politeness sleeps inside loops whose exit is a wall-clock deadline, not success-only.

Legitimate uses exist and must be preserved: fixture-shaping sleeps (slow-server fixtures, deliberately-blocked publishers, mtime spacing), and genuinely unbounded success-only poll loops.

**Hazard.** No mechanical check backs the written rule — nothing forbids `time.After` in `_test.go` — so the idiom re-enters with every new test (some sites are in code the remediation itself touched). Load-dependent verdicts are exactly the noise generator a continuous-review orchestrator cannot afford.

**Proposed fix.** A tx-last-style mechanical wave:
- Convert fail-on-timeout selects to bare receives (`<-ch`).
- Convert deadline polls to success-only loops (keep the inter-poll politeness sleep).
- Rework negative windows onto real synchronization points — the hard subset; "prove X never happens" needs an event that closes the window (e.g. drive the system to a later checkpoint that could only be reached after the forbidden event's opportunity has passed, then assert it didn't happen).
- Cap with lint (forbidigo pattern or custom rule) forbidding `time.After` in `_test.go`, with explicit per-site suppressions for the few legitimate fixture uses, so the idiom cannot return.

## 2. store/claim_producer vocabulary split still live in identifiers

**Finding.** Migration 037 renamed the DB columns and the config loader rejects the retired `stores` YAML key, but the retired noun survives one layer up:

- `lib/control/config/claim_producers.go:372` — the function that rejects the `stores` key names its own result `stores :=`; exported signatures `DialLifecycleSubscribers(ctx, stores RemoteClaimProducersConfig, …)` (there and `lib/control/config/publishers.go:60`) carry it as a parameter name.
- `lib/services/test/harness/store_filesystem.go` / `store_postgres.go` — exported `StartFilesystemStore` / `FilesystemStoreSpec` / `StartPostgresStore` / `PostgresStoreSpec` coexist with `DialClaimProducer` / `ClaimProducerClient` in the same package.
- `lib/services/test/scenarios/claim_producers/` declares `package stores`.
- `lib/services/test/smoke/stores_redesign_smoke_test.go` (file name).
- Example templates name producers `fs-store` / `pg-store`.

**Hazard.** A fresh session grepping either noun finds both alive, concludes both are legitimate, and mints new `Store` identifiers — the dialect regrows.

**Proposed fix.** Targeted rename sweep: harness exports, the `package stores` name, file names, template fixture names, parameter/local names. Compiler-enumerable; confined to test support, config internals, and example fixtures. Scope carefully: "store" is a legitimate noun in other contexts (object-store sensor, blob storage) — this is a claim-producer-context sweep, not a global ban, so no lint guard is practical; the defense is the sweep plus the concept doc.

## 3. Endpoint env-var collision + undocumented-variable tail

**Finding.** Three near-colliding endpoint variables, one documented:

- `RIMSKY_CONTROL_API` — the CLI's control-API endpoint (six call sites, e.g. `cmd/rimsky/cli/run.go:146`). Undocumented.
- `RIMSKY_CONTROL_API_URL` — proxy/enrollment control-API address (`lib/protocols/enroll/mode.go:14`). Documented.
- `RIMSKY_URL` — the host-agent's **proxy** address (`lib/runtime/hostagent/config.go:45`), not a control-API URL. Undocumented, and the generic name misleads.

Plus ~30 more `RIMSKY_*` vars read in live code but absent from CLAUDE.md / concept docs / RELEASING.md: `RIMSKY_LOG_LEVEL`, `RIMSKY_LOG_BINARY`, `RIMSKY_CONTEXT`, `RIMSKY_SUPERVISOR_CONFIG`, `RIMSKY_SUPERVISOR_CALLBACK_ADVERTISE_PORT` (HOST twin is documented), sensor state DSNs, the eight `RIMSKY_OPENLINEAGE_*` vars, metrics/listen addresses, and others. (Some may appear in example READMEs — not checked there.)

**Hazard.** A session wiring the CLI finds `RIMSKY_CONTROL_API_URL` in the docs, sets it, and the CLI reads the other variable; silent no-effect. `RIMSKY_URL` invites misconfiguration by name alone.

**Proposed fix.** Two independent halves:
- Naming (pre-v1, break freely): rename `RIMSKY_URL` to say what it is (proxy address, e.g. `RIMSKY_PROXY_URL`); either fold the CLI onto `RIMSKY_CONTROL_API_URL` or rename the pair so they cannot be confused.
- Documentation, made mechanical: an env-var registry (concept doc or generated table) plus a fitness test that greps `os.Getenv("RIMSKY_` across live code and fails on any variable missing from the registry. Turns docs-drift-from-env-surface into a lint failure.

## 4. rimsky.yml parsed through two loaders with divergent env expansion

**Finding.** Three env-expansion implementations with three behaviors:

- `lib/control/config/claim_producers.go::expandBracedEnvRefs` (line 260) — `${VAR}` only; unset → silently empty. Used by `LoadRimskyConfigYAML`.
- Plain `os.ExpandEnv` — `$VAR` and `${VAR}`; unset → silently empty. Used by the supervisor loader (`lib/control/launch/supervisor.go:320`) and the compose path (`cmd/rimsky/cli/compose/synthetic_config.go:134`).
- `expandConfigEnv` — unset → hard error; byte-identical copies in `lib/services/claim_producers/filesystem/server/opts.go:168` and `postgres/server/opts.go:228` (strict-DRY violation; shared home exists at `lib/services/internal/`).

Sharp edge: `LoadSiblingBlocks` (`synthetic_config.go:125`) re-parses the **same sibling rimsky.yml** with `os.ExpandEnv` that `LoadRimskyConfigYAML` parses with `expandBracedEnvRefs`. A bare `$VAR` expands on the compose path and passes through literally on the control-api path — same file, same key, different value by reader, no error either way.

**Hazard.** Whichever behavior an agent observes first becomes its model of "how rimsky config expansion works," and it is wrong for the other path. Silent-empty on unset is how credentials quietly vanish.

**Proposed fix.** One expansion function, one policy: `${VAR}` only, unset → hard error (the strictest existing variant). Collapse the two service copies into `lib/services/internal/` (consumption-side isolation permits that home). Unify the root module's paths by making compose consume `LoadRimskyConfigYAML` instead of re-parsing — kills the double-parse at the root rather than synchronizing two parsers forever.

## 5. blob-backend concept doc wrong in both directions

**Finding.** Three mutually inconsistent statements of how many surfaces spill into the blob backend:

- Doc body (`.ok-planner/design/concepts/blob-backend.md:11`): four — attribute values, event payloads, parked payloads, scratch.
- TOC line in `concepts.md`: one — attribute values only.
- Code: exactly two — attribute values (`value_handle`/`value_handle_backend` in both backends) and scratch (`rimsky_node_runs.scratch_handle`, written by `lib/runtime/runner_terminal_scratch.go`). No event-payload or parked-payload blob columns exist in any migration; the park path stores no payload blob. The doc's own invariants section already matches the two-surface reality, contradicting its opening sentence.

All other blob-backend invariants verified accurate (memory-backend unified gate, backend-prefix handle fallback, orphan ledger + sweep). This was the only wrong concept doc found across the catalog.

**Hazard.** CLAUDE.md names the concept catalog as the authoritative design surface. An agent trusting the body may build the "missing" spill columns; one trusting the TOC would touch scratch spill unaware the blob backend is involved.

**Proposed fix.** Edit the doc body and the TOC line to the two real surfaces. No code change.

## 6. Three stale open tensions

**Finding / proposed fix per tension:**

- `timeout-policy-asymmetry` — fully dissolved. Both referents are gone: no `frame_timeout_ms` anywhere; `max_park_duration` is an actively rejected config key (`lib/control/config/claim_producers.go:349`); `lib/runtime/sweep_parked.go` only wakes resume-ready nodes (no destructive parked→failed path); the frame and parked-state concept docs no longer mention these timeouts. → Move to `_resolved/` with a note that the machinery was removed rather than the asymmetry settled.
- `stub-mode-signature-no-proto-surface` — evidence stale, core live. The "probe logic duplicated in the CLI" claim is false (CLI calls the shared `ProbeStubMode`, `lib/protocols/conformance/executor/runner.go:101`; the `{stub: true}` literal has one site); the `userdata` framing is stale (field now reserved in executor.proto). The no-proto-surface question remains real. → Keep open; rewrite the evidence section.
- `callback-hostname-split` — partially superseded. The "fails silently when empty" framing predates the startup error for unset-advertise + wildcard-bind (`lib/runtime/supervisor.go::effectiveCallbackHostPort`). The set-but-unreachable silent mode and two-hostname asymmetry persist. → Keep open; acknowledge the fix in the body.

The other six open tensions verified as legitimately open against current code.

## 7. X/XInTx residue

**Finding.** The tx-last wave collapsed the dual-method idiom across ~1,900 call sites but missed:

- Live pairs (the retired idiom, verbatim): `MarkRevoked`→`markRevokedInTx` and `RevokeIfNotLast`→`revokeIfNotLastInTx` in `lib/foundation/persistence/sqlite/api_keys.go`; postgres `RevokeIfNotLast` twin; `getOrCreateInTx` wrappers in both backends' `deployment_ca.go`.
- Judgment call: `lib/foundation/persistence/blob.go` pairs `BlobBackend.Write/Read` with `TxBlobBackend.WriteInTx/ReadInTx` + dispatch helpers. Optional-capability split, not a nil-tx wrapper — structurally different, but wearing the retired idiom's name.
- Vocabulary residue: ~15 single-form functions keeping a now-meaningless `InTx` suffix — most of `lib/runtime` (`signal_emit.go:20`, `state_propagation.go:393`, …), one exported (`EmitTerminalSuccessAndDrainInTx`, `lib/runtime/pure_cascade_settle.go:18`, called from the scheduler), `Harness.InTx` in `test/support/scenario/harness.go:805`, conformance `park_resume.go:107`.

**Hazard.** Copy-source effect: an agent adding a persistence method sees a live pair in `api_keys.go` and infers the pair is the house pattern.

**Proposed fix.** Collapse the api-key and deployment-CA pairs into single tx-taking methods; rename the suffix-residue functions to drop `InTx` (tx-last signature already carries the information); make an explicit call on the blob interface — bless it as a distinct capability idiom with non-echoing names, or fold it in. Compiler-enumerable, no behavior change.

## 8. Image-freshness hole + stale :latest prose

**Finding.**

- `lib/services/test/scenarios/claude_agent_fake_cli/Dockerfile.fake-claude-agent:37` builds `FROM rimsky-executor-claude-agent:latest`. The produced image is tagged `:src-<tree-hash>` but its base layer is whatever `:latest` points at — a stale base yields stale executor code under a fresh-looking tag. The one remaining place the "staleness is unrepresentable" guarantee can be violated. The same Dockerfile carries a stale comment claiming the harness builds it inline via testcontainers (it is built by `make test-images`).
- Six example READMEs still describe `:latest` harness resolution (`examples/README.md:81`; claimproducer, publisher, lifecyclesubscriber, executor, validation READMEs), contradicting the src-tag resolution CLAUDE.md documents.
- Legitimate and unchanged: the Makefile producing `:latest` for interactive dev; the two demo shell scripts defaulting to `:latest` (env-overridable).

**Proposed fix.** Parameterize the base (`ARG BASE_TAG`; `FROM rimsky-executor-claude-agent:${BASE_TAG}`) and have `test-images` pass the same src-tree tag it stamps on outputs (the base is built by `service-images` from the same tree). Delete the stale comment. Update the six READMEs to describe src-tag resolution.

## 9. Enforcement gaps: unconfigured citation tags + lax YAML decoding

**Finding.**

- `@constraint:` / `@deliberate:` tags in two demo shell scripts: `examples/subscription-mounting-demo.sh` (4 sites), `examples/cascade-send-demo.sh` (7 sites). Neither tag is configured in `.plumbline.json`; the cheatsheet calls any unconfigured tag residue. `plumbline .` exits clean anyway — shell scripts evidently escape the check, so the no-invented-tags rule is only self-enforcing in Go.
- Zero `KnownFields` uses across all twelve `yaml.Unmarshal` sites — every config loader decodes lax. `LoadRimskyConfigYAML` hand-rejects exactly five retired keys with good messages; any other unknown key (typo, guess, stale example) is silently ignored at every boundary (rimsky.yml, supervisor YAML, service configs).

**Hazard.** Tags: proof that invented tags already slip in where the hook doesn't look. YAML: set a plausible-but-wrong key, observe no effect and no error, conclude the feature is broken.

**Proposed fix.** Convert or delete the eleven tag sites; raise the shell-coverage gap upstream in plumbline if intended to be covered. Switch every loader to `yaml.Decoder` + `KnownFields(true)`, keeping the five hand-rejections only where their retirement-specific messages add value.

## 10. Oversized files (~40 over 700 lines)

**Finding.** Worst live-code offenders: `lib/foundation/persistence/sqlite/nodes.go` 1,416 / `postgres/nodes.go` 1,226; `test/support/scenario/harness.go` 1,204; `lib/foundation/persistence/conformance/claimant_guard.go` 1,118; `lib/graph/node/template_validator.go` 1,107; `lib/services/executors/claude-agent/agentrun.go` 1,034; `lib/control/controlapi/instances.go` 1,023; `runner_dispatch.go`, both `queue.go` backends, `templates.go` close behind. Test outlier: `lib/runtime/auto_terminal_test.go` at 2,766.

**Hazard.** The ~500-line guideline is about edit/merge granularity, and the project's parallel worktree-wave workflow depends on file-disjoint packets — every multi-feature monolith shrinks the space of conflict-free packets (the remediation's one rebase conflict was two packets meeting inside shared `lib/runtime` files). No mechanical check exists (golangci-lint has no file-length linter).

**Proposed fix.** Opportunistic feature-seam splits, not a big-bang wave — the remediation demonstrated the move (7-way `template_validator_test.go` split; dispatch-context extraction from `tokenregistry.go`). Highest-value targets: the persistence `nodes.go` pair (split both backends along the same seams to keep structures mirrored) and `auto_terminal_test.go`.

---

## Suggested sequencing

Three buckets, independent of each other:

1. **Doc/design-corpus edits** (no code): #5 blob-backend doc + TOC, #6 tension moves/refreshes, #8's README updates.
2. **Small mechanical closures** (each a self-contained packet): #7 InTx collapse + rename, #8 base-tag ARG, #9 tag cleanup + KnownFields, #3 env-var registry + fitness test + renames, #4 expansion unification.
3. **Real sweeps** (worktree-wave scale): #1 wall-clock test verdicts (largest; includes the lint capstone), #2 store/claim_producer rename.

Bucket ordering within a wave plan: #4 before #3 (the expansion unification touches the same config loaders the env-var registry will enumerate); #1's lint capstone lands only after its sweep or the tree goes red. #10 is ongoing hygiene, not a wave — split opportunistically when a packet already owns the file.
