# Completion report: 2026-08-06-ruled-intake-drain

Execution record, kept current as stages land. Executor: goal-driven
inline session, 2026-08-06.

# Certification — Sprint: Ruled-intake drain

Status: certified clean

## Outcomes delivered

- Race-detector retirement completed in the corpus: the race-gate
  decision and its audit record are gone, the release-chain and
  race-injection decisions read race-free, and the decisions TOC
  matches. The build has carried no race gate since the prior commit;
  the corpus now agrees.
- A template that misspells `retry_backoff.kind` or `.jitter` is now
  refused at registration with a hard error naming the legal values —
  no more silent flat-backoff fallback discovered in production
  timing.
- The two genuinely closed template vocabularies are named Go types:
  `cascade_mode` (the type moved to the spec package, one type
  repo-wide) and claim `intent:` (reusing the protocol layer's
  type); the two registry-resolved `kind:` fields stay bare strings.
- `rimsky instance create`, `rimsky run`, and both compose paths now
  honor `env:RIMSKY_AGENT_IDENTITY_FILE` when guessing the target
  agent, matching the agent daemon's precedence.
- The host-agent spawn allowlist is settable by env
  (`env:RIMSKY_AGENT_ALLOW_PATHS`); flags still win; unset stays
  open.
- The claude-agent executor's inert declared-tags surface is gone;
  it advertises no tags until something actually emits them.
- The write-only `max_retries_without_progress` column is dropped in
  both backends (migration 041); the consumed read-side counter
  stays.
- The three compose demo scripts run from a vendored `examples/` +
  `lib/protocols/` copy with no parent checkout (proven by an
  out-of-tree run); their fixtures live inside the examples module.
- The proxy verifies an HTTPS control API against
  `env:RIMSKY_CONTROL_API_CA` (same anchor as every bundled
  service); a pinned CA over a plaintext URL refuses startup.
- Under mutual-TLS peer auth the proxy enrolls through the standard
  machinery, renews its leaf without restart, and serves the
  peer-service protocols on a separate listener that requires and
  verifies client certificates against the deployment CA; the
  agent-facing listener is unchanged. `peerauth`/`mtlstest` moved to
  `lib/protocols` so proxy and bundled services share one
  implementation.

## Divergences

- Naming: the sprint's decision says "supervisor-facing" listener;
  the proxy source names it "peer" (`RIMSKY_PROXY_PEER_GRPC_PORT`,
  peer-facing log lines) because an existing pin test bans the word
  "supervisor" in the proxy package (supervisor-identity blindness).
  Corpus keeps the sprint's wording; no behavior difference.
