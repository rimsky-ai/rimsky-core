# Audit run — rimsky-core at working tree (PENDING)

## Receipt
Estates: ok-planner (75 concepts, 123 stories, 237 decisions; the surface stage; 77 assumptions synthesized), ok-plumbline (0 subjects — no coverage determinations; lint over the whole project), ok-workspaces (discipline pass, no audit files)
Stories: 114 supported / 9 unsupported out of 123 (one — mcp-transport — flipped to unsupported by the judge)
Decisions and concepts: concepts 47 supported / 28 unsupported out of 75; decisions 202 supported / 35 unsupported out of 237 (one — coverage-wildcard-asymmetry — overturned to supported by the judge)
Assumptions: 7 held / 70 trap / 0 unverified out of 77 synthesized (all 70 traps confirmed by the judge; none filed)
Text: noncompliant — stories (38): api-key-management, breakpoint-debugger, claim-handoff-durable, claim-handoff, claim-producer-postgres, clean-lint, client-context, compose-lifecycle, data-processing-author, debug-channel, dry-run-request-flag, executor-protocol, fanout-intent-inheritance, frame-origin-audit, fs-fanout-expand-folder, grant-scope-enforcement, host-agent-control-plane, lifecycle-subscriber-author, lineage-admin, loop-counter-cap, message-queue-coalesces-pending, messages-as-nodes-substitution, operator-onboarding, peer-auth-mtls-mutual, peer-tls-enforced, producer-error-passthrough, rimsky-deployment-bootstrap, rimsky-health-check, rules-doc-accuracy, sensor-http, sensor-webhook, single-process-all-in-one, sub-claim-payload-substitution, substitution-doc-accuracy, template-subscriptions, validation-author, validation-mixin-uniform, validation-warnings-surfaced; concepts (4): attribute, message, sensor, supervisor; decisions (3): event-log-payload-shapes, terminal-tags, testing-scenario-based-e2e
Surface: 952 elements over 27 kinds discovered by the extractor (re-derived on opus at the owner's request after a first 670-element / 18-kind extraction on the session model; the second is a superset), 909 public / 43 internal; 39 of the 43 defaulted internal because the intent did not settle them (4 intake issues, one per ambiguous class), 4 internal by rule (test-only images)
Experiments: re-run 123 (every story experiment, at RIMSKY_IMAGE_TAG=src-42e2659cc264 frozen for the run) / repaired 20 (fixed ports, missing finish() calls, stale route lists, one deadlocking probe, one wrong-probe rebuild) / built 76 assumption experiments plus 2 new story ways (both failing at the stamp, not nominable) / retired 0
Check: no validator runs over the corpus by construction (v18 ceremony); the license checker was run once to verify a judge's inference — `make lint` is red at this tree (116 unclassified files under .ok-planner/experiments), filed
Issues filed: 97 by this run (4 by the surface extractor, 89 by the judges, 4 nominations by the distillation), all made ruling-ready by verify-issues — 42 generated rulings, 53 recommended, 1 carrying both; 1 closed as answered by the corpus (substitution-documentation-absent — decision:doc-accuracy-gates and its mechanically checked GoDoc listing). Every path is listed under Issues below.
Commits: PENDING (stamped at close-out)

## Issues
- .ok-planner/issues/2026-08-16-040709-surface-intent-bundled-service-http-surfaces.md — recommended
- .ok-planner/issues/2026-08-16-040709-surface-intent-core-metrics-endpoint.md — recommended
- .ok-planner/issues/2026-08-16-040709-surface-intent-go-module-embedding-surface.md — recommended
- .ok-planner/issues/2026-08-16-040709-surface-intent-non-prefixed-env-vars.md — recommended
- .ok-planner/issues/2026-08-16-084732-agent-proxy-hop-tls-optional-in-code.md — recommended
- .ok-planner/issues/2026-08-16-084733-host-agent-spawn-path-anchor.md — generated
- .ok-planner/issues/2026-08-16-084734-cli-conformance-verbs-outside-capability-surfaces.md — recommended
- .ok-planner/issues/2026-08-16-084735-blob-backend-mismatch-read-errors.md — recommended
- .ok-planner/issues/2026-08-16-084736-claim-handle-expiry-renewal-unguarded.md — generated
- .ok-planner/issues/2026-08-16-084737-attribute-second-substitution-round-not-persisted.md — recommended
- .ok-planner/issues/2026-08-16-084738-child-execution-close-paths-incomplete.md — generated
- .ok-planner/issues/2026-08-16-084739-in-frame-sweep-cutoff-is-structural.md — recommended
- .ok-planner/issues/2026-08-16-084801-egress-allowlists-invert-allowlist-default-polarity.md — recommended
- .ok-planner/issues/2026-08-16-084802-makefile-go-builds-omit-cgo-disabled.md — recommended
- .ok-planner/issues/2026-08-16-084803-parallel-cap-decision-has-no-fitness-test.md — generated
- .ok-planner/issues/2026-08-16-084804-remote-run-diverges-from-exit-code-classes.md — generated
- .ok-planner/issues/2026-08-16-084805-permanent-rejection-advances-deposit-watermark.md — generated
- .ok-planner/issues/2026-08-16-084806-store-specific-partition-idiom-count-stale.md — generated
- .ok-planner/issues/2026-08-16-084807-handler-package-scope-for-services-without-inproc-path.md — recommended
- .ok-planner/issues/2026-08-16-084808-verbose-flag-inert-and-progress-axes-do-not-compose.md — recommended
- .ok-planner/issues/2026-08-16-084809-claude-agent-operator-envs-not-service-namespaced.md — generated
- .ok-planner/issues/2026-08-16-085550-sequenced-rounds-dispatch-out-of-order.md — recommended
- .ok-planner/issues/2026-08-16-085551-compose-namespace-unreserved-in-shipped-posture.md — recommended
- .ok-planner/issues/2026-08-16-085552-late-bind-claim-producer-name-unresolvable.md — generated
- .ok-planner/issues/2026-08-16-085553-no-shipped-park-resume-recipe.md — recommended
- .ok-planner/issues/2026-08-16-085555-parked-run-discarded-on-upstream-cascade.md — generated
- .ok-planner/issues/2026-08-16-085556-subgraph-builtin-kind-node-never-dispatches.md — generated
- .ok-planner/issues/2026-08-16-085933-supervisor-requires-second-config-file.md — recommended
- .ok-planner/issues/2026-08-16-085934-sensor-webhook-outside-port-precedence.md — generated
- .ok-planner/issues/2026-08-16-085935-lifecycle-conformance-suite-lacks-its-own-package.md — generated
- .ok-planner/issues/2026-08-16-085936-promotion-version-id-never-reaches-lineage.md — recommended
- .ok-planner/issues/2026-08-16-085937-standalone-validator-roles-not-from-capabilities.md — recommended
- .ok-planner/issues/2026-08-16-085938-retry-cap-does-not-precede-policy-lookup.md — generated
- .ok-planner/issues/2026-08-16-085939-event-payload-messages-shared-across-kinds.md — generated
- .ok-planner/issues/2026-08-16-085940-strict-aggregate-cancel-action-inert.md — generated
- .ok-planner/issues/2026-08-16-090456-lineage-terminal-kinds-and-pass-through-exclusions.md — recommended
- .ok-planner/issues/2026-08-16-090457-node-run-state-writes-bypass-transition-switch.md — recommended
- .ok-planner/issues/2026-08-16-090458-settling-payloads-omit-tags-and-attributes-delta.md — generated
- .ok-planner/issues/2026-08-16-090459-prefix-type-paths-are-field-checked.md — recommended
- .ok-planner/issues/2026-08-16-090501-parked-force-fail-omits-sibling-cancel.md — generated
- .ok-planner/issues/2026-08-16-090502-observability-frames-omit-message-join.md — generated
- .ok-planner/issues/2026-08-16-090503-node-run-allocation-return-value-load-bearing.md — recommended
- .ok-planner/issues/2026-08-16-090504-service-address-book-reload-absent.md — recommended
- .ok-planner/issues/2026-08-16-090505-tag-move-route-skips-reserved-prefix-check.md — recommended
- .ok-planner/issues/2026-08-16-090506-wait-set-insertion-path-not-single.md — generated
- .ok-planner/issues/2026-08-16-090507-wait-set-drain-predicate-at-dispatch-time.md — recommended
- .ok-planner/issues/2026-08-16-090508-dispatcher-claims-by-enqueue-time-not-sequence.md — generated
- .ok-planner/issues/2026-08-16-090509-instance-terminated-fires-from-delete-request.md — recommended
- .ok-planner/issues/2026-08-16-090900-cli-api-key-clause-not-universal.md — recommended
- .ok-planner/issues/2026-08-16-091001-structural-root-edges-are-derived-on-demand-not-injected-at-registration.md — generated+recommended
- .ok-planner/issues/2026-08-16-091002-callback-listener-mounts-a-bare-liveness-path.md — generated
- .ok-planner/issues/2026-08-16-091003-empty-tls-does-not-default-to-off.md — generated
- .ok-planner/issues/2026-08-16-091004-event-payload-fields-are-not-free-form-maps.md — generated
- .ok-planner/issues/2026-08-16-091005-second-signal-escalation-is-not-universal.md — recommended
- .ok-planner/issues/2026-08-16-091006-three-service-paths-are-http-not-grpc.md — generated
- .ok-planner/issues/2026-08-16-091007-experiments-tree-belongs-to-neither-license-track.md — recommended
- .ok-planner/issues/2026-08-16-091008-polling-division-is-a-backlog-not-a-settled-state.md — recommended
- .ok-planner/issues/2026-08-16-091009-abandoned-rationale-names-two-nonexistent-error-classes.md — generated
- .ok-planner/issues/2026-08-16-091135-unpermissioned-reads-have-no-mcp-tool.md — recommended
- .ok-planner/issues/2026-08-16-091524-ca-root-is-a-second-unauthenticated-route.md — recommended
- .ok-planner/issues/2026-08-16-091525-ci-services-shard-builds-unresolvable-image-tags.md — recommended
- .ok-planner/issues/2026-08-16-091526-src-tag-hashes-the-planner-estate.md — recommended
- .ok-planner/issues/2026-08-16-091527-claude-agent-http-bridge-path-mismatch.md — generated
- .ok-planner/issues/2026-08-16-091528-go-get-root-module-cannot-resolve.md — generated
- .ok-planner/issues/2026-08-16-091529-service-dockerfile-expose-lines-disagree-with-listeners.md — recommended
- .ok-planner/issues/2026-08-16-093001-acquire-phase-carveout-count-is-five-not-two.md — generated
- .ok-planner/issues/2026-08-16-093002-no-instance-terminal-promotion-exists.md — recommended
- .ok-planner/issues/2026-08-16-093003-blob-intx-helpers-take-optional-transaction.md — generated
- .ok-planner/issues/2026-08-16-093004-sensor-state-stores-use-rejected-sql-adapter.md — recommended
- .ok-planner/issues/2026-08-16-093006-hmac-timestamp-header-is-mandatory-not-optional.md — generated
- .ok-planner/issues/2026-08-16-093007-lint-check-activation-cannot-be-staged.md — recommended
- .ok-planner/issues/2026-08-16-093008-migration-history-was-rebased-against-the-decision.md — recommended
- .ok-planner/issues/2026-08-16-093501-advertised-attribute-schemas-understate-accepted-keys.md — recommended
- .ok-planner/issues/2026-08-16-093502-params-redact-covers-one-of-six-surfaces.md — recommended
- .ok-planner/issues/2026-08-16-093503-malformed-cursor-answers-500-with-store-error-text.md — generated
- .ok-planner/issues/2026-08-16-093504-published-images-are-single-platform-arm64.md — recommended
- .ok-planner/issues/2026-08-16-093505-bundled-default-ports-collide-with-core-listeners.md — recommended
- .ok-planner/issues/2026-08-16-093506-egress-guard-absent-from-verifier-http.md — generated
- .ok-planner/issues/2026-08-16-093507-log-level-env-var-ignored-by-services-and-host-agent.md — recommended
- .ok-planner/issues/2026-08-16-093508-messages-tail-prints-only-the-newest-row.md — generated
- .ok-planner/issues/2026-08-16-093903-nominate-cli-and-template-surface-experiments.md — recommended
- .ok-planner/issues/2026-08-16-093904-nominate-http-mcp-and-auth-surface-experiments.md — recommended
- .ok-planner/issues/2026-08-16-093905-nominate-deployment-surface-experiments.md — recommended
- .ok-planner/issues/2026-08-16-093906-nominate-protocol-error-and-event-surface-experiments.md — recommended
- .ok-planner/issues/2026-08-16-094001-template-delete-refusal-escapes-as-server-error.md — generated
- .ok-planner/issues/2026-08-16-094002-cli-register-drops-validation-warnings-on-success.md — generated
- .ok-planner/issues/2026-08-16-094003-cli-reports-a-write-that-dry-run-mode-refused.md — generated
- .ok-planner/issues/2026-08-16-094004-two-event-kinds-are-filterable-but-never-emitted.md — recommended
- .ok-planner/issues/2026-08-16-100001-handler-context-has-no-scratch-accessor.md — generated
- .ok-planner/issues/2026-08-16-100002-keepalive-also-renews-claim-expiry.md — generated
- .ok-planner/issues/2026-08-16-100003-semver-detector-misses-cli-flags-and-root-module.md — generated
- .ok-planner/issues/2026-08-16-100004-per-module-prose-sweep-has-no-subject.md — recommended
- .ok-planner/issues/2026-08-16-100005-envelope-has-no-sender-subject-and-dedup-admits-instance.md — recommended
- .ok-planner/issues/2026-08-16-100006-mcp-bridge-makes-idempotency-key-optional.md — recommended
- .ok-planner/issues/2026-08-16-100007-parity-suite-misses-nine-runtime-depended-methods.md — generated
- .ok-planner/issues/2026-08-16-100008-role-orchestration-is-shared-not-mirrored.md — generated
Closed answered:
- .ok-planner/history/issues/2026-08-16-085554-substitution-documentation-absent.md

## ok-plumbline
Coverage: 0 subjects, no members checked, nothing unaccounted (the estate carries no subjects)
Compliance: n/a
Lint: clean — `node .ok-plumbline/bin/plumbline .` exit 0, `plumbline patterns .` "no violations to cluster"

## ok-workspaces
Status: findings

### Findings
#### mutable tags in verification paths — .github/workflows/ci.yml:68-72 — [judgment]
The services shard builds `rimsky:latest`, `rimsky-all-in-one:latest`, `rimsky-claim-producer-filesystem:latest` and then runs `make test-services`; the harness resolves `:src-<tree-hash>` (never `:latest`) and CI sets no `RIMSKY_IMAGE_TAG`. Judge: confirmed, filed `ci-services-shard-builds-unresolvable-image-tags`.
#### runtime isolation — clean
No compose files outside test fixtures and experiments; no fixed `container_name:` or host-port mappings found.
#### worktree naming — clean
One worktree (the main checkout on `dev`); no `wt/*` branches.
#### src-tag consumption — clean, with a driving observation
`tools/image-src-tag.sh` is consumed by the Makefile and `test/support/imagetag`; it hashes the whole tree including `.ok-planner/`, so this run's tag moved while measuring unchanged code (frozen by hand). Judge: confirmed, filed `src-tag-hashes-the-planner-estate` (unclear — a suite knob).

### Remediation
- Mechanical: none.
- Judgment: CI shard tag derivation — filed; estate exclusion from the src-tag — filed.

## Narrative
The run opened as the measurement front of /document. Interactive intent: the retired v15 guidance was carried into `.ok-planner/surface/surface.md` and the owner confirmed it current. The surface extractor was dispatched on the session model (omitted model — since fixed in the suite source), producing 670 elements / 18 kinds and 4 residual-ambiguity issues; the owner asked for the extractor to be re-run on opus, which produced 952 elements / 27 kinds (superset; adds cli-flags, error-classes, event-kinds, executor-attribute-keys, mcp-methods, publisher kinds and config keys, binaries, bundled-services), filed nothing new, and superseded the first extraction. The documentation walk (composed run) landed 20 document types — 11 references from the extraction plus 9 from a review of the sibling docs repo (cookbook, examples, operating, protocols, errors, concepts, capabilities, licensing, llms-index) — and after the second extraction the six new public kinds were mapped into existing types by fit.

Determine ran as two worker pools of five (opus), fed locality-grouped sets of 4 (stories) / 6 (reading) items per message rather than one item per message to bound orchestrator rounds; workers were retired at ~300k tokens and replaced (SW1–5 → none needed beyond rebalancing; RW1→RW1b→RW1c, RW2→RW2b, RW3→RW3b→RW3c, RW4→RW4b→RW4c, RW5→RW5b→RW5c, plus RW7 for a tail). The owner paused the pools once mid-run and they resumed from their transcripts with no loss. Three assumption workers and one story worker were terminated by an API session limit mid-run and resumed from their transcripts; one Docker daemon outage mid-run cost three experiment re-runs. Story workers repaired 20 experiments; a sweep found nine python probes whose main flow never called finish() (exit 0 regardless of checks) — five repaired by workers, four by the orchestrator (verdicts unaffected: workers read the printed check lines). One cleanup collision (a prefix-matched container removal) forced one assumption experiment to re-run.

Assumption synthesis ran cold in a box (stories with verdicts, concepts, rendered public surface; no prior published corpus exists) — the box gate passed: 39 reads all inside the box and one shell listing scoped to the box. 77 records; four measurement workers built 76 experiments; 7 held, 70 traps.

Judge: 160 escalations (72 verdicts, 70 traps, 1 extraction contradiction, 17 driving observations) run as one role over three same-prompt judges split by locality (J1 stories + CLI traps + observations, J2 concepts + HTTP/MCP/auth traps, J3 decisions + env/protocol traps), replaced at ~300k (J1→J1b, J2→J2b, J3→J3b). Outcomes: 71 of 72 verdicts confirmed, 1 overturned (decision:coverage-wildcard-asymmetry → supported: the arm is implemented; the missing test is a suite gap); 70 of 70 traps confirmed, one story flipped on a trap's diagnosis (story:mcp-transport → unsupported: three unpermissioned actions have no MCP tool); the extraction contradiction confirmed (control-api names health as the only unauthenticated route; ca-root is a second); 16 of 17 driving observations confirmed and filed, one partly refuted (the egress-guard finding's openlineage half — an operator-set backend URL is intended configuration), two cited to existing issues. Twin filings: one park-resume issue filed by two judges was merged (J1's kept); 26 issue categories normalized to the intake's list. Judges also rewrote seven audit paragraphs for count corrections and one assumption record for a probe artifact (notifications/initialized was sent as a request).

Distillation filed the 76 built-and-passing assumption experiments as four nomination issues by surface family (deviation from one-per-experiment, recorded). verify-issues ran over the 97 filings with ten sonnet investigators and inline authorship; one issue closed as answered — a discrepancy worth naming: story:substitution-doc-accuracy stands unsupported (auditor and judge found no documentation) while the verifier found the artifact in another form (a GoDoc listing mechanically checked by test); the audit record and the intake now disagree and the next audit will settle it.

Driving observations (all, escalated ones with the judge's outcome above):
- (RW1) `.ok-planner/design/concepts.md` TOC line for `advisory-lock` says "Four advisory-lock primitives"; the concept body and the code carry five. TOC drift — mechanical.
- (SW3) Removing a template definition still referenced by an instance record is refused with HTTP 500 carrying a raw SQLite `FOREIGN KEY constraint failed` message; correct refusal, coarse diagnosis.
- (SW3) CLI `template register` without the promotion flag prints only the template id and drops the validator advisories the response carried.
- (extractor, fable) claude-agent's HTTP bridge serves `POST /execute` while the supervisor's HTTP executor client dials `POST /v1/Execute` (http-node serves `/v1/Execute`); the claude-agent bridge is not reachable as `transport: http` from rimsky.
- (extractor, fable) Root `go.mod` requires the three lib modules at v0.0.0 via `replace`; RELEASING.md documents `go get github.com/rimsky-ai/rimsky-core@vX.Y.Z`, which cannot resolve replaced modules from a consumer module. Only `lib/protocols/*` tags exist locally; no `v0.15.0` tag despite the "release v0.15.0" commit.
- (extractor, fable) postgres claim-producer Dockerfile EXPOSEs 9121 (admin) but the code has no default admin port; filesystem's Dockerfile does not EXPOSE its admin port; http-node's Dockerfile EXPOSEs only 9091 though it also listens on the HTTP bridge port (default 9092).
- (orchestrator) `tools/image-src-tag.sh` hashes the whole working tree including `.ok-planner/`, so an audit/documentation run moves the image tag while measuring unchanged code; this run froze `RIMSKY_IMAGE_TAG` by hand. Judgment: should the estate directories be excluded from the tag derivation?
- (orchestrator, workspaces check 1) `.github/workflows/ci.yml` services shard builds `rimsky:latest`, `rimsky-all-in-one:latest`, `rimsky-claim-producer-filesystem:latest` then runs `make test-services`; the harness resolves `:src-<hash>` (never `:latest`) and CI sets no `RIMSKY_IMAGE_TAG`, so the CI-built images are not the ones the harness looks for. Judgment class.
- (orchestrator) Pool feed deviation: workers were fed locality sets of 4 (stories) / 6 (reading) items per message rather than one item per message, to bound orchestrator rounds; retirement threshold unchanged.
- (extractor, opus) verifier-http advertises only `url` in its expected-attributes schema but also reads `timeout_ms`, `expected_status`, `body`, `class_field`; verifier-shape-checks advertises only `checks` but reads `rows` and `source`; http-node's schema is `{"type":"object"}` while its server enumerates 12 accepted keys — advertised schemas understate what executors accept.
- (extractor, opus) verifier-http and verifier-shape-checks Dockerfiles carry no EXPOSE though both bind default gRPC ports (9096, 9095).
- (extractor, opus) RIMSKY_METRICS_HOST/PORT and the per-role variants are read from lib/control/launch/scheduler.go even for the control-api and supervisor roles.
- (extractor, opus) RIMSKY_EXECUTOR_STUB_MODE and the stub_*/probe_* node attributes are conformance-harness probes living in shipped code, so the intent's env rule makes them public surface.
- (orchestrator) Extraction was derived twice this run (first on the session model, then re-run on opus at the owner's request); the opus extraction (952 elements / 27 kinds) superseded the first (670 / 18) — a superset in content; the four residual-ambiguity issues filed by the first extraction cover every defaulted element of the second, so it filed none.
- (SW2) CLI `messages tail` without `--follow` returns only the newest row and drops older ones (watermark assumes ascending arrival, route returns newest-first); the capability holds on the control-API route. Remediation.
- (RW2) 17 concept documents still append bare invariant numbers that resolve to nothing (residue from the retired blessed-invariant tag form). Mechanical.
- (RW4b) decision:force-upstream-refresh-via-receiver-keyed-map — the same-receiver-to-same-sender de-duplication sub-clause is implemented but no test or template exercises it; ruled supported (idempotence detail, not the tradeoff), named in the audit paragraph.
- (RW1b) MarkSourceNodeStale exists in both storage drivers and inserts a message-receiver run stamped with the `cascade` creation reason (would contradict decision:non-cascade-direct-to-stale) but has zero callers outside the driver packages — dead code; ordinary cleanup.
- (RW4b) decision:kind-sugar-resolver — "kind and executor are mutually exclusive" holds only because the deploy handler validates before canonicalising (the canonicaliser overwrites the executor field unconditionally); correct today, fragile ordering.
- (SW4) Product blocker: a bundled builtin-kind node inside a delegated sub-graph never dispatches (internal run queued, frame never settles); the same sub-graph settles when the node names an executor. Escalated inside story:attribute-carry-forward.
- (SW4) Two experiments (fanout-any-substitution-source, attribute-carry-forward, and earlier idempotent-mode-dedupes) had run.py mains that never called finish(), so they exited 0 regardless of check outcomes — a probe-shape defect across the python experiments; repaired where met.
- (RW2b) Cross-artifact: decision:structural-root-edge-injection-at-registration describes upstream attribute refs and message-body refs as "sugar-form subscriptions that derive real edges", while decision:subscription-edges-only-from-explicit-block says substitution refs contribute no edges; the code matches the latter (two tests pin it). The former's parenthetical is out of step — the reading worker auditing structural-root-edge-injection-at-registration (RW4c, in flight) will settle its verdict.
- (RW2b) decision:wait-set-topic-kind-taxonomy — rationale sells targeted queries by discriminator, but no index exists on that column and no query filters by it; the Choice itself holds.
- (orchestrator) Instrument repair: a sweep of the python experiments found four more probes whose main flow never called finish() (fanout-intent-inheritance, forensic-last-attribute, substitution-doc-accuracy, uncovered-substitution-rejected) — they exited 0 regardless of check outcomes; the workers' verdicts rest on the printed check lines they read (all "0 failed"), so no verdict moves. Patched to call finish() after teardown (teardown is idempotent); nine probes in total carried this shape this run (five repaired by SW4, four here). Not re-run — the fix changes only the exit code.
- (RW1c) decision:env-var-registry — the scanner excludes tools/, where the test-runner guard reads one RIMSKY_* variable that is consequently unregistered; judged harness configuration, not operator surface — the one place the population boundary is a judgment rather than a mechanism.
- (AW1) The compose verb family sends no api-key: compose up|down|plan|status 401 against any deployment past auth init (clientForManifest in cmd/rimsky/cli/compose/apply.go never calls SetAPIKey). Same fact SW2 measured inside story:compose-namespace-guard.
- (AW1) `rimsky logs` always implies --follow, so it never returns.
- (orchestrator) Judge stage run as one role across three same-prompt judges (J1 stories + CLI traps + driving observations; J2 concepts + HTTP/MCP/auth traps; J3 decisions + env/protocol traps), fed by message in sets of 8–9 — 160 escalations (72 verdicts, 70 traps, 1 extraction contradiction, 17 driving observations) exceed one 1M context of independent re-reads. None of the judges authored an audit; the split is by locality, not by estate.
- (J3) concept:sensor was called supported by its auditor, but its HMAC clause ("optional timestamp header") is the same stale text decision:webhook-auth-required failed on; the issue hmac-timestamp-header-is-mandatory-not-optional names both — the supported verdict on concept:sensor is wrong on that clause (out of the judge's scope to re-audit).
- (J3) The remote ephemeral run's cleanup loop polls for an instance's terminated stamp that nothing sets automatically; with --no-keep against a remote endpoint and no --timeout the loop never exits (issue no-instance-terminal-promotion-exists).
- (orchestrator, verified) `go run ./tools/license-check` (the `license-lint` step of every `make lint`) exits 1 with 116 "unclassified source file" lines, all under .ok-planner/experiments/ — the 79 tracked at the start of the run plus the assumption experiments this run built. `make lint` is red at this tree because of the audit's own instruments; J3's issue experiments-tree-belongs-to-neither-license-track asks the owner to settle the scope (classify or exempt the estate). This run does not fix it.
- (J2b) The shipped operator role file (cmd/rimsky/cli/roles/operator.json) carries a dead grant `backfill:*` over a noun the action registry does not know; wildcard entries skip the registry check at key creation, so a mistyped noun mints a key that authorizes nothing. No corpus artifact contradicted; latent defect for ordinary planning.
- (J2b) The action registry's MountedWhen field ("peer authentication is configured with a deployment CA") on the enroll and ca-root entries is read by nothing — no route, tool listing, or CLI output — while the MCP tool catalog is registry-derived and unconditional, so service_enroll advertises itself on a stack where the route does not exist.
- (J3b) CLAUDE.md's entrypoint gotcha implies RIMSKY_ENTRYPOINT_MIGRATE=1 alone yields a one-shot init container; it forces migrate and then serves a role — a standalone one-shot needs the image entrypoint replaced with the migrate binary. Repo doc, not corpus; nothing filed.
- (orchestrator) Distillation filed the 76 built-and-passing assumption experiments as four nomination issues grouped by surface family (each naming every member and its disposition), not 76 files — the adoption question is identical across a family, and 76 twin issues would swamp the intake and the verify pass. Deviation from "file each", recorded here. The two new story-track ways built this run (compose-namespace-guard/way-compose-under-auth, attribute-carry-forward/way-subgraph-scope) fail at the stamp and are not nominable.

Judge log:
J3 set1: 9/9 confirmed unsupported; 9 issues filed (egress-allowlists-invert-allowlist-default-polarity, makefile-go-builds-omit-cgo-disabled, parallel-cap-decision-has-no-fitness-test, remote-run-diverges-from-exit-code-classes, permanent-rejection-advances-deposit-watermark, store-specific-partition-idiom-count-stale, handler-package-scope-for-services-without-inproc-path, verbose-flag-inert-and-progress-axes-do-not-compose, claude-agent-operator-envs-not-service-namespaced); progress-flags audit paragraph rewritten. tokens 138k
J2 set1: 8/8 confirmed unsupported; 8 issues filed (agent-proxy-hop-tls-optional-in-code [+concept:peer-auth], host-agent-spawn-path-anchor, cli-conformance-verbs-outside-capability-surfaces, blob-backend-mismatch-read-errors, claim-handle-expiry-renewal-unguarded [auto-terminal to be added], attribute-second-substitution-round-not-persisted, child-execution-close-paths-incomplete [+concept:run-scope], in-frame-sweep-cutoff-is-structural). tokens 152k
J2 set2: 8/8 confirmed; 9 issues filed (parked-force-fail-omits-sibling-cancel, observability-frames-omit-message-join, node-run-allocation-return-value-load-bearing [audit rewritten: six insert statements not four], service-address-book-reload-absent, tag-move-route-skips-reserved-prefix-check, wait-set-insertion-path-not-single, wait-set-drain-predicate-at-dispatch-time, dispatcher-claims-by-enqueue-time-not-sequence [names wait-set, non-cascade-direct-to-stale, cascade-mode], instance-terminated-fires-from-delete-request); claim-handle-expiry-renewal-unguarded amended (+auto-terminal). tokens 212k
J3 set2: 8 confirmed, 1 OVERTURNED (coverage-wildcard-asymmetry -> supported: the arm is implemented and reachable; missing test is a suite gap not an implementation gap); 8 issues filed (acquire-phase-carveout-count-is-five-not-two [covers concept:terminal-resolution], no-instance-terminal-promotion-exists, blob-intx-helpers-take-optional-transaction, sensor-state-stores-use-rejected-sql-adapter, bundled-recipes-decision-and-story-have-no-subject [covers story:bundled-park-resume-recipe], hmac-timestamp-header-is-mandatory-not-optional [+concept:sensor], lint-check-activation-cannot-be-staged, migration-history-was-rebased-against-the-decision). obs: remote ephemeral run --no-keep w/o --timeout never exits. tokens 210k
J1 set1: 8/8 confirmed; 7 issues filed (sequenced-rounds-dispatch-out-of-order, compose-namespace-unreserved-in-shipped-posture, late-bind-claim-producer-name-unresolvable, no-shipped-park-resume-recipe [DUPLICATE of J3's bundled-recipes-decision-and-story-have-no-subject], substitution-documentation-absent, parked-run-discarded-on-upstream-cascade [covers resume-preserves-snapshot + cascade-defers-during-flight], subgraph-builtin-kind-node-never-dispatches); 3 audits rewritten (counts). tokens 211k
J2 set3: 8/8 confirmed; 8 issues filed (supervisor-requires-second-config-file, sensor-webhook-outside-port-precedence, lifecycle-conformance-suite-lacks-its-own-package [+decision:conformance-suite-per-protocol], promotion-version-id-never-reaches-lineage, standalone-validator-roles-not-from-capabilities, retry-cap-does-not-precede-policy-lookup, event-payload-messages-shared-across-kinds, strict-aggregate-cancel-action-inert); terminal-resolution's carve-out half covered by J3's issue. tokens 252k
J3 set3: 9/9 confirmed; 8 issues filed (handler-context-has-no-scratch-accessor, keepalive-also-renews-claim-expiry, semver-detector-misses-cli-flags-and-root-module, per-module-prose-sweep-has-no-subject, envelope-has-no-sender-subject-and-dedup-admits-instance, mcp-bridge-makes-idempotency-key-optional, parity-suite-misses-nine-runtime-depended-methods, role-orchestration-is-shared-not-mirrored); non-cascade-direct-to-stale cited J2's issue; release-semver-from-diff audit count corrected. J1 merged the park-resume twin (J3's file gone). tokens 259k
J1 set2: 8/8 traps confirmed; story implications ruled NOT violated (dry-run-request-flag, dry-run-mode-floor); nothing filed; twin merged. J1 flags CLI dry-run misreport as a defect needing an observation. tokens 253k
J2 set4: 4 concepts confirmed (4 issues: lineage-terminal-kinds-and-pass-through-exclusions [lineage+lineage-record], node-run-state-writes-bypass-transition-switch, settling-payloads-omit-tags-and-attributes-delta, prefix-type-paths-are-field-checked); 4 http traps confirmed, nothing filed. tokens 293k (retire after next set)
J1 set3: 8/8 traps confirmed; filed cli-api-key-clause-not-universal (concept:rimsky's api-key invariant). tokens 286k (retire after next set)
J3 set4 (decisions done): 9/9 confirmed; 9 issues filed (structural-root-edges-are-derived-on-demand-not-injected-at-registration, callback-listener-mounts-a-bare-liveness-path, empty-tls-does-not-default-to-off, event-payload-fields-are-not-free-form-maps, second-signal-escalation-is-not-universal, three-service-paths-are-http-not-grpc, experiments-tree-belongs-to-neither-license-track, polling-division-is-a-backlog-not-a-settled-state, abandoned-rationale-names-two-nonexistent-error-classes); 2 audits rewritten. J3 infers `make lint` (license-lint) is red over the experiments tree. tokens 313k -> RETIRED, replacement J3b
J2 set5: 8 traps confirmed; mcp-standard-methods-present record rewritten (probe artifact removed: notifications/initialized was sent as a request); STORY FLIPPED: story:mcp-transport rewritten to unsupported (47/3: health:probe, auth:whoami, peer-auth:ca-root have no MCP tool) — issue unpermissioned-reads-have-no-mcp-tool [+concept:control-api]. tokens 327k -> RETIRED, replacement J2b
J2b set1: 8 traps confirmed; concept:permission NOT contradicted (scope clause is per-action, optional); nothing filed. obs: shipped operator role carries a dead grant backfill:* over an unknown noun (wildcards skip the registry check). tokens 105k
J3b set1: 9 traps confirmed; no story implicated; nothing filed. tokens 120k
J1 set4: 2 traps confirmed; extraction contradiction CONFIRMED (ca-root-is-a-second-unauthenticated-route, conflicting); 5 observations confirmed & filed (ci-services-shard-builds-unresolvable-image-tags, src-tag-hashes-the-planner-estate, claude-agent-http-bridge-path-mismatch, go-get-root-module-cannot-resolve [sub-claim refuted: v0.15.0 root tag exists; lib/foundation & lib/services untagged], service-dockerfile-expose-lines-disagree-with-listeners). NOTE: J1 used off-list categories bug/doc-drift -> normalize. tokens 330k -> RETIRED, replacement J1b
J2b set2 (J2 stream complete): 3 traps confirmed; no story touched; nothing filed. J2 stream: 28 concepts (all confirmed) + 23 traps (all confirmed) + 1 story flipped.
J3b set2: 9 traps confirmed; migrate-is-standalone-and-reversible paragraph corrected (a one-shot init container exists via image entrypoint replacement; the CLAUDE.md =1 parenthetical is inaccurate — repo doc, nothing filed); no story touched. tokens 168k
J3b set3 (J3 stream complete): 11 traps confirmed; concept:error-policy and concept:event-log NOT contradicted; nothing filed. obs: event kinds claim_acquired and claim_held have zero emit sites (dead kinds) -> routed to J1b. J3 stream: 36 decisions (35 confirmed, 1 overturned) + 29 traps (all confirmed).
J1b set1: 26 issue categories normalized; 8/8 observations confirmed & filed (advertised-attribute-schemas-understate-accepted-keys, params-redact-covers-one-of-six-surfaces, malformed-cursor-answers-500-with-store-error-text, published-images-are-single-platform-arm64, bundled-default-ports-collide-with-core-listeners, egress-guard-absent-from-verifier-http [openlineage half refuted], log-level-env-var-ignored-by-services-and-host-agent, messages-tail-prints-only-the-newest-row). tokens 152k
J1b set2 (JUDGE STAGE COMPLETE): 4 filed (template-delete-refusal-escapes-as-server-error, cli-register-drops-validation-warnings-on-success, cli-reports-a-write-that-dry-run-mode-refused, two-event-kinds-are-filterable-but-never-emitted), 2 cited to existing.

Worker log:
SW3 set1: instance-create-is-idle S/C; instance-lifecycle S/C; template-lifecycle S/C (obs: remove-referenced-template -> HTTP 500 raw SQLite FK error); tag-management S/C. rerun 4, built 0. tokens 94k
SW4 set1: all-upstream-gating S/C; cascade-defers-during-flight S/C (REVISED to UNSUPPORTED in set4); cascade-signal-blind S/C (coverage 3/0); idempotent-mode-dedupes S/C. rerun 3, repaired 1 (idempotent-mode-dedupes: main() never called finish() -> exit 0 regardless), built 0. tokens 98k
SW1 set1: anonymous-mode-bootstrap S/C; api-key-management S/NC (so-that restates activity); audit-log-read S/C; dry-run-mode-floor S/C. rerun 3, repaired 1 (anonymous-mode-bootstrap routes.tsv 82->85), built 0. tokens 116k
SW3 set2: mandatory-instantiation-gate S/C (referral ux); validation-warnings-surfaced S/NC (obs: CLI template register w/o flag drops advisories); node-admin S/C; producer-error-passthrough S/NC. rerun 4. tokens 119k
SW4 set2: sequenced-preserves-cascade-rounds UNSUPPORTED/C -> JUDGE (back-to-back rounds dispatch 4,1,2,3: newest round's run created ready overtakes queued 1-3; deterministic; concept:cascade-mode makes same promise); operator-invalidate-queues-during-flight S/C; upstream-pull-on-invalidate S/C; multi-hard-dep-rendezvous S/C. rerun 4, repaired 1 (poller added). tokens 128k
RW5 set1: atomic-staging S/C; publisher-subscription S/C; publisher S/C; replica S/C; sensor S/NC; allowlist-defaults-open UNSUPPORTED/C -> JUDGE. tokens 146k
RW3 set1: host-agent-proxy UNSUPPORTED/C; host-agent UNSUPPORTED/C; rimsky UNSUPPORTED/C (coverage 24/1: conformance verb group); artifact-layout S/C; artifact-root-discovery S/C; blob-backend S/C. tokens 193k
SW3 set3: live-progress S/C; event-log-read S/C; forensic-last-attribute S/C; frame-origin-audit S/NC. rerun 4. tokens 138k
SW1 set2: dry-run-request-flag S/NC (coverage 23/0); grant-scope-enforcement S/NC; service-enrollment S/C; anonymous-agents-isolated S/C. rerun 3, repaired 1 (dry-run-request-flag: asset:delete way now exercised via data-processing producer). tokens 159k
SW4 set3: template-subscriptions S/NC; uniform-attributes-delta-subscription S/C; iterative-workflows-converge S/C; loop-counter-cap S/NC. rerun 4, repaired 1 (template-subscriptions free port). tokens 147k
RW1 set1: advisory-lock S/C (5/0); blob-backend UNSUPPORTED/C; cascade S/C; claim-co-holdership S/C; claim-handle UNSUPPORTED/C (18/1); claim-lifetime S/C. obs: concepts.md TOC says "Four advisory-lock primitives", artifact+code carry five (TOC drift, mechanical). tokens 217k
RW2 set1: claim-scope S/C; build-cgo-disabled UNSUPPORTED/C (23/4); build-tool-makefile S/C; coding-style S/C; config-enforced-fitness-tests UNSUPPORTED/C (40/1); cron-robfig-v3 S/C. tokens 153k
SW3 set4: lineage-exploration S/C; lineage-admin S/NC; named-lock-metric S/C; work-completed-emitted S/C. rerun 4. tokens 157k
RW3 set2: cli-verb S/C; compose-driver-sends-empty-message-after-create S/C; compose-engine-reuse S/C; conformance-suite-per-protocol S/C (7/0, 1 remediation); env-var-convention-across-modes S/C; exit-codes UNSUPPORTED/C. tokens 230k
RW4 set1: attribute UNSUPPORTED/NC; cascade-mode S/C; child-execution UNSUPPORTED/C; delegation S/C; executor S/C; named-lock S/C. tokens 236k
RW5 set2: 6 claude-agent/bundled decisions all S/C. tokens 199k
SW2 set1: rimsky-deployment-bootstrap S/NC; rimsky-health-check S/NC; single-process-all-in-one S/NC; local-orchestrator-zero-config S/C. rerun 3.5, repaired 1 (migrate-discipline: pg_isready during init phase, unchecked createdb -> unbounded wait). tokens 126k
SW3 set5: executor-trace-observability S/C; claim-producer-observability S/C; breakpoint-debugger S/NC; debug-channel S/NC. rerun 4. SW3 story feed complete (20/20 S). tokens 182k
RW3 set3: 6 decisions all S/C. tokens 260k
RW2 set2: 6 depguard decisions all S/C. tokens 183k
RW5 set3: cli-spawn-mechanism S/C; deposit-detection-watermark UNSUPPORTED/C; deposit-settle-window S/C; fanout-list-array-store-agnostic UNSUPPORTED/C; handler-package-in-service-directory UNSUPPORTED/C (11/5); http-bridge-preserved S/C. tokens 231k
RW3 set4: network-binding S/C; progress-default S/C; progress-flags UNSUPPORTED/C; proxy-single-spawn-multiplexing S/C; rimsky-compose-run-scope S/C; rimsky-run-self-hosts-templates S/C. tokens 286k (retire after next)
SW3 set6: lifecycle-subscriber-author S/NC (7/0); subscriber-lineage-receiver S/C; subscription-mounting S/C. rerun 3. tokens 202k
RW2 set3: 6 decisions all S/C. tokens 202k
SW2 set2: operator-onboarding S/NC (referral documentation); client-context S/NC; portable-template-across-modes S/C; runtime-diagnostics S/C (4/0). rerun 2, repaired 2 (client-context free ports; runtime-diagnostics free port + used bin/rimsky instead of go build). obs: naming the delivery surface in story bodies is systematic (4 of 5 SW2 NC). tokens 173k
RW5 set4: 5 S/C; operator-env-namespaced-per-service UNSUPPORTED/C (41/2). tokens 267k (retire after next)
RW4 set2: node-subscription S/C; sub-graph S/C; template S/C; write-semantics S/C; acquire-prefix-fallback S/C; acquire-unavailable-carveout UNSUPPORTED/C. tokens 325k -> RETIRED, replacement RW4b
RW1 set2: frame S/C; instance S/C; node-run S/C; node S/C; orphan-reaper UNSUPPORTED/C; parked-state UNSUPPORTED/C. tokens 329k -> RETIRED, replacement RW1b
SW3 set7: validation-mixin-uniform S/NC; claude-agent-expose-env-per-node S/C; claude-agent-mcp-servers-per-node S/C; claude-agent-session-resume S/C; publisher-protocol S/C. rerun 5. tokens 226k
RW3 set5: run-name S/C; service-spawn-flag S/C; services-source S/C; termination UNSUPPORTED/C; timeout-flag S/C; timestamp-format S/C. tokens 318k -> RETIRED, replacement RW3b
RW2 set4: image-two-stage S/C (18/0); intx-suffix-convention UNSUPPORTED/C (7/2); jcs-cyberphone S/C; layer-ordering S/C (43/0); logging-slog-only S/C; metrics-prometheus-client S/C. tokens 230k
SW2 set3: script-friendly-outcome S/C (3/0); one-shot-to-terminal S/C; compose-lifecycle S/NC (5/0); compose-namespace-guard UNSUPPORTED/C -> JUDGE. rerun 1, repaired 3, built 1 way (not passing = not nominable). tokens 215k
SW3 set8: http-node S/C; inproc-utility-executor S/C; opaque-executor-scratch S/C; claude-agent S/C. rerun 4. tokens 243k
RW2 set5: module-split S/C (4/0); postgres-pgx-v5 UNSUPPORTED/C (19/4); registry-hub-rimskyai-namespace S/C; release-attestations S/C (15/0); release-chain S/C; release-dev-mechanical S/C. tokens 244k
RW5 set5: 6 decisions all S/C. tokens 311k -> RETIRED, replacement RW5b
SW1 set3: host-agent-anonymous-mode S/C; host-agent-control-plane S/NC; host-agent-late-bind-all-protocols UNSUPPORTED/C -> JUDGE; host-agent-per-binding-overrides S/C. rerun 3, repaired 1. tokens 213k
SW3 set9: verifier-http S/C; verifier-severity-partition S/C; verifier-shape-checks S/C; validation-author S/NC. rerun 4. tokens 260k
RW4b set1: 6 decisions (async-callback x2, attribute-carry-forward, attribute-set-as-body, cascade-flags-required-no-defaults, cascade-inside-settlement) all S/C. tokens 155k
RW2 set6: release-distribution S/C; release-scan-docker-scout S/C; release-semver-sha-dot-joined S/C; revive-no-exported-rule S/C; spec-jcs-canonicalization S/C; sqlite-modernc-pure-go S/C. tokens 263k
SW2 set4: audit-artifact S/C; message-bus S/C (4/0; remediation: CLI messages tail w/o --follow returns only newest row); message-queue-coalesces-pending S/NC (payload clause is a writing defect: coalesce applies to all messages per concept:instance); message-schema S/C. rerun 3, repaired 1. tokens 247k
SW3 set10: template-sub-graph-delegation S/C; uncovered-substitution-rejected S/C; claim-scope-substitution S/C; template-error-policy S/C (4/0); producer-class-routing S/C (referral documentation). rerun 5. tokens 276k
RW3b set1: anonymous-mode S/C; api-key S/C; cascade-graph UNSUPPORTED/C; control-api S/C (note: extractor's ca-root contradiction is a separate escalation); discovery-cache S/C; dry-run S/C. tokens 210k
SW2 set5: messages-as-nodes-substitution S/NC; typed-message-substitution S/C; one-message-per-frame S/C; empty-message-wakes-roots S/C (3/0). rerun 4. tokens 265k
RW2 set7: testcontainers-go S/C; toplevel-dirs S/C; module-layout S/C (11/0); blessed-invariant-annotations S/C (obs: 17 concept docs append bare invariant numbers resolving to nothing); bundled-recipes-production-paths UNSUPPORTED/C; claim-producer-vocabulary-boundary S/C. tokens 292k
SW1 set4: host-agent-per-run-scope-isolation S/C; spawned-local-services S/C; peer-auth-mtls-mutual S/NC (coverage boundary noted in prose); peer-tls-enforced S/NC. rerun 3, repaired 1. tokens 243k
RW1b set1: persistence-database S/C; run-scope UNSUPPORTED/C; service-address-book UNSUPPORTED/C; supervisor S/NC; tag UNSUPPORTED/C; wait-set UNSUPPORTED/C. tokens 216k
SW3 set11: asset-management S/C; bundled-park-resume-recipe UNSUPPORTED/C -> JUDGE; executor-protocol S/NC; executor-reads-dispatch-context S/C. rerun 4 (1 failing = real gap). tokens 300k -> RETIRED (no replacement; story feed nearly drained). SW3 total 45 stories.
RW4b set2: child-execution-naming S/C; coverage-wildcard-asymmetry UNSUPPORTED/C; emit-work-completed S/C; entry-absorption-flag S/C; env-as-substitution-source-kind S/C; fan-out-and-delegation-are-distinct-mechanisms S/C. tokens 223k
RW5b set1: topology-test-coverage S/C; webhook-auth-required UNSUPPORTED/C; asset S/C; auto-terminal UNSUPPORTED/C; breakpoint S/C; cancel-siblings S/C. tokens 202k
SW1 set5: permissive-peer-build S/C; clean-lint S/NC; rules-doc-accuracy S/NC; substitution-doc-accuracy UNSUPPORTED/NC -> JUDGE. rerun 4. tokens 261k
SW2 set6: mcp-transport S/C (44/0); cascade-send S/C. rerun 1, repaired 1 (mcp-transport: free port, labels, +3 routes). SW2 story feed complete (22 stories, 1 escalation). tokens 290k -> RETIRED
RW2 set8: comment-hygiene-uniform-rule S/C (1639/0); config-flip UNSUPPORTED/C; design-link-annotations S/C (2311 citations resolve); doc-residue-reshape-pass S/C (2/0, 1 remediation); implementation-language-go-plus-ts S/C; migrations-no-compat-shims S/C. tokens 312k -> RETIRED, replacement RW2b for remaining 2 chunks
RW1b set2: 6 decisions all S/C (obs: advisory-locks Choice text silent on sqlite in-tx substitution; frame-isolation node-keyed latest-run lookup guarded at 2 operator callers). tokens 268k
RW3b set2: lifecycle-subscriber UNSUPPORTED/C; message-schema S/C; message S/NC; peer-auth S/C; rimsky-yml UNSUPPORTED/C; service UNSUPPORTED/C (11/1). tokens 325k -> RETIRED, replacement RW3c
SW1 set6: sensor-cron S/C; sensor-http S/NC; sensor-object-store S/C; sensor-webhook S/NC. rerun 4. SW1 story feed complete (20 stories, 2 escalations). tokens 278k (stand by)
RW4b set3: 6 decisions all S/C (obs: force-upstream-refresh same-receiver-same-sender dedup sub-clause implemented but untested; named in audit). tokens 283k
RW1b set3: guard-conformance-suite S/C; memory-gate-premise-corrected S/C; message-idempotencies-dedup-tuple S/C; migrations-append-only-numbered UNSUPPORTED/C; mode-default-most-recent S/C; non-cascade-direct-to-stale UNSUPPORTED/C. tokens 319k -> RETIRED, replacement RW1c
RW4b set4: inproc-eventstream S/C; inproc-handler-interface UNSUPPORTED/C; inproc-registry S/C; inproc-transport-client S/C; keepalive-endpoint UNSUPPORTED/C; kind-sugar-resolver S/C (obs: mutual exclusion is order-dependent, fragile). tokens 316k -> RETIRED, replacement RW4c
RW2b set1 (RW2 stream complete): parallel-cap-removal S/C; pre-v1-break-freely S/C; project-agnostic S/C; release-formal-skill S/C; release-notes-template S/C; release-semver-from-diff UNSUPPORTED/C; untagged-prose-by-module UNSUPPORTED/C. tokens 152k (idle; available for rebalancing)
RW5b set2: claim-producer S/C; claim-tree S/C; claim S/C; conformance UNSUPPORTED/C; data-processing UNSUPPORTED/C; error-policy S/C. tokens 310k -> RETIRED, replacement RW5c
SW5 set1: claim-handoff S/NC; claim-handoff-durable S/NC (text names instance termination as release trigger; measured: only delete releases); claim-producer-conformance S/C; claim-producer-filesystem S/C. rerun 3, rebuilt 1. tokens 199k
RW3c set1: 4 auth decisions S/C; validation UNSUPPORTED/C; terminal-resolution UNSUPPORTED/C. tokens 199k
SW4 set4: resume-preserves-snapshot UNSUPPORTED/C -> JUDGE; attribute-carry-forward UNSUPPORTED/C (3/1, sub-graph scope blocked) -> JUDGE; fanout-any-substitution-source S/C (5/0); fanout-intent-inheritance S/NC. REVISION: cascade-defers-during-flight (set1 S) now UNSUPPORTED -> JUDGE. rerun 5, repaired 3, built 1 (failing). blocker: builtin-kind node inside a delegated sub-graph never dispatches. tokens 219k
SW5 set2: data-processing-author S/NC; fs-fanout-expand-folder S/NC; held-abandon-cascades-abandoned S/C; held-commit-cascades-success S/C. rerun 4. SW5 story feed complete (8 stories). tokens 233k (stand by)
RW4c set1: loop-counter-shape S/C; message-sender-kind-discriminator UNSUPPORTED/C; no-event-substitution S/C; no-resume-context S/C; one-message-per-frame S/C; orphan-reaper-connection-state S/C. tokens 133k
RW1c set1: parity-expansion UNSUPPORTED/C (77/9); persistence-driver S/C; persistence-dual-backend S/C; process-role-unified-message-covers-rimsky-run S/C; scratch-column S/C; sequence-scope-monotonic S/C. tokens 159k
RW2b set2: 6 decisions all S/C. tokens 225k
RW3c set2: 4 S/C; idempotency-key-header-universal UNSUPPORTED/C; launch-integration UNSUPPORTED/C. tokens 247k
RW2b set3: 6 decisions all S/C. tokens 280k -> retire (no replacement)
RW4c set2: 5 S/C; structural-root-edge-injection-at-registration UNSUPPORTED/C. tokens 197k
SW1 set7: claim-producer-postgres S/NC (pg/swap_failed unprovoked; class-vocabulary mismatches recorded); claim-producer-protocol S/C; claim-producer-scopes-conflict S/C; commit-response-honored S/C. rerun 3, extended 1. SW1 total 24 stories. tokens 325k -> RETIRED
RW3c set3: 6 decisions all S/C (mcp-http-parity 49/0). tokens 293k
RW5c set1: event-log UNSUPPORTED/C; fan-out UNSUPPORTED/C; inertness S/C; lineage-record UNSUPPORTED/C; lineage UNSUPPORTED/C; message-sender-node S/C. tokens 225k
RW1c set2: sqlite-multiproc-safety S/C; walker-rule-per-sender-node S/C; graph S/C; observability S/C; transition-reason UNSUPPORTED/C (7/5); async-callback-persistent-registry S/C. tokens 270k
RW4c set3: 6 decisions S (terminal-tags NC text). tokens 243k
RW3c set4 (RW3 stream complete): producer-error-passthrough S/C; single-process-mode S/C; synthetic-envelope-mechanism-retired S/C; protocol-version-v1-namespaced UNSUPPORTED/C; tls-mode-validation UNSUPPORTED/C. tokens 316k -> RETIRED
RW4c set4: writeback-bumps-progress S/C. RW4 stream complete; RW4c took held chunk (event-log-payload-shapes...). tokens 250k
RW2b set4: 6 decisions all S/C. tokens 334k -> RETIRED. RW2b totals: 25 artifacts.
SW4 set5: fanout-list-array S/C; template-fan-out S/C; sub-claim-payload-substitution S/NC; lenient-marker S/C. rerun 4, repaired 3. SW4 story feed complete (25 stories, 4 escalations). tokens 245k
RW1c set3: 6 decisions all S/C (env-var-registry 86/0). tokens 316k -> RETIRED. RW1 stream complete except held tail (2).
RW5c set2: permission S/C; role-template S/C (6/0); signal UNSUPPORTED/C; terminal-tag S/C; asset-materialize-endpoint-retired S/C; empty-message-as-root-trigger S/C. tokens 297k
RW4c set5: event-log-payload-shapes UNSUPPORTED/NC; executor-unary-rpc S/C; graceful-shutdown UNSUPPORTED/C; grpc-internal-protocols UNSUPPORTED/C; licensing-dual-apache-agpl UNSUPPORTED/C (1794/79 — experiments tree); licensing-enforced-by-license-lint S/C. tokens 308k -> RETIRED
RW5c set3 (RW5 stream complete): 5 S/C; polling-audit UNSUPPORTED/C. tokens 355k -> RETIRED
RW7 set1 (tail complete): secret-at-rest-posture S/C; test-wallclock-lint-ratchet S/C; terminal-error-abandoned-as-error-class UNSUPPORTED/C; test-harness-invalidate-node-retired S/C; testing-scenario-based-e2e S/NC (10/0). tokens 139k. READING TRACK COMPLETE.
AW1 set1: cli-ls-aliases HELD; cli-json-flag-universal TRAP; cli-output-flag-is-json-superset TRAP; cli-short-flags-single-dash TRAP; cli-dry-run-flag-exists TRAP (story implication: dry-run-request-flag / dry-run-mode-floor — CLI has no dry-run flag; under a dry_run-mode key `rimsky deploy` prints "deployed" while nothing was). built 5. tokens 141k
AW2 set1: http-error-envelope-uniform TRAP; http-status-codes-conventional TRAP (bad cursor -> 500 on five collections); http-delete-idempotent TRAP; http-list-routes-paginate TRAP; http-idorkey-accepted-uniformly TRAP. built 5. tokens 196k
AW4 set1: every-protocol-has-capabilities TRAP; every-protocol-has-observability-sibling TRAP; grpc-protocols-have-http-bridge TRAP; conformance-covers-every-protocol TRAP; conformance-exit-code-machine-readable TRAP. built 5 (all pass; nomination candidates). tokens 194k
AW3 set1: env-overrides-every-config-key TRAP; env-unknown-vars-rejected TRAP; log-level-universal TRAP; metrics-on-bundled-services TRAP; health-endpoint-on-every-service TRAP. built 5. tokens 201k
AW2 set2: http-version-prefix-negotiable TRAP; http-tag-create-idempotent TRAP; http-events-streamable TRAP; http-observability-mirrors-primary TRAP; mcp-tools-cover-every-route TRAP (narrow; story implication: mcp-transport "every read and mutation" — /v1/health and /v1/auth/whoami have no tool). built 5. tokens 255k
AW4 set2: stub-mode-on-every-bundled-executor TRAP; park-controls-on-every-executor TRAP; error-classes-namespaced-uniformly TRAP; error-types-catchall-supported TRAP; error-classes-stable-across-releases TRAP. built 5. tokens 260k
AW1 set2: cli-destructive-verbs-confirm TRAP; cli-help-on-every-subcommand TRAP; cli-duration-flags-share-syntax TRAP; cli-time-window-flags-uniform TRAP; cli-thin-client-route-parity TRAP. built 5, repaired 1. obs: compose verbs send no api-key (401 past auth init); `rimsky logs` always implies --follow. tokens 235k
AW2 set3: mcp-standard-methods-present TRAP; mcp-catalog-hides-denied-tools TRAP; mcp-resource-uris-are-a-family TRAP; permission-actions-cover-full-crud TRAP (shallow); permission-scope-on-every-action TRAP. built 5. tokens 320k -> RETIRED, replacement AW2b
AW4 set3 (stream complete, 13 assumptions all TRAP): event-kinds-one-naming-scheme TRAP; event-kinds-paired TRAP; event-kinds-filterable-by-instance-and-node TRAP. built 3. tokens 314k -> RETIRED
AW3 set2: egress-guard-on-every-outbound-service TRAP; allowlist-polarity-uniform TRAP; dispatch-budget-env-clamps-node TRAP; sensor-state-dsn-uniform HELD; sensor-auth-block-uniform TRAP. built 4, rerun 3 (cited). AW3 cleanup removed AW1's exp-assumption-dispatch-defaults container -> AW1 told to re-run. tokens 296k
AW2b set1: permission-wildcards-are-globs TRAP; roles-are-server-side TRAP; read-only-role-covers-every-read-action HELD; anonymous-mode-has-an-off-switch TRAP; api-key-retrievable-after-mint TRAP. built 5. tokens 166k
AW1 set3: cli-context-flags-everywhere TRAP; template-lint-equals-registration-validation TRAP (offline clause only); dispatch-defaults-cover-every-node-timing-key TRAP (re-run clean after container collision); attribute-defaults-have-per-node-form TRAP; params-redact-applies-everywhere TRAP (secret exposure across event/audit/observability reads). built 5. tokens 296k
AW1 set4 (stream complete, 20 assumptions): node-tags-are-selectors TRAP; compose-plan-previews-up HELD; compose-restart-supervises TRAP; compose-state-key-is-declarative TRAP; admin-reset-is-scoped TRAP (confirmation half). built 5. Docker daemon went down mid-set; AW1 restarted it and re-ran. tokens 342k -> RETIRED
SESSION LIMIT: AW3 (mid set3), AW2b (mid set2), AW4 (idle, stream complete) terminated by API session limit; resuming AW3 and AW2b.
AW2b set2 (stream complete): key-expiry-emits-an-event TRAP; enroll-route-always-mounted TRAP; asset-verbs-match-across-surfaces TRAP; runtime-diagnostics-are-actionable TRAP. built 4. tokens 246k
AW3 set3+4 (stream complete): object-store-backend-is-cloud TRAP; sensors-are-ha-when-replicated HELD; backends-have-feature-parity TRAP (narrow); blob-backends-interchangeable TRAP; migrate-is-standalone-and-reversible TRAP; all-in-one-state-persists HELD; bundled-ports-do-not-collide TRAP; image-names-follow-one-scheme HELD; images-multi-arch TRAP (all 15 Hub images arm64-only); npm-package-ships-generated-clients TRAP. built 10. tokens 437k -> RETIRED. ASSUMPTION TRACK COMPLETE.
