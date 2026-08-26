# Trap triage — gaps versus design tradeoffs

This table classifies the 70 trap records at `.ok-planner/documentation/traps/`, which `/document` wrote at release `d977250c` (2026-08-16). It answers one question per trap: does the design corpus rule the measured behavior as a choice, is the corpus silent, or has the tree moved past the measurement?

Eight classification agents produced the rows on 2026-08-23, one agent per surface cluster. Each agent read its trap records, searched `.ok-planner/design/{decisions,concepts,stories}/` for coverage, and checked the current tree for staleness. The orchestrating session verified every borderline call against the named artifact's text and spot-checked the firm calls.

A **ruled** trap names a decision or concept that states the behavior or its tradeoff as a choice. A **silent** trap has no design artifact that speaks to its behavior; it is a candidate gap and the owner's question. A **stale** trap describes behavior the current tree no longer shows; the next audit re-measures it.

The 70 traps split 22 ruled, 38 silent, 10 stale.

## ruled

| trap | evidence | note | confidence |
| --- | --- | --- | --- |
| backends-have-feature-parity | decision:persistence-dual-backend | the decision states SQLite is for dev/test only and Postgres for production as a deliberate rationale, matching the still-current SQLite boot warning that demonstrates the asymmetry | firm |
| object-store-backend-is-cloud | decision:object-store-watching-model | the decision states as a deliberate choice that the shipped build registers only the filesystem backend (plus an in-memory test fixture) while the object-store abstraction merely admits future cloud backends by design | firm |
| migrate-is-standalone-and-reversible | decision:migrations-append-only-numbered | migrations are ruled append-only and forward-only by deliberate design (with rationale against rebasing/mutating), which is the direct cause of the trap's "no down direction anywhere in the runner" finding; image-entrypoint-role-selection likewise rules the unknown-role-exits-nonzero behavior behind the "no standalone rimsky-migrate route" half of the trap | firm |
| cli-thin-client-route-parity | concept:rimsky | The concept's Boundaries state the CLI does not own the control API's routes and groups verbs by operator workflow — an explicit disclaimer of full route parity, distinct from the MCP full-parity commitment (decision:mcp-http-parity); current dispatch shows no growth. | borderline |
| anonymous-mode-has-an-off-switch | concept:anonymous-mode | The concept states the state is computed from the ledger and no setting turns it on or off. | firm |
| api-key-retrievable-after-mint | concept:api-key | The concept states the server keeps only a one-way hash and surfaces the plaintext exactly once at mint and at each rotation. | firm |
| enroll-route-always-mounted | decision:service-auth-mtls | The decision ties enrollment to the mtls-on mode of the default-off switch; enroll.go mounts /enroll and /ca-root only when deps.Enroll.CA is non-nil. | borderline |
| permission-scope-on-every-action | decision:auth-grant-scope | The decision's rejected alternative explains the measured gap: per-resource ACLs rejected, dimension keys constrain by property instead. | firm |
| roles-are-server-side | concept:role-template | The concept states the server stores the expanded grant, records no role identifier, and offers no role-registration surface. | firm |
| http-observability-mirrors-primary | concept:cascade-graph | The concept frames the observability family as a purpose-built read surface, not a same-shaped mirror under a read-only permission. | borderline |
| http-version-prefix-negotiable | decision:protocol-version-v1-namespaced | The decision commits to a single v1 namespace with no carve-outs and rejects version discovery/negotiation explicitly. | firm |
| event-kinds-filterable-by-instance-and-node | concept:event-log | The concept states an entry raised outside any instance names neither instance nor node — precisely the auth.access_attempted gap measured. | firm |
| event-kinds-one-naming-scheme | decision:structured-log-kind-format | The decision explicitly carves the event log out of the SUBSYSTEM.NOUN.VERB sweep; the event-log vocabulary keeps its mixed conventions and current code confirms not stale. | borderline |
| error-classes-namespaced-uniformly | decision:acquire-prefix-fallback | The decision makes exact-match the norm and acquire/* the one carved-out wildcard family; template_validator.go still names acquire/* as the sole synthetic wildcard. | borderline |
| error-classes-stable-across-releases | decision:claude-agent-error-classes-closed | Closedness is per-executor by deliberate choice; member spellings are protocol surface owned by the executor's declaration, not a corpus-wide versioned catalog. | firm |
| error-types-catchall-supported | decision:acquire-prefix-fallback | The decision's rejected alternative establishes exact-match-only as default everywhere except acquire/*, which is why a bare * or http/* registers but never routes. | borderline |
| allowlist-polarity-uniform | decision:allowlist-defaults-open | The corpus pairs allowlist-defaults-open (reference allowlists default open) with decision:destination-allowlists-default-closed (egress default closed) as a stated, deliberate polarity split. | firm |
| every-protocol-has-capabilities | concept:observability | Capabilities is answered only by the two named observability protocols plus each primary protocol's own advertised abilities — deliberately not a uniform handshake on every service. | borderline |
| every-protocol-has-observability-sibling | concept:observability | The concept defines observability as the pair of optional service protocols, one per service kind, so the other protocols having no sibling is the stated design. | firm |
| grpc-protocols-have-http-bridge | decision:grpc-internal-protocols | The decision commits to gRPC for all declared service protocols with a small named list of deliberate HTTP-JSON exceptions, matching the measured behavior. | firm |
| npm-package-ships-generated-clients | decision:implementation-language-go-plus-ts | TypeScript is only an ambient type-declaration stub for the protocols module's wire contract, exactly the proto-files-plus-path-helper package the experiment found. | firm |
| runtime-diagnostics-are-actionable | decision:debug-channel-gate-paused-or-breakpoint | The decision rejects extending the debug-override remediation channel to a parked frame as a normal-operation degraded state, ruling diagnose-without-remediation deliberate — though it names only the parked-frame case. | borderline |

## silent

| trap | evidence | note | confidence |
| --- | --- | --- | --- |
| cli-destructive-verbs-confirm | - | No decision, concept, or story rules "no interactive confirmation outside compose" as deliberate; current tree (code:cmd/rimsky/cli/compose/down.go::147, code:cmd/rimsky/cli/instances.go::RunInstanceKill#245) confirms the same asymmetry still holds. | firm |
| cli-duration-flags-share-syntax | - | No artifact rules a uniform or deliberately non-uniform duration grammar; current source (auth_create.go day-suffix parser vs auth_handlers.go plain time.ParseDuration) confirms the three-way grammar split still holds. | firm |
| cli-help-on-every-subcommand | - | No artifact addresses the leaf-vs-family --help exit-code split; current code (main.go dispatchTemplate hardcoding family --help exit 0 vs leaf parse errors exit 2) still reproduces it. | firm |
| cli-json-flag-universal | - | No artifact rules the --json vs -o json naming split or why only auth list departs; current tree still matches the trap exactly. | firm |
| cli-output-flag-is-json-superset | - | No artifact addresses the -o/--output flag set or the silent table/text fallback to human; current code (output.go#22-30) still reproduces it. | firm |
| cli-short-flags-single-dash | - | No artifact rules stdlib flag non-clustering as a deliberate ergonomics choice; the CLI still uses stdlib flag exclusively (no cobra/pflag in go.mod). | firm |
| cli-time-window-flags-uniform | - | No artifact rules the four-flag asymmetry, RFC3339-only grammar, or watch --until's differing exit-condition meaning; current code matches every claim. | firm |
| admin-reset-is-scoped | - | decision:node-reset-clears-failure-marker rules the single-node scoping and 409 gate, but nothing addresses the missing confirmation prompt that makes the trap contradicted. | borderline |
| key-expiry-emits-an-event | - | No artifact addresses expiry as an audit-event kind; kinds.go still declares only auth.key_created/revoked/rotated. | firm |
| permission-actions-cover-full-crud | - | No artifact documents the action registry as intentionally excluding instance:update/asset:create/message:delete; the registry still lacks them. | firm |
| permission-wildcards-are-globs | - | No artifact documents the closed three-shape wildcard grammar or the registry-skips-wildcards gap. | firm |
| sensor-auth-block-uniform | - | decision:webhook-auth-required rules only the inbound webhook sensor; nothing addresses http-poll outbound credentials or the silent drop of an unrecognized auth block. | firm |
| http-delete-idempotent | - | The corpus's idempotency decisions govern message-send dedup keys, not DELETE-route idempotence; a second delete still returns 404. | firm |
| http-error-envelope-uniform | - | No artifact rationalizes the divergent error-envelope shapes across core CRUD, observability, and MCP; all four shapes still stand. | firm |
| http-events-streamable | - | No artifact addresses GET /v1/events streaming-vs-polling; the CLI --follow is still a client-side poll loop. | firm |
| http-idorkey-accepted-uniformly | - | No artifact addresses the {idOrKey} vs {id} route-spelling split; assets/frames/messages/debug_override still spell {id}. | firm |
| http-list-routes-paginate | - | No artifact asserts a uniform pagination contract or its exceptions; the measured departures still match verbatim. | firm |
| http-status-codes-conventional | - | The malformed-cursor sub-finding is stale (ErrInvalidCursor now maps to 400) but the core non-uniformity holds uncontradicted: unknown id reads 200 empty on messages and claim-holders; the 400/404 split persists. | borderline |
| http-tag-create-idempotent | - | concept:tag and story:tag-management do not address duplicate tag creation; tags.go still returns 409 on repeat POST. | firm |
| mcp-catalog-hides-denied-tools | - | No artifact addresses whether the per-key MCP catalog reflects scope-level (vs action-level) grants; catalog.go filtering is action-only, matching the trap. | firm |
| mcp-resource-uris-are-a-family | - | No artifact addresses the rimsky:// resource-URI scheme's scope; code still enumerates only the two breakpoint-hit forms. | firm |
| mcp-standard-methods-present | - | No artifact states which base MCP methods (ping, prompts, subscribe, sampling, roots, logging) are deliberately out of scope. | firm |
| event-kinds-paired | - | No artifact addresses acquisition/release kind symmetry; kinds.go still shows claim_acquired with no claim_released and subclaim.acquired with no release counterpart. | firm |
| attribute-defaults-have-per-node-form | - | No artifact states that template-level defaults.attributes is deliberately by_executor only; template.go still defines only a ByExecutor field. | firm |
| dispatch-defaults-cover-every-node-timing-key | - | decision:three-dispatch-deadlines rules only the three deadline keys; nothing addresses max_retries/retry_backoff or states their exclusion as a choice. | firm |
| env-overrides-every-config-key | - | decision:config-yaml-loading-policy and decision:env-var-registry describe the mechanisms but neither states as a tradeoff that a guessed RIMSKY_<KEY> outside the enumerated set is silently inert. | borderline |
| env-unknown-vars-rejected | - | Strict KnownFields rejection is ruled for YAML keys; no artifact rules the env-var-name asymmetry the trap measures. | borderline |
| conformance-covers-every-protocol | - | No artifact explains why the HostDaemon protocol carries no conformance suite; the gap is unaddressed rather than ruled. | borderline |
| conformance-exit-code-machine-readable | - | decision:exit-codes and decision:progress-flags govern rimsky run / compose run, not the conformance subcommands; no artifact rules the conformance CLI's exit-code or machine-readable-output shape. | firm |
| template-lint-equals-registration-validation | - | The corpus commits to deployment-scoped validation but never states whether template lint must reach a deployment; RunTemplateLint always dials the control API. | borderline |
| asset-verbs-match-across-surfaces | - | No artifact states CLI-vs-REST-vs-MCP verb parity as a rule; lineage is a client-side composition and materialization-history has no CLI verb. | firm |
| compose-restart-supervises | - | No artifact states restart is an apply-time classification rather than live supervision; classifyRestart runs only inside plan/apply. | firm |
| compose-state-key-is-declarative | - | No artifact states the one-way state semantics or the live-instance-blocks-manifest refusal. | firm |
| egress-guard-on-every-outbound-service | - | verifier-http now wires the shared guard but the openlineage subscriber's emitter still dials with a bare http.Client; the asymmetry persists and no artifact rules it deliberate. | borderline |
| metrics-on-bundled-services | - | Metrics decisions cover the three core roles' export; no artifact states bundled services deliberately lack a metrics port. | firm |
| node-tags-are-selectors | - | No artifact states --tag/--tag-prefix are undefined on node-listing verbs or that the CLI omits node tags while HTTP filtering works. | firm |
| park-controls-on-every-executor | - | concept:parked-state defines the state and wake paths but never claims the probe attributes are a platform-wide family; the four-executor asymmetry is unstated. | firm |
| stub-mode-on-every-bundled-executor | - | No artifact documents stub_response/stub-mode; the four-way asymmetry across bundled executors is unstated in the corpus. | firm |

## stale

| trap | evidence | note | confidence |
| --- | --- | --- | --- |
| blob-backends-interchangeable | lib/foundation/persistence/postgres/migrations/046-attribute-bytes-in-the-row.sql:1-27 | migration 046 drops value_handle/value_handle_backend and rimsky_blob_orphans and moves attribute/scratch data into byte columns, so the pluggable blob-backend mechanism the trap exercised no longer exists in the current tree | firm |
| cli-context-flags-everywhere | code:cmd/rimsky/cli/compose/apply.go::clientForContext#404 | At release d977250c clientForContext set compose origin but never called SetAPIKey, dropping --key/RIMSKY_API_KEY silently; commit d6769c97 added the SetAPIKey call so all four compose verbs now thread credentials correctly. | firm |
| cli-dry-run-flag-exists | code:cmd/rimsky/cli/dryrun_preview.go | The corpus rules the server-side per-request mechanism but is silent on CLI flag exposure; the sharper measured harm — rimsky deploy silently discarding the dry-run envelope and reporting a write that never happened — is fixed: dryrun_preview.go routes any dry_run:true body through ReportDryRunPreview. | borderline |
| mcp-tools-cover-every-route | code:lib/control/controlapi/actions.go#237-254 | At d977250c neither health:probe nor auth:whoami carried an MCPTools entry; the current tree adds both, closing the measured gaps (the terminate/kill naming inversion still stands). | firm |
| dispatch-budget-env-clamps-node | lib/services/executors/claude-agent/clirunner.go:142 | Commit 90f84aec renamed the variable to RIMSKY_CLAUDE_AGENT_DISPATCH_MAX_USD per decision:operator-env-namespaced-per-service; the variable the trap names is no longer read (the fallback-not-ceiling behavior likely persists under the new name). | firm |
| log-level-universal | lib/protocols/serverkit/logging.go:14-46 | serverkit logging is now wired into every bundled service and the host daemon, and an unrecognized value emits SERVERKIT.LOGLEVEL.UNRECOGNIZED naming variable and value; both halves of the measured gap no longer hold. | firm |
| bundled-ports-do-not-collide | lib/foundation/ports/ports.go:11-31 | Supervisor callback now defaults to 8081 and the proxy to 8090, so neither measured collision exists; a fitness test enforces zero collisions across every shipped default. | firm |
| health-endpoint-on-every-service | lib/runtime/callback.go:150 | The supervisor callback listener now serves /v1/health per the versioning decision; the measured plain-/health 200 no longer holds. | firm |
| images-multi-arch | Makefile:370-373,453-456 | push-images now builds and pushes every tag as a linux/amd64+linux/arm64 index with a closing platform-verification step. | firm |
| params-redact-applies-everywhere | decision:secret-at-rest-posture | The params_redact field and redactor were deliberately removed; instance params now return unmasked everywhere per the current decision. | firm |