- Executor call (recorded above): `CascadeMode` was MOVED from the
  cascade package rather than newly declared, keeping exactly one
  type repo-wide; claim intent reuses `claimproducer.Intent` (the
  ruling's preferred branch).
- Executor call: `peerauth` + `mtlstest` relocated from
  services-internal to `lib/protocols` (strict DRY — the proxy
  cannot import services-internal packages; both fit the protocols
  dependency budget); their license headers switched to Apache-2.0
  per the per-surface license rule.
- Fixer calls (review-fix loop): added a third `.gitignore`
  stray-binary entry (`/stub-executor`, the repo-root build product)
  beyond the two the finding named, and repaired the surrounding
  comment that still described the old fixture location.
- Corpus repairs mid-cycle: none.
- Architect refutations: none (no kickbacks).

## Findings fixed

- Sprint alignment: 1 — a stray staged 15MB compiled stub-executor
  binary (fixed: removed; `.gitignore` covers all three build-product
  paths for the moved fixture). All 8 deltas byte-identical, all 10
  work items realized, changed corpus coherent.
- Test suites: clean — full lint (all five modules incl.
  license-lint) and all five module suites green against
  content-addressed images built from this tree. Two registry
  regenerations were part of the work (env-var registry, wallclock
  baseline), recorded above.
- Mechanical floor: clean — every `@concept:`/`@story:`/`@decision:`
  annotation in the changed files resolves to a live artifact.
- Code review: 3 (the same binary, its `.gitignore` root cause, and
  a README citing the deleted fixture path) — all fixed, re-review
  clean. TLS trust paths, the peerauth move, both named-type sweeps,
  migrations, and the harness reader fix verified clean.

## Issues promoted

None. The review-fix loop closed in one cycle with no kickbacks, no
dissolutions, and no cap escalation.

## Stages landed

### Standalone corpus deltas

- Retired `decision:race-gate-split`: deleted
  `.ok-planner/design/decisions/race-gate-split.md` and its
  implementation-audit record
  `.ok-planner/audits/decisions/race-gate-split.md`. Grepped the tree
  for dangling `race-gate-split` references outside `.ok-planner/`:
  none.
- Amended `decision:release-chain` and
  `decision:race-injection-hooks` — final-form bodies copied verbatim
  from the sprint.
- Amended `concept:terminal-resolution`, `concept:inertness`,
  `concept:message`, `concept:publisher` — final-form bodies copied
  verbatim from the sprint.

### Retry-backoff registration validation

- New `validateRetryBackoff` in the template validator
  (lib/graph/node/template_validator.go), wired into the per-node
  validation loop next to the action-vocabulary check; rejects unknown
  `retry_backoff.kind` / `.jitter` as hard errors in the standard
  validation-error shape; empty (unset) values stay legal.
- New tests in
  lib/graph/node/template_validator_retry_backoff_test.go covering
  unknown kinds, unknown jitters, and all canonical/unset values.
- Applied the amended error-policy concept body verbatim.

### Named types for the two closed template vocabularies

- `CascadeMode` moved from lib/foundation/cascade/state.go into
  lib/foundation/spec/enums.go (the sibling closed-set home:
  BackoffKind, JitterKind, AggregationKind, ClaimLifetime all live in
  spec); `TemplateNodeDef.CascadeMode` is now the named type; all 45
  `cascade.CascadeMode*` references swept to `spec.CascadeMode*`
  across 15 files; redundant conversions dropped; unused imports
  pruned.
- Claim `intent:` reuses the protocol layer's existing
  `claimproducer.Intent` (foundation→protocols is the sanctioned
  import direction, with precedent in lib/foundation/locks):
  `NodeClaimProducerRef.Intent` typed; `clientiface.
  ValidateClaimBinding.Intent` typed with the string conversion at the
  proto wire boundary; the validator's intent switch now uses the
  named consts; test helper signature updated.
- The two registry-resolved `kind:` fields stay bare strings, per the
  ruling.

### CLI target-agent guess honors the identity-file env override

- New `hostagent.IdentityFilePath()` (env override
  `RIMSKY_AGENT_IDENTITY_FILE`, exported as
  `hostagent.IdentityFileEnvVar`, else the default path); the CLI's
  `ResolveTargetAgent` — the single shared site behind instance
  create, run, and both compose paths — now resolves through it,
  matching the daemon's precedence. Precedence pinned by three tests
  in cmd/rimsky/cli/target_agent_test.go.

### Spawn-allowlist env parity

- `LoadConfigFromEnv` gains `RIMSKY_AGENT_ALLOW_PATHS`
  (comma-separated globs, trimmed, empty entries dropped; unset stays
  open). Flag still wins: the runAgentStart flag-override block is
  extracted into pure `applyAgentStartFlags` so the flag/env
  precedence is pinned by test (cmd/rimsky/cli/agent_test.go) along
  with the parsing tests (lib/runtime/hostagent/config_test.go).

### Drop claude-agent declared-tags surface

- `RIMSKY_EXECUTOR_DECLARED_TAGS`, `DeclaredTags()`, the Opts field,
  the ObservabilityServer field/param, and the capabilities-payload
  line removed from lib/services/executors/claude-agent; the bundled
  registry no longer advertises tags for claude-agent. Sibling
  executors keep their code-backed declared tags.

### Drop the write-only no-progress tuning column

- New migration 041 in both backends drops
  `max_retries_without_progress`; `UpdateDispatchTuning` removed from
  the persistence interface, both impls, three test fakes, and its
  runtime caller in the park path; orphaned `intPtrOrNullPark` helper
  deleted. `consecutive_retries_no_progress` (read-side) stays.

### Self-contained compose demos

- `git mv` of stub-executor and sample-manifest from
  cmd/rimsky/cli/compose/testdata/ into examples/compose/; the three
  demo scripts now resolve fixtures from their own directory
  (script_dir), build the stub inside the examples module, and keep
  the `RIMSKY_BIN` override; the two scenario tests repointed.
  Proven: one-shot demo runs from a vendored copy containing only
  `examples/` + `lib/protocols/` (the module's stated dependency)
  outside the checkout.

### Proxy control-API trust anchor

- Applied the new `decision:host-agent-proxy-enrollment` body verbatim
  (pulled forward from the enrollment stage — the CA-anchor work
  realizes the same decision and cites it).
- Proxy config gains `RIMSKY_CONTROL_API_CA` (same env name and PEM
  format as the bundled services, via `enroll.EnvControlAPICA`); new
  `controlAPIHTTPClient` builds the pinned-pool client whenever the
  control-API URL is HTTPS; CA set + plaintext URL is a startup
  error; CA unset keeps the default transport. All three outbound
  control-API clients (both instance fetchers and the register
  identity verifier) now share one client built once at startup.
- Functional tests: anchored client verifies a private-CA HTTPS
  server, unanchored client rejects it, plaintext+CA refuses startup,
  unset CA yields the default transport.

### Proxy enrollment and split serving

- Moved `lib/services/internal/peerauth` → `lib/protocols/peerauth`
  and `lib/services/internal/mtlstest` → `lib/protocols/mtlstest`
  (strict DRY: the proxy lives in the root module and cannot import
  services-internal packages; both fit the protocols module's
  dependency budget — stdlib + grpc + enroll). All fifteen services
  import sites swept; license headers switched to Apache-2.0 (the
  protocols/examples surfaces are Apache).
- Under `RIMSKY_PEER_AUTH=mtls` the proxy enrolls at startup via
  `peerauth.LoadFromEnv` (label "host-agent-proxy", fail-closed) and
  renews via `StartMaintain` — the same machinery and renewal cadence
  as every bundled service. `buildProxyServers` splits serving: the
  peer-service protocols (executor, claim-producer, lifecycle,
  observability) move to a second listener
  (`RIMSKY_PROXY_PEER_GRPC_PORT`, default 9091) whose TLS config
  requires and verifies client certificates against the deployment
  CA; the agent-facing listener carries only the host-agent protocol
  and keeps its existing posture. Peer-auth none keeps the
  single-listener shape unchanged.
- Seam tests: enrolled identity serves the peer listener and accepts
  a deployment-CA client cert while refusing certless clients; under
  mtls the agent listener carries ONLY the host-agent service; under
  none one server carries everything. Renewal mechanics
  (two-thirds-TTL renewal, hot cert swap, injected clock) are pinned
  by the moved peerauth suite.
- CLAUDE.md proxy gotcha updated (new env vars, split listener, the
  no-"supervisor"-in-source pin).

### Decisions TOC refresh

- Dropped the race-gate-split row, added the
  host-agent-proxy-enrollment row, re-derived the
  race-injection-hooks row to the amended Choice; the release-chain
  row already matched its amended Choice.

## Divergences

- The sprint's decision text says "supervisor-facing protocols /
  listener"; the proxy source deliberately bans the word
  "supervisor" (an existing pin test enforces supervisor-identity
  blindness), so the code names the split surface "peer"
  (`RIMSKY_PROXY_PEER_GRPC_PORT`, `registerPeerProtocols`,
  peer-facing log lines). The corpus keeps the sprint's wording; the
  pin keeps the code's. No behavior difference.

### Build gates

- `make lint` green across all five modules (includes license-lint;
  the files moved into protocols/examples switched to Apache-2.0
  headers, which the license checker enforces per-surface).
- All five module suites green: `make test-root`, `test-foundation`,
  `test-protocols`, `test-services`, `test-examples`, run against
  freshly built `core-images service-images test-images` (the
  content-addressed tags demand images from the exact working tree).
- Regenerated the env-var registry (`go run ./tools/env-registry`:
  +RIMSKY_AGENT_ALLOW_PATHS, +RIMSKY_PROXY_PEER_GRPC_PORT,
  −RIMSKY_EXECUTOR_DECLARED_TAGS) and the wallclock-lint baseline
  (stub-executor's two recorded idioms moved out of cmd/ with the
  fixture relocation).
- One transient full-run kill (test/scenarios at the 300s per-binary
  ceiling under maximal parallel Docker load) did not reproduce on
  rerun: the package passes in 138s standalone and the subsequent
  full `make test-root` run was green. No test was changed.

## Bugs found and fixed along the way (fix-every-bug rule)

- examples/compose/audit-artifact-demo.sh queried the retired `phase`
  column of `table:rimsky_node_runs` (retired by the 001-initial
  consolidated schema; the column is `state`, success-terminal
  `fresh`). Rewrote the queries and expectations; demo passes.
- The services harness passed one-shot `strings.NewReader` readers as
  testcontainers `ContainerFile` content. On a boot retry (the
  harness retries container boot 3×) the exhausted reader produced an
  EMPTY config file in the retried container, turning any first-boot
  Docker flake into a deterministic
  "persistence.driver is required" boot failure — observed as
  TestSingleProcessAllInOne_MemoryBlobAcrossRoles failing under
  full-suite load. Fixed at the root with
  `rereadableContainerFile` (content staged to a temp file,
  re-readable on every attempt) and swept all nine reader sites in
  the harness. Suite rerun green.
- test/support/scenario serialized the now-typed claim intent into
  its template JSON as the named type; the wire value is a string —
  fixed the serializer (`string(s.Intent)`), not the test.

## Calls made where the sprint was silent

- The claim-intent named type reuses `claimproducer.Intent` (the
  ruling's preferred branch); nothing suggested cross-module reuse is
  unwanted.
- The CascadeMode named type was MOVED from the cascade package into
  spec rather than newly declared, so the repo keeps exactly one
  CascadeMode type (uniformity rule); the cascade package no longer
  declares it.
