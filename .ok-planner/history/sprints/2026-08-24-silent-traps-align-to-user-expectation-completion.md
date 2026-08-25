# Completion report: Align twenty silent traps to what a user expects

Sprint: `2026-08-24-silent-traps-align-to-user-expectation.md`

## Stages

Each build stage names the work items it groups by the trap slug the sprint uses, plus the corpus deltas the stage lands. The builder adds each new decision's catalog bullet to `.ok-planner/design/decisions.md` in the stage that lands the decision; the decision-catalog work item in stage 5 confirms all five bullets are present.

1. **CLI flag grammar** — done. Items: `cli-help-on-every-subcommand`, `cli-short-flags-single-dash`, `cli-json-flag-universal`, `cli-output-flag-is-json-superset`, `cli-duration-flags-share-syntax`. Deltas: new decision `short-flags-single-letter`.
2. **CLI verbs** — done. Items: `cli-destructive-verbs-confirm` (with `admin-reset-is-scoped` folded in), `cli-time-window-flags-uniform` (with the new `rimsky audit` verb), `node-tags-are-selectors`. Deltas: none.
3. **HTTP and MCP control surface** — done. Items: `http-idorkey-accepted-uniformly`, `http-list-routes-paginate`, `http-status-codes-conventional`, `http-tag-create-idempotent`, `mcp-standard-methods-present`. Deltas: new decision `mcp-base-methods-scope`.
4. **Runtime and configuration** — done. Items: `key-expiry-emits-an-event`, `dispatch-defaults-cover-every-node-timing-key`, `sensor-auth-block-uniform`. Deltas: amend concept `api-key`; new decisions `dispatch-defaults-cover-every-node-timing-key`, `http-poll-sensor-auth-outbound`.
5. **Services, conformance, and compose** — done. Items: `conformance-covers-every-protocol`, `compose-state-key-is-declarative`, `stub-mode-on-every-bundled-executor`, decision catalog (the five new bullets). Deltas: new decision `compose-undeployed-is-registered`.
6. **Finish the completion report** — done. The session: five build stages marked done by their builders, `## Work done` records each, `## Divergences` carries F1–F4 and D2–D42, and the standing reviewer's ledger closed empty after one fix-only round (L1–L29, all closed; see the ledger file beside this report).
7. **Run `/certify-work .ok-planner/sprints/2026-08-24-silent-traps-align-to-user-expectation.md`** — done. The session: the gate ran three edit rounds and exited at round 4 with every ledger row settled; the presentation follows the work-done record below.
8. **Walk the presentation** — pending. The session.
9. **Offer archive-and-commit** — pending. The session.

## Work done

The builder records each stage here as it lands.

### Stage 1 — CLI flag grammar

**Corpus delta.** Landed `.ok-planner/design/decisions/short-flags-single-letter.md` verbatim from the sprint and added its one-line bullet to `.ok-planner/design/decisions.md` in alphabetical position.

**`cli-help-on-every-subcommand`.** Added three shared helpers to `cmd/rimsky/cli/flags.go`: `UsageLine` builds a node's `usage: rimsky <node> <args>` banner, `SetUsage` installs it above the flag set's defaults, and `ParseVerbFlags` captures the parser's output and routes it — `flag.ErrHelp` prints the banner and the flags to stdout and returns exit 0, any other parse error prints to stderr and returns exit 2. `UsageError` prints the same banner to stderr and returns 2, so each node's usage text lives in exactly one place. Every flag-set-owning node now goes through it: the 34 verbs behind `runWithCommon`, the five auth verbs with their own flag sets, the five `ctx` verbs, the three `daemon` verbs, the four `compose` reconcile verbs and `compose run`, the seven `conformance` subcommands, and `rimsky version`. The group dispatchers already answered help on stdout with exit 0 and are unchanged. Each verb's inline `fmt.Fprintln(os.Stderr, "usage: …")` arm became `return UsageError(fs)`.

**`cli-short-flags-single-dash`.** `RegisterCommonFlags` now registers `-y` beside `--yes`; `instance events` and `messages tail` register `-f` beside `--follow`; `compose up|down|plan|status` keep `-f` as the manifest path; `--force` has no short form. The root usage names the grammar.

**`cli-json-flag-universal`.** `auth list` moved onto the common flag set and dropped `--json`; its human rendering is now a real `EmitTable` listing. The four read verbs with no `-o` gained it: `auth show`, `auth status`, `ctx current`, `daemon status`. `ctx current` and `daemon status` take `RegisterOutputFlags` (format plus `--no-color`) rather than the whole common set, because they reach no control API. `daemon status` grew a `daemonStatusReport` struct so its structured form has fields; `runDaemonStatus` now gathers that report and renders it.

**`cli-output-flag-is-json-superset`.** `Format` gained `FormatYAML` and `FormatTable`; `ParseFormat` accepts `human|json|yaml|table` and errors on anything else, naming the accepted set. `EmitYAML` marshals through the JSON shape so `-o yaml` carries exactly the fields `-o json` does. One renderer, `Render(format, verb, value, tables, human)`, is the single rendering path: it emits the structured form on stdout for json and yaml, refuses `-o table` on a verb whose `tables` argument is `NoTable` with an error naming the verb, and otherwise runs the verb's human closure. Every verb's `if common.Format == FormatJSON { … }` branch became a `Render` call, so structured output is the only thing on stdout in structured mode and human narration stays on stderr. The streaming verbs (`instance events`, `messages tail`, `watch`) use `Format.Structured()` plus `EmitStructured` per line.

**`cli-duration-flags-share-syntax`.** Deleted `parseExpiresDuration`; `auth create-key --expires` now parses with `time.ParseDuration` alone, so `30d` is a usage error. `rimsky conformance executor|claim-producer --retention-test-seconds` became `--retention-test`, a duration flag; `ObservabilityCheckOpts.RetentionTestSeconds int` became `RetentionTest time.Duration` in both conformance kits and the two retention probes take the window directly.

**Tests.** New `cmd/rimsky/help_test.go` builds the CLI and walks all 87 nodes of the verb tree with `--help` and `-h`, proving each exits 0, writes its usage to stdout, and writes nothing to stderr; a second test proves each node names itself in its own help. New `cmd/rimsky/cli/flag_grammar_test.go` proves `-y` confirms a destructive verb, `-f` streams like `--follow`, `-o table` renders the listing and refuses a verb with no table, `-o yaml` and `-o json` carry the same fields, an unrecognized `-o` value fails instead of falling back to human output, and `ctx current -o json` emits JSON. `output_test.go` covers the format grammar and the YAML/JSON field equality directly. `auth_subcommands_test.go` proves `auth list -o json` puts a JSON array on stdout and that `--expires 30d` is now rejected. `conformance_test.go` proves every conformance subcommand answers help on stdout with exit 0.

**Files changed.**

- `.ok-planner/design/decisions/short-flags-single-letter.md` (new)
- `.ok-planner/design/decisions.md`
- `cmd/rimsky/main.go`
- `cmd/rimsky/conformance.go`
- `cmd/rimsky/conformance_test.go`
- `cmd/rimsky/help_test.go` (new)
- `cmd/rimsky/cli/flags.go`
- `cmd/rimsky/cli/output.go`
- `cmd/rimsky/cli/output_test.go`
- `cmd/rimsky/cli/flag_grammar_test.go` (new)
- `cmd/rimsky/cli/templates.go`
- `cmd/rimsky/cli/instances.go`
- `cmd/rimsky/cli/tags.go`
- `cmd/rimsky/cli/nodes.go`
- `cmd/rimsky/cli/admin.go`
- `cmd/rimsky/cli/asset.go`
- `cmd/rimsky/cli/health.go`
- `cmd/rimsky/cli/lineage.go`
- `cmd/rimsky/cli/messages.go`
- `cmd/rimsky/cli/parked.go`
- `cmd/rimsky/cli/watch.go`
- `cmd/rimsky/cli/run.go`
- `cmd/rimsky/cli/context.go`
- `cmd/rimsky/cli/daemon.go`
- `cmd/rimsky/cli/auth_create.go`
- `cmd/rimsky/cli/auth_init.go`
- `cmd/rimsky/cli/auth_list.go`
- `cmd/rimsky/cli/auth_list_test.go`
- `cmd/rimsky/cli/auth_login.go`
- `cmd/rimsky/cli/auth_revoke.go`
- `cmd/rimsky/cli/auth_rotate.go`
- `cmd/rimsky/cli/auth_show.go`
- `cmd/rimsky/cli/auth_status.go`
- `cmd/rimsky/cli/auth_subcommands_test.go`
- `cmd/rimsky/cli/compose/apply.go`
- `cmd/rimsky/cli/compose/down.go`
- `cmd/rimsky/cli/compose/run.go`
- `cmd/rimsky/cli/compose/template_run.go`
- `cmd/rimsky/cli/compose/compose_dispatch_test.go`
- `lib/protocols/conformance/executor/observability_check.go`
- `lib/protocols/conformance/claimproducer/observability_check.go`
- `test/plumbline/cli_output_format_test.go`

**Checks run.** `go build ./...`, `go vet ./...` in all four modules, `make lint` (golangci-lint in all four modules plus license-check and logkind-lint), `go run ./tools/wallclock-lint`, the plumbline lint over every edited tree, `go test -timeout 0 -short ./cmd/... ./test/plumbline/...`, and `go test -timeout 0 -short ./conformance/...` in `lib/protocols`. All pass. `TestCtxDemo` needs `RIMSKY_IMAGE_TAG` and built images, so it is excluded by `-short` and was not run.

### Stage 2 — CLI verbs

**Corpus delta.** None. The stage carries three annotations at the sites it added: `// @story: audit-log-read` on `RunAudit` and `Client.ListAudit`, and `// @concept: node` on the node-tag selection path.

**`cli-destructive-verbs-confirm` (with `admin-reset-is-scoped`).** New `cmd/rimsky/cli/confirm.go` holds the lifted spine: `ConfirmDestructive(yes, interactive, in, out, targets)` prints the targets, prompts `Proceed? [y/N]` and reads the answer when the caller is interactive, and refuses with the `--yes` instruction when it is not; `IsTerminal` answers whether stdin is a terminal; `ConfirmDestructiveTargets(yes, targets...)` is the one call a verb makes. `cmd/rimsky/cli/compose/apply.go` lost its private `confirmDestructive` and `isTerminal` and now formats its destructive steps into target lines through `destructiveStepTargets`, so compose and the verbs share one prompt. Nine verbs gained the gate, each naming its target: `tag rm`, `instance delete`, `instance kill`, `template undeploy`, `template rm`, `auth revoke`, `admin reset`, `lineage prune`, `asset delete`. Every gate sits before the verb's first HTTP call, so a refused verb sends no request. `auth revoke` owns its flag set and gained `--yes`/`-y` through the new shared `RegisterYesFlag`, which `RegisterCommonFlags` now also uses. `admin reset` reads the `--yes` it has always parsed. `instance kill` keeps `--force` as its own precondition and `--yes` answers the prompt alone (D10).

**`cli-time-window-flags-uniform` (with the new `rimsky audit` verb).** `messages tail` gained `--since` and `--until`, wired to the route's `delivered_after` and `delivered_before` parameters. New `cmd/rimsky/cli/audit.go` and `cmd/rimsky/cli/client_audit.go` add `rimsky audit`, reading `GET /v1/audit` with `--since`/`--until` plus the route's other filters (D12), walking `next_cursor` to exhaustion and rendering a table of occurrence, id, kind, key, action, and status. `watch --until` became `--until-state`, freeing `--until` for the window grammar; `lineage prune --before` became `--until`, and the wire field stays `before`. `asset lineage` and `instance nodes` took no window flags and still take none. `cmd/rimsky/main.go` routes the new verb and names it in the root usage; `cmd/rimsky/help_test.go` walks it with every other node.

**`node-tags-are-selectors`.** `cli.Node` carries the `tags` the server already returns. `Client.ListInstanceNodes` now takes a `ListNodesQuery` (`tag`, `cursor`, `limit`), and `PagedListInstanceNodes` walks the cursor. `rimsky instance nodes` shows a `TAGS` column, filters by `--tag` through the route's existing `tag` parameter, and filters by `--tag-prefix` client-side. `rimsky node get` is unchanged. The five other `ListInstanceNodes` call sites pass an empty query; `instance nodes` and `instance status` now page to completion (D11).

**Reviewer findings on stage 1, fixed in this stage.** L1–L10, listed with their dispositions in the reply that closed the stage. The structural one: flag resolution now refuses `-o table`, so `Render` renders and decides nothing (D14).

**Tests.** New `cmd/rimsky/cli/destructive_confirm_test.go` proves every one of the nine destructive verbs refuses with exit 2 and sends no request when it cannot ask and no `--yes` was given, that a confirmed verb reaches the deployment and removes only its target, and that the prompt proceeds on `y` alone. New `cmd/rimsky/cli/time_window_test.go` proves `rimsky audit` narrows to `--since`/`--until` and emits JSON on stdout, that `watch` names its exit condition with `--until-state` and no longer takes `--until`, and that `lineage prune` names its cutoff with `--until` and no longer takes `--before`. New `cmd/rimsky/cli/node_tags_test.go` proves the TAGS column, the server-side `--tag` selection, and the client-side `--tag-prefix` selection. `messages_test.go` gained the delivery-window proof. `flag_grammar_test.go` proves `-y` on `tag rm` (L10), that the three streaming reads refuse `-o table` before they stream (L1), and that streamed `-o yaml` parses back one document per record (L5).

**Files changed.**

- `cmd/rimsky/cli/confirm.go` (new)
- `cmd/rimsky/cli/audit.go` (new)
- `cmd/rimsky/cli/client_audit.go` (new)
- `cmd/rimsky/cli/destructive_confirm_test.go` (new)
- `cmd/rimsky/cli/time_window_test.go` (new)
- `cmd/rimsky/cli/node_tags_test.go` (new)
- `cmd/rimsky/main.go`
- `cmd/rimsky/help_test.go`
- `cmd/rimsky/conformance_test.go`
- `cmd/rimsky/cli/flags.go`
- `cmd/rimsky/cli/output.go`
- `cmd/rimsky/cli/output_test.go`
- `cmd/rimsky/cli/flag_grammar_test.go`
- `cmd/rimsky/cli/admin.go`
- `cmd/rimsky/cli/asset.go`
- `cmd/rimsky/cli/auth_list.go`
- `cmd/rimsky/cli/auth_revoke.go`
- `cmd/rimsky/cli/auth_show.go`
- `cmd/rimsky/cli/auth_status.go`
- `cmd/rimsky/cli/auth_subcommands_test.go`
- `cmd/rimsky/cli/client_instances.go`
- `cmd/rimsky/cli/client_nodes.go`
- `cmd/rimsky/cli/client_test.go`
- `cmd/rimsky/cli/context.go`
- `cmd/rimsky/cli/daemon.go`
- `cmd/rimsky/cli/health.go`
- `cmd/rimsky/cli/instances.go`
- `cmd/rimsky/cli/lineage.go`
- `cmd/rimsky/cli/messages.go`
- `cmd/rimsky/cli/messages_test.go`
- `cmd/rimsky/cli/nodes.go`
- `cmd/rimsky/cli/parked.go`
- `cmd/rimsky/cli/run.go`
- `cmd/rimsky/cli/tags.go`
- `cmd/rimsky/cli/templates.go`
- `cmd/rimsky/cli/watch.go`
- `cmd/rimsky/cli/compose/apply.go`
- `cmd/rimsky/cli/compose/down.go`
- `cmd/rimsky/cli/compose/plan.go`
- `cmd/rimsky/cli/compose/template_run.go`
- `cmd/rimsky/cli/compose/wait.go`
- `cmd/rimsky/cli/compose/wait_test.go`
- `cmd/rimsky/cli/internal/clitest/server.go`
- `cmd/rimsky/cli/main_test.go` (new)
- `cmd/rimsky/cli/admin_test.go`
- `cmd/rimsky/cli/asset_test.go`
- `cmd/rimsky/cli/auth_dry_run_test.go`
- `cmd/rimsky/cli/instances_test.go`
- `cmd/rimsky/cli/lineage_test.go`
- `cmd/rimsky/cli/tags_test.go`
- `cmd/rimsky/cli/templates_test.go`
- `cmd/rimsky/cli/watch_test.go`
- `lib/services/test/scenarios/cli_shortcut_verbs_e2e_test.go`

**Checks run.** `go build ./...`, `go vet ./...`, `gofmt`, `make lint`, `go run ./tools/wallclock-lint`, the plumbline lint over every edited file, and `go test -timeout 0 -short -count=1` over `./cmd/...` and `./test/plumbline/...`.

### Stage 3 — HTTP and MCP control surface

**Corpus delta.** Landed `.ok-planner/design/decisions/mcp-base-methods-scope.md` verbatim from the sprint and added its one-line bullet to `.ok-planner/design/decisions.md` in alphabetical position. The dispatch site in `lib/control/controlapi/mcp/server.go` carries `// @decision: mcp-base-methods-scope`, and so does each of the four new MCP tests.

**`http-idorkey-accepted-uniformly`.** The frames (2), messages (2), assets (5), and debug-override (1) routes now spell their instance segment `{idOrKey}` and resolve it through `resolveInstance`, the helper the nodes handler already used. Each answers 404 on an unknown identifier, whether the caller spelled a UUID or a key. The parse-then-`Get`-inside-the-transaction pattern is gone from all ten handlers; the resolve happens once, before the handler's own work, and each handler carries the resolved `inst.ID` from there. `resolveInstance` gained a `// @concept: instance` annotation. The action registry's route table (`lib/control/controlapi/actions.go`) and the MCP tool input schemas (`lib/control/controlapi/mcp_route.go`) renamed the same argument, so `instance_debug_override`, `instance_frame_list`, `instance_frame_get`, `message_send`, `message_list`, and the five asset tools now take `idOrKey` and describe it as "instance id or instance_key" (D17).

**`http-list-routes-paginate`.** Five collections gained the contract: the observability `executors` and `claim-producers` collections, the assets list, the breakpoints list, and the claim holders. Each accepts `limit` and `cursor` and answers its collection plus `next_cursor`. `next_cursor` is present on every page — the `omitempty` came off `listFramesResponse`, `listMessagesResponse`, and `listEventsResponse`, and the map-shaped responses always carry the key — and the last page carries it as the empty string (D18). Every collection serializes an empty page as `[]`. The three control-api collections page through one helper, `pageByKey` in `lib/control/controlapi/app_util.go`, which sorts by a stable key and slices; the two observability collections use `pageServiceEntries` over the service name, since the observability router is a separate package with its own writer (D19). The tags cursor moved to the shared opaque encoding: `persistence.EncodeKeyCursor` / `DecodeKeyCursor` in `lib/foundation/persistence/pagination.go`, used by both drivers' `TemplateTags().List` and by every in-process pager, so a tag cursor is no longer the bare tag and a cursor the store did not mint answers `ErrInvalidCursor` → 400. `parseLimit` now returns an error instead of silently falling back, and all eight call sites answer 400; the observability router's `parsePagination` already did.

**`http-status-codes-conventional`.** Listing messages under an unknown instance answers 404 through the same resolve (above). Listing holders under an unknown claim handle answers 404 through a `ClaimHandles().Get` lookup and the new `shared.ErrClaimHandleNotFound` sentinel, which `writeError` maps to 404 beside the other not-found sentinels.

**`http-tag-create-idempotent`.** `POST /v1/tags` answers 200 with the existing mapping when the tag exists and names the same template hash, and 409 naming the mapping it holds when it names a different one. One helper, `writeTagCreateOutcome`, decides both the pre-check branch and the lost-insert-race branch, so the two paths cannot diverge.

**`mcp-standard-methods-present`.** The MCP server answers `ping` with an empty result. `ping` joins `initialize` as a method that needs no session header (D20). The `initialize` capabilities name `tools` (with `listChanged: false`) and `resources` (with `subscribe: false`) and nothing else, so the declared set is exactly the served set; every other base method falls to the existing default arm and receives method-not-found.

**Reviewer line settled in this stage.** L14 — the client cursor walks. The four loops (`pagedListAudit`, `PagedListInstanceNodes`, `PagedListInstances`, `pagedListTags`) were four copies of one walk; they now share `pageAll` in the new `cmd/rimsky/cli/paging.go`, which stops on an empty cursor, on a cursor the server repeats, and on a page with no rows. `PagedListAssets` is the fifth caller. The server-side reading is recorded as D18.

**Tests.** New `lib/control/controlapi/control_surface_uniformity_test.go` proves that every instance-scoped route answers the same by key as by id, that a POST of a message and a debug override both reach past the identifier by key, that every one of those routes answers 404 for an unknown UUID and an unknown key alike, that twelve collection routes answer 400 for a malformed, negative, or zero `limit`, that an empty page carries its collection as `[]` and a `next_cursor` field, and that the breakpoint list walks its cursor to the whole set and refuses a malformed one. `admin_routes_test.go` now proves an unknown claim handle answers 404, a handle with no holders answers `[]` with a cursor field, and a malformed holder cursor answers 400. `tags_test.go`'s duplicate-tag test became the idempotency scenario: same hash → 200, different hash → 409, and the tag still names the original template afterwards. `frames_test.go`'s two invalid-instance-id tests became unknown-instance-key tests. New `lib/control/observability/handler_service_pagination_test.go` proves both service collections page through their cursor and refuse a malformed limit. Four new tests in `lib/control/controlapi/mcp/server_test.go` prove `ping` answers an empty result with no session, that `initialize` names the served capabilities and no others, that nine unimplemented base methods answer method-not-found, and that the six served methods do not. The persistence conformance suite now proves the tag cursor is opaque and that a cursor the store did not mint answers `ErrInvalidCursor`. `lib/control/controlapi/app_util_test.go` was deleted: it asserted `parseLimit`'s silent fallback against the function directly, and the route-level 400 proof replaces it.

**Files changed.**

- `.ok-planner/design/decisions/mcp-base-methods-scope.md` (new)
- `.ok-planner/design/decisions.md`
- `lib/control/controlapi/mcp/server.go`
- `lib/control/controlapi/mcp/server_test.go`
- `lib/control/controlapi/app.go`
- `lib/control/controlapi/app_util.go`
- `lib/control/controlapi/app_util_test.go` (deleted)
- `lib/control/controlapi/actions.go`
- `lib/control/controlapi/mcp_route.go`
- `lib/control/controlapi/mcp_route_test.go`
- `lib/control/controlapi/frames.go`
- `lib/control/controlapi/frames_test.go`
- `lib/control/controlapi/messages.go`
- `lib/control/controlapi/messages_test.go`
- `lib/control/controlapi/assets.go`
- `lib/control/controlapi/debug_override.go`
- `lib/control/controlapi/nodes.go`
- `lib/control/controlapi/events.go`
- `lib/control/controlapi/audit_read.go`
- `lib/control/controlapi/instances.go`
- `lib/control/controlapi/templates.go`
- `lib/control/controlapi/tags.go`
- `lib/control/controlapi/tags_test.go`
- `lib/control/controlapi/claims.go`
- `lib/control/controlapi/breakpoints.go`
- `lib/control/controlapi/admin_routes_test.go`
- `lib/control/controlapi/control_surface_uniformity_test.go` (new)
- `lib/control/observability/handler.go`
- `lib/control/observability/handler_service_pagination_test.go` (new)
- `lib/foundation/persistence/pagination.go`
- `lib/foundation/persistence/postgres/template_tags.go`
- `lib/foundation/persistence/sqlite/template_tags.go`
- `lib/foundation/persistence/conformance/template_tags_conditional.go`
- `lib/foundation/shared/errors.go`
- `cmd/rimsky/cli/paging.go` (new)
- `cmd/rimsky/cli/client_assets.go`
- `cmd/rimsky/cli/asset.go`
- `cmd/rimsky/cli/asset_test.go`
- `cmd/rimsky/cli/client_instances.go`
- `cmd/rimsky/cli/instances.go`
- `cmd/rimsky/cli/audit.go`
- `cmd/rimsky/cli/tags.go`

**Checks run.** `go build ./...` and `go vet ./...` in all four modules, `make lint`, `go run ./tools/wallclock-lint`, the plumbline lint over the whole tree (exit 0), and `go test -timeout 0 -count=1` over `./lib/control/...`, `./lib/runtime/...`, `./lib/foundation/persistence/...`, `./lib/foundation/shared/...`, plus `-short` over `./cmd/...` and `./test/plumbline/...`. All pass.

### Reviewer lines L11–L15, fixed after stage 3

**L11 — the message delivery window.** Both drivers' `MessageListFilter` predicates became `delivered_at >= ?` / `delivered_at <= ?`, matching the `occurred_at >= / <=` the events and audit feeds already used, so a boundary-exact message no longer vanishes. On the pending question the sprint settles the axis — the item wires `--since`/`--until` to the route's delivered-after and delivered-before parameters — so a delivery window is a window over `delivered_at`, and a message with no delivery instant falls outside every such window; `--pending` remains the way a caller reaches one. `messages tail`'s flag help now says exactly that, so nothing is silent. The conformance suite's `MessagesListDeliveredAfterBefore` case was rewritten to exercise the filter at the boundary on both drivers: a message delivered exactly at the window's start is in it, one delivered exactly at its end is in it, a one-instant window holds exactly the message delivered at that instant, and an undelivered message shows on an unwindowed read, is absent from both windows, and is reachable through the pending filter.

**L12 — the three node reads that decide outcomes.** `rimsky run`'s outcome classification, `compose plan`'s `aggregateOutcome`, and `compose wait`'s per-node terminal reporting all read through `PagedListInstanceNodes` now. The helper's first parameter became the one-method `cli.InstanceNodeLister` interface so the compose wait loop, which holds an interface rather than a `*Client`, can use the same walk.

**L13 — the fake server pages.** `clitest.Server.handleListInstanceNodes` now models limit and cursor the way its instances endpoint does, with a `ListNodesDefaultPageSize` knob, an empty page as `[]`, and a 400 on a malformed limit — so it answers as the real route does. New `cmd/rimsky/cli/node_paging_test.go` proves `rimsky instance nodes` lists a five-node instance whole across a two-per-page server, and that a node failing on the last page still decides the run's outcome.

**L14 — the cursor walks.** Settled inside the stage; see the stage-3 entry and D18.

**L15 — `--action` with `--action-prefix`.** `rimsky audit` refuses the pair with a usage error naming both flags and saying the route honors one and drops the other; each flag's help names the other as incompatible. `time_window_test.go` proves the refusal exits non-zero and prints no rows.

Two more tests in `node_paging_test.go` prove the shared `pageAll` walk stops on a cursor the server repeats and on a page with no rows, so a server that always answers a cursor cannot make a verb ask forever.

**Files changed for L11–L15.**

- `cmd/rimsky/cli/messages.go`
- `cmd/rimsky/cli/audit.go`
- `cmd/rimsky/cli/time_window_test.go`
- `cmd/rimsky/cli/run.go`
- `cmd/rimsky/cli/client_instances.go`
- `cmd/rimsky/cli/compose/plan.go`
- `cmd/rimsky/cli/compose/wait.go`
- `cmd/rimsky/cli/internal/clitest/server.go`
- `cmd/rimsky/cli/node_paging_test.go` (new)
- `lib/foundation/persistence/postgres/messages.go`
- `lib/foundation/persistence/sqlite/messages.go`
- `lib/foundation/persistence/conformance/messages.go`

### Stage 4 — Runtime and configuration

**Corpus deltas.** Applied the `### Amend concept: api-key` body verbatim over `.ok-planner/design/concepts/api-key.md` (the Boundaries sentence now names expiry at the key's declared end). Landed `.ok-planner/design/decisions/dispatch-defaults-cover-every-node-timing-key.md` and `.ok-planner/design/decisions/http-poll-sensor-auth-outbound.md` verbatim from the sprint, each with its one-line bullet added to `.ok-planner/design/decisions.md` in alphabetical position.

**`key-expiry-emits-an-event`.** `lib/protocols/proto/v1/events.proto` gained `OPERATIONAL_KIND_AUTH_KEY_EXPIRED = 6` and `AuthKeyExpiredPayload` (`key_id`, `key_name`, `expires_at`); `make proto-gen` regenerated the Go types. `lib/foundation/events/kinds.go` maps the enum to the wire form `auth.key_expired` and exports `KindAuthKeyExpired()`; `lib/foundation/auth/audit.go` adds `EventKeyExpired`, `KeyExpiredPayload`, and `KeyExpiredProto`, so the payload is built from the generated message. The persisted marker is a new nullable `expiry_event_at` column on `rimsky_api_keys`, added by `050-api-key-expiry-event.sql` in both drivers, carried on `persistence.APIKey` and read back by both drivers' scans. A new store method, `APIKeyTable.SweepExpired`, stamps the column and returns the rows in one statement (`UPDATE … WHERE expires_at <= now AND expiry_event_at IS NULL RETURNING id, name, expires_at`), so a second sweep — and a second role's concurrent sweep — selects nothing. `runtime.SweepKeyExpiry` sits beside `SweepRotationGrace` in `lib/runtime/auth_sweep.go`: it appends one `auth.key_expired` event per swept key inside the sweep's own transaction, so a failed append rolls the marker back. The scheduler's one-minute auth sweep loop runs both sweeps each tick; a rotation-sweep failure no longer skips the expiry sweep. `auth.key_expired` joins the `/v1/audit` allowlist, so `rimsky audit --kind auth.key_expired` reaches it. The sweep does not revoke: the key's declared end already makes it inactive through `APIKey.ActiveAt`.

**`dispatch-defaults-cover-every-node-timing-key`.** `dispatch_defaults.max_retries` and `dispatch_defaults.retry_backoff` join the deployment configuration in `lib/control/config/claim_producers.go`, validated at load with the same rules the template validator applies per node (kind and jitter from the declared sets, non-negative delays, a cap not below the base, and a positive base delay whenever any backoff key is set). They thread the way the sync-RPC deadline default does: `config.DispatchDefaultsConfig` → `launch/supervisor.go` → `config.SupervisorConfig` → `runtime.Config` → `RunArgs.MaxRetriesDefault` / `RunArgs.RetryBackoffDefault`. `resolveRetryConfig` in `lib/runtime/runner_error_policy.go` — the one site the runner's error policy reads a node's retry settings from, for both the ordinary policy path and the infra-error path — now starts from the deployment default and lets the node override. `retry_backoff` replaces whole: the node's object is taken entire or the default's is, never a merge of the two. The site carries `// @decision: dispatch-defaults-cover-every-node-timing-key`.

**`sensor-auth-block-uniform`.** New shared package `lib/services/internal/sensorauth` holds the one `AuthConfig` type both sensors decode into, the three mode constants, the webhook's inbound validation (moved verbatim, fail-loud), the poll's outbound validation, and `ApplyOutbound`, which sets the configured header. It lives under `lib/services/internal/`, so both sensors reach it without either importing anything outside `lib/protocols` (depguard `consumption-side-isolation`). The webhook sensor dropped its private `AuthConfig`, its mode constants, and `validateAuthConfig`, and now calls `sensorauth.ValidateInbound`. The http-poll sensor's `Subscribe` decodes `auth` with the same shape, refuses at bind time anything it cannot apply — `hmac`, an unknown mode, an empty mode, and a `secret_header` block missing its header or secret — and mounts nothing when it refuses. `pollOne` applies the header on every poll; a subscription with no block, or with `none`, sends nothing. The kind's advertised config schema names `auth` with its two legal modes.

**Tests.** `lib/runtime/auth_sweep_test.go` gains `TestKeyExpiryIsAuditedOnceWhenTheKeysDeclaredEndPasses`: a key whose expiry has not passed is not reported, one whose expiry has passed is reported once with an `auth.key_expired` event carrying its id, name, and declared end, a second sweep reports nothing, and the row is not revoked. The persistence conformance suite gains `SweepExpired_ReportsEachKeyOnce`, proving the same on both drivers plus the untouched unexpired and never-expiring rows. `lib/runtime/runner_error_policy_dispatch_defaults_test.go` proves a node with no `max_retries` retries to the deployment-wide cap and then fails, a node's own cap overrides it, a node with no `retry_backoff` takes the default object whole (its kind included, so the second delay doubles), and a node's own flat `retry_backoff` replaces the default whole rather than inheriting its exponential kind. `lib/control/config/dispatch_defaults_test.go` proves both keys parse, default to unset, and that five retry-policy shapes the deployment cannot apply are refused at load. New `lib/services/sensors/sensor-http/poll_auth_test.go` proves the poll sends the configured secret header, sends no credential header with no block or with `none`, and refuses five auth blocks at bind time without mounting. `state_db_test.go`'s restart scenario now proves the auth block round-trips, so a restarted sensor still sends the credentials the operator configured.

**Files changed.**

- `.ok-planner/design/concepts/api-key.md`
- `.ok-planner/design/decisions/dispatch-defaults-cover-every-node-timing-key.md` (new)
- `.ok-planner/design/decisions/http-poll-sensor-auth-outbound.md` (new)
- `.ok-planner/design/decisions.md`
- `lib/protocols/proto/v1/events.proto`
- `lib/protocols/proto/v1/gen/events.pb.go` (generated)
- `lib/foundation/events/kinds.go`
- `lib/foundation/auth/audit.go`
- `lib/foundation/persistence/api_keys.go`
- `lib/foundation/persistence/postgres/api_keys.go`
- `lib/foundation/persistence/sqlite/api_keys.go`
- `lib/foundation/persistence/postgres/migrations/050-api-key-expiry-event.sql` (new)
- `lib/foundation/persistence/sqlite/migrations/050-api-key-expiry-event.sql` (new)
- `lib/foundation/persistence/conformance/api_keys.go`
- `lib/runtime/auth_sweep.go`
- `lib/runtime/auth_sweep_test.go`
- `lib/runtime/runner.go`
- `lib/runtime/supervisor.go`
- `lib/runtime/runner_error_policy.go`
- `lib/runtime/runner_error_policy_dispatch_defaults_test.go` (new)
- `lib/control/config/scheduler.go`
- `lib/control/config/claim_producers.go`
- `lib/control/config/supervisor.go`
- `lib/control/config/dispatch_defaults_test.go`
- `lib/control/launch/supervisor.go`
- `lib/control/controlapi/audit_read.go`
- `lib/control/controlapi/admin_diagnostics_test.go`
- `lib/services/internal/sensorauth/sensorauth.go` (new)
- `lib/services/sensors/sensor-webhook/sensor.go`
- `lib/services/sensors/sensor-webhook/sensor_test.go`
- `lib/services/sensors/sensor-webhook/state_db_test.go`
- `lib/services/sensors/sensor-webhook/reconcile_test.go`
- `lib/services/sensors/sensor-http/sensor.go`
- `lib/services/sensors/sensor-http/state_db.go`
- `lib/services/sensors/sensor-http/state_db_test.go`
- `lib/services/sensors/sensor-http/poll_auth_test.go` (new)

**Checks run.** `make proto-gen`, `go build ./...` and `go vet ./...` in all four modules, `make lint` (golangci-lint in all four modules plus license-check and logkind-lint), `go run ./tools/wallclock-lint`, the plumbline lint over every edited tree (exit 0), and `go test -count=1 -timeout 0` over `./lib/foundation/persistence/...` (both drivers, Docker up), `./lib/runtime/...`, `./lib/control/...`, `./lib/foundation/auth/...`, `./lib/foundation/events/...`, `./test/plumbline/...`, `./test/scenarios/auth/...`, and `./sensors/... ./internal/...` in the `lib/services` module. All pass.

### Reviewer lines L16-L21, fixed after stage 4

**L16 - the in-memory pagers discarded the source order.** `pageByKey` sorts by its key, so a key of the row's id alone re-sorted the breakpoint list out of the `created_at ASC` order both drivers return, and left the assets list in an order nothing defined. Both keys are now the row's own time prefixed to its id, through a new shared helper, `persistence.SortableTimeKey`, which formats a UTC instant fixed-width so it sorts lexicographically. RFC3339Nano does not: it trims trailing zeros, so `.1Z` sorts after `.12Z`. `TestBreakpointListWalksItsCursorToTheWholeSet` now reads the whole collection unpaginated, proves that reading is oldest-first, and requires the paged walk to yield the same sequence.

**L17 - `messages tail` could not reach an undelivered message.** The verb gained `--pending`, wired to the route's `pending` parameter, and refuses `--pending` with `--since` or `--until` with a usage error naming both, because a delivery window and "no delivery instant" select disjoint sets. The unwindowed read also walks its cursor now: a single-shot `messages tail` pages to exhaustion through the shared `pageAll`, and only `--follow` keeps the single newest page, which is what a tail wants. The reviewer's advisory fork is recorded as F2.

**L18 - the cursor encoding is URL-safe.** Every cursor rides a query parameter, and standard base64 emits `+` and `/`. `+` decodes to a space in a query string, so a cursor could come back corrupt. All three encodings in `lib/foundation/persistence/pagination.go` - the key cursor, the claim-handle cursor, and the event cursor - moved to `base64.RawURLEncoding` together (D31).

**L19 - the MCP schemas declare the pagination they accept.** `asset_list`, `breakpoint_list`, `claim_holders_list`, `asset_versions`, `asset_materialization_history`, the four lineage walks, and `waitset_list` now name `limit` and `cursor` in their input schemas, and the three tools that took the bare `{additionalProperties:true}` object - `auth_list`, `parked_node_list`, `held_frames_list` - take a `pagedObj` that names both. An agent reads the schema to learn a tool's arguments, so an accepted-but-undeclared parameter is invisible.

**L20 - the pagination contract now covers every `/v1` collection.** Sixteen more routes accept `limit` and `cursor` and answer their collection plus `next_cursor`: `GET /v1/auth/keys`; the five admin diagnostics (`held-frames`, `parked-nodes`, `wait-sets`, `producer-outbox`, `lifecycle-outbox`); the two asset sub-collections (`versions`, `materialization-history`); six lineage collections (run and claim ancestors and descendants, `by-source`, `by-producer`); and `breakpoint-hits`, which carried the second contract the item's title names. The two single-record lineage routes are `get` routes, not collections, and take none (D33). The `breakpoint-hits` change retires `next_since` and `truncated` and keeps `since` as an ordinary filter (D32); the MCP resource surface mirrors it, because a test requires the two payloads to be identical. `rimsky auth list` and the CLI's breakpoint-hit read follow the routes they call.

**L21 - a dry-run tag create on an existing tag says it was a rehearsal.** The idempotent 200 branch bypassed the dry-run envelope, so a dry-run caller got the same body an executed call returns and could not tell the two apart. `writeTagCreateOutcome` now takes the request and answers `would_have_left_tag_unchanged` under dry run. The conflicting-hash branch still answers 409 under dry run: `concept:dry-run` says a dry run runs the write's validation and previews what the write would have produced, and this write would be refused.

**Note - the dangling `@concept:` in migration 038.** Both drivers' `038-instance-target-routing-identity.sql` cited `host-agent-proxy`, a slug retired when the concept was renamed. Repointed to `host-daemon-proxy`. This changes an applied migration's digest (D30).

**Note - the cursor encoding.** Settled with L18 above.

**Files changed for L16-L21.**

- `cmd/rimsky/cli/auth_list.go`
- `cmd/rimsky/cli/client_instances.go`
- `cmd/rimsky/cli/instances.go`
- `cmd/rimsky/cli/messages.go`
- `cmd/rimsky/cli/messages_test.go`
- `lib/control/controlapi/admin_diagnostics.go`
- `lib/control/controlapi/admin_waitset.go`
- `lib/control/controlapi/app_util.go`
- `lib/control/controlapi/assets.go`
- `lib/control/controlapi/auth_handlers.go`
- `lib/control/controlapi/breakpoints.go`
- `lib/control/controlapi/breakpoints_test.go`
- `lib/control/controlapi/control_surface_uniformity_test.go`
- `lib/control/controlapi/lineage.go`
- `lib/control/controlapi/mcp_dry_run_parity_test.go`
- `lib/control/controlapi/mcp_resources.go`
- `lib/control/controlapi/mcp_resources_test.go`
- `lib/control/controlapi/mcp_route.go`
- `lib/control/controlapi/tags.go`
- `lib/foundation/persistence/pagination.go`
- `lib/foundation/persistence/postgres/migrations/038-instance-target-routing-identity.sql`
- `lib/foundation/persistence/sqlite/migrations/038-instance-target-routing-identity.sql`

**Checks run for L16-L21.** `go build ./...` and `go vet ./...` in all four modules, `make lint`, `go run ./tools/wallclock-lint`, the plumbline lint over `lib`, `cmd`, and `test` (exit 0), and `go test -count=1 -timeout 0` over `./lib/...` and `./cmd/...` (`-short` for the image-dependent suites) plus `./test/plumbline/...`. All pass.

### Stage 5 — Services, conformance, and compose

**Corpus delta.** Landed `.ok-planner/design/decisions/compose-undeployed-is-registered.md` verbatim from the sprint and added its one-line bullet to `.ok-planner/design/decisions.md` in alphabetical position.

**Decision catalog.** All five new decisions carry a bullet in the catalog's one-line form: `short-flags-single-letter`, `mcp-base-methods-scope`, `dispatch-defaults-cover-every-node-timing-key`, `http-poll-sensor-auth-outbound`, and `compose-undeployed-is-registered`. The catalog and the directory agree exactly: 270 files under `decisions/`, 270 bullets, no file without a bullet and no bullet without a file.

**`conformance-covers-every-protocol`.** The new kit `lib/protocols/conformance/hostdaemon/` sits beside the executor and claim-producer kits.

`session.go` is the daemon-side client. It opens the `HostDaemon.Connect` stream, sends `Register`, and reads the ack. A read loop then routes heartbeat acks, forward responses, spawns, and reaps to their own channels. Each check waits on the frame it asked for or on the stream's end, never on a duration.

`runner.go` holds the battery. It reports nine checks in the claim-producer kit's `check.Result` shape:

- The register ack names a proxy version and a routing identity.
- The server acks a heartbeat with the instant it received it.
- A second `Register` on an established stream changes nothing, and the connection survives.
- A `Reaped` naming a spawn the server never ordered changes nothing, and the connection survives.
- A `DispatchFrame` naming a stream the server never opened does the same.
- The server answers every `LocalHttpForward` under its own forward id, with a status the daemon can relay.
- The server refuses an empty `Register.api_key` with `InvalidArgument`.
- The server refuses a first frame that is not a `Register` the same way.
- No two live connections share one routing identity. A second registration under the same credentials takes a fresh identity, is refused `AlreadyExists`, or reports `displaced_prior`.

`rimsky conformance host-daemon` runs the battery (`cmd/rimsky/conformance.go`). It takes `--endpoint`, `--transport`, `--timeout`, `--tls`, `--daemon-label`, `--routing-label`, `--callback-base-url`, and the shared `--key`. `dispatchConformance` routes the verb. `printConformanceUsage`, the root usage, and the `rimsky-conformance` image description all name it.

**`compose-state-key-is-declarative`.** `templateStateSynonyms` in `cmd/rimsky/cli/compose/manifest.go` replaces `validTemplateState`. It accepts `registered`, `deployed`, and `undeployed`. `TemplateRef.EffectiveState` reads the same map and answers `registered` for `undeployed`, so one map entry carries both the validation and the synonym. The load error names all three states.

`cmd/rimsky/cli/compose/plan.go` no longer drops the undeploy step. A template the manifest declares `registered` or `undeployed`, and the deployment holds at `deployed`, now lands in its own `declaredUndeploy` set. The kept-hash filter does not read that set. The filter spares a hash a surviving tag still names; it must not spare a hash the manifest declares out of service. The step carries a note naming the tag that planned it. Both sites carry `// @decision: compose-undeployed-is-registered`.

**`stub-mode-on-every-bundled-executor`.** The `stub_response` override moved out of `http-node` into `lib/services/internal/stubprobe`, beside `Park` and `Cancel`. `SuccessDelta(attrs)` answers the default `{stub:true}` delta. It answers the requested object when the request carries one, and an error naming the requirement when that value is not an object. `HasResponseOverride(attrs)` answers whether the request carries an override. `Success(StubSuccess{…})` builds the settling outcome from the caller's change summary, error class, changed flag, and scratch.

`verifier-http` and `verifier-shape-checks` answer through `Success` in place of their hardcoded stub successes. Those were the only two callers of `execoutcome.StubSuccess`, so that function is gone. `claude-agent` drops its extra `stub_probe` precondition: under stub mode it honors `stub_response` whether or not `stub_probe` accompanies it, as the other three now do. One rule holds across all four bundled executors: in stub mode, a request carrying `stub_response` settles with that object as its attributes delta.

**Tests.** New `lib/services/internal/stubprobe/stubprobe_test.go` is the shared helper's contract suite. It proves the default delta, the override replacing that delta whole, the delta holding its own map rather than the request's, the refusal of a non-object override, and the outcome carrying the override, the requested tags, the scratch, and the caller's error class.

`verifier-http`, `verifier-shape-checks`, and `claude-agent` each gain a scenario. Each proves the executor answers with the requested `stub_response` and refuses one that is not an object. `verifier-shape-checks` also proves a stub-mode request carrying neither marker still runs its checks. `http-node`'s two existing `stub_response` scenarios already cover it.

Two new cases in `cmd/rimsky-host-daemon-proxy/conformance_test.go` run the host-daemon battery against the in-tree proxy, the way that file already exercises the executor and claim-producer kits. The first proves all nine checks reach the server and pass. The second proves the battery fails a server that implements nothing.

`cmd/rimsky/cli/compose/plan_test.go` proves a manifest declaring `registered` or `undeployed` plans exactly one undeploy for a template the deployment holds at `deployed`, and that declaring `deployed` plans nothing. `manifest_test.go` proves `undeployed` resolves to `registered`, an omitted state resolves to `deployed`, and an unknown state fails the load naming all three.

**Files changed.**

- `.ok-planner/design/decisions/compose-undeployed-is-registered.md` (new)
- `.ok-planner/design/decisions.md`
- `lib/protocols/conformance/hostdaemon/session.go` (new)
- `lib/protocols/conformance/hostdaemon/runner.go` (new)
- `cmd/rimsky/conformance.go`
- `cmd/rimsky/conformance_test.go`
- `cmd/rimsky/help_test.go`
- `cmd/rimsky/main.go`
- `cmd/rimsky-host-daemon-proxy/conformance_test.go`
- `dockerfiles/Dockerfile.conformance`
- `test/plumbline/cli_api_key_universal_test.go`
- `cmd/rimsky/cli/compose/manifest.go`
- `cmd/rimsky/cli/compose/manifest_test.go`
- `cmd/rimsky/cli/compose/plan.go`
- `cmd/rimsky/cli/compose/plan_test.go`
- `lib/services/internal/stubprobe/stubprobe.go`
- `lib/services/internal/stubprobe/stubprobe_test.go` (new)
- `lib/services/internal/execoutcome/execoutcome.go`
- `lib/services/internal/execoutcome/execoutcome_test.go`
- `lib/services/executors/http-node/server.go`
- `lib/services/executors/verifier-http/executor.go`
- `lib/services/executors/verifier-http/executor_test.go`
- `lib/services/executors/verifier-shape-checks/server.go`
- `lib/services/executors/verifier-shape-checks/server_test.go`
- `lib/services/executors/claude-agent/agentrun.go`
- `lib/services/executors/claude-agent/agentrun_test.go`

**Checks run.** `go build ./...` and `go vet ./...` in all four modules, `make lint` (golangci-lint in all four modules plus license-check and logkind-lint), `go run ./tools/wallclock-lint`, the plumbline lint over `lib`, `cmd`, `test`, and `dockerfiles` (exit 0), and `go test -count=1 -timeout 0` over `./cmd/...` and `./test/plumbline/...` (`-short`), `./conformance/...` in `lib/protocols`, and `./executors/... ./internal/... ./sensors/...` in `lib/services`. All pass. The image-dependent suites under `lib/services/test/` need a built image set and were left to the gate.

### Reviewer lines L22-L27, fixed after stage 5

**L22 - the async-callback path never saw the deployment retry defaults.** `CallbackServer` now carries `MaxRetriesDefault` and `RetryBackoffDefault`. `lib/runtime/supervisor.go` sets both at construction, from the same `cfg` fields the runner takes. `CallbackServer.runArgs` passes them into every `RunArgs` the async-callback terminal path builds. An async executor reporting `error` therefore resolves retries the way the same node failing synchronously does, and `applyTerminalInfraError` reaches the operator's cap rather than `defaultInfraRetryCap`. `TestAsyncCallbackTerminalTakesTheDeploymentWideRetryDefaults` drives the real error policy with the args `runArgs` builds, so it proves the wiring rather than the struct's fields.

**L23 - the poll sensor kept a copy of the upstream credential.** `decision:secret-at-rest-posture` says a bundled sensor persists no copy of the secret in its own state. It keeps only its watermark and takes the full config back through subscription resync. This sprint carries no delta amending that decision.

Dropping the `auth` column alone would not have restored that shape. The poll sensor rebuilt whole watches from its own state at attach, so it reported every subscription live. `decision:subscription-reconciler`'s resync re-issues only the rows a publisher does not report, so it would never re-issue one, and the sensor would poll without credentials forever.

The state table now holds `publisher_subscription_id`, `started_at`, `last_hash`, and `last_poll_at`. `AttachStateDB` seeds a watermark cache rather than watches. `Subscribe` merges the watermark into the watch resync delivers. `ListWatermarks` and `GetWatermark` replace `ListAll` and `GetSubscription`. The bootstrap drops the `auth` column and the config columns beside it, so an existing sensor database loses the stored secret on its next start. D28 records the reversal.

**L24 - a public description named retired fields.** The `breakpoint:read` entry in `lib/control/controlapi/actions.go` now says the hits route returns every hit after the caller's `?since=` watermark, pages with `?limit=` and `?cursor=`, and answers with `next_cursor`.

**L25 - the CLI fake did not model the breakpoint-hits route.** `clitest.Server` now parses `limit` and `cursor` the way `handleListBreakpointHits` does. It mints its cursor through the same `persistence.EncodeKeyCursor` and answers `next_cursor`. It answers 400 for a malformed limit, and for a cursor it did not mint. Two tests in `cmd/rimsky/cli/node_paging_test.go` prove the walk: the client reads a five-hit instance whole across a two-per-page server and repeats no row, and the server refuses a cursor it did not mint rather than reading it as the first page.

**L26 - MCP tool schemas hid their pagination.** `instance_frame_list` and `message_list` gained `limit` and `cursor`. A new fitness check found five more tools in the same state: `event_list`, `instance_list`, `template_list`, `tag_list`, and `node_list` each took a bare `{additionalProperties:true}` object while its route accepts filters and pagination. Each now names the parameters its route reads.

`TestEveryPaginatingRouteDeclaresItsPaginationOnItsMCPTools` in `lib/control/controlapi/control_surface_uniformity_test.go` is the check. It walks every GET route in the action registry with substituted path parameters. It asks the server which routes answer `next_cursor`. It then requires each such action to carry a tool declaring `limit` and `cursor`. The server's own answer decides which routes the check covers, so a route that starts paginating later cannot leave its tool silent.

**L27 - the migration digest.** Recorded as F3 below; the repoint stands.

**Files changed for L22-L27.**

- `lib/runtime/callback.go`
- `lib/runtime/supervisor.go`
- `lib/runtime/runner_error_policy_dispatch_defaults_test.go`
- `lib/services/sensors/sensor-http/state_db.go`
- `lib/services/sensors/sensor-http/state_db_test.go`
- `lib/services/sensors/sensor-http/sensor.go`
- `lib/control/controlapi/actions.go`
- `lib/control/controlapi/mcp_route.go`
- `lib/control/controlapi/control_surface_uniformity_test.go`
- `cmd/rimsky/cli/internal/clitest/server.go`
- `cmd/rimsky/cli/node_paging_test.go`

**Checks run for L22-L27.** `go build ./...` and `go vet ./...` in all four modules, `make lint`, `go run ./tools/wallclock-lint`, the plumbline lint over `lib`, `cmd`, `test`, and `dockerfiles` (exit 0), and `go test -count=1 -timeout 0` over `./lib/control/...`, `./lib/runtime/...`, `./cmd/...` and `./test/plumbline/...` (`-short`), and `./executors/... ./internal/... ./sensors/...` in `lib/services`. All pass.

### Reviewer lines L28-L29, fixed after stage 5

**L28 - dead code in the new conformance kit.** `lib/protocols/conformance/hostdaemon/session.go` no longer defines the `spawns` and `reaps` channels, `deliverSpawn`, `deliverReap`, or their two `readLoop` arms. No check awaited either frame. No check can: the proxy orders a `Spawn` when a supervisor dispatches to a late-bound service, and a battery acting as a daemon drives no dispatch. `readLoop` reads every frame off the wire whether or not its switch matches a case, so the deletion changes nothing the server observes. The battery still runs all nine checks against the in-tree proxy.

**L29 - the api-key exemption stated a false reason.** All seven conformance exemptions are gone, not reworded. `test/plumbline/cli_api_key_universal_test.go` classified a verb as control-api-dialing on the bare selector `NewClient`, and `grpc.NewClient` matches that selector too. The new `reachCalls` walk carries each call's qualified name. `controlAPIClientConstructors` names `cli.NewClient` and `cli.NewClientWithKey`. A verb that dials a service over that service's own protocol therefore classifies as local and needs no exemption. `runConformancePublisher` satisfies the rule without one, because it does reach the control-api client.

A new test restores the protection the exemption dropped. `TestEveryVerbPresentingAnAPIKeyAcceptsOneOnItsOwnFlagSet` requires every verb that resolves an api-key and owns a flag set to register one. The rule covers a verb presenting its key to the control API and a verb presenting it to a service that verifies the key there. The test checks 54 verbs, `runConformanceHostDaemon` among them, and carries no exception list.

**Files changed for L28-L29.**

- `lib/protocols/conformance/hostdaemon/session.go`
- `test/plumbline/cli_api_key_universal_test.go`

**Checks run for L28-L29.** `go build ./...` and `go vet ./...` in the root and `lib/protocols`, `make lint`, `go run ./tools/wallclock-lint`, the plumbline lint over `lib`, `cmd`, `test`, and `dockerfiles` (exit 0), and `go test -count=1 -timeout 0` over `./test/plumbline/...`, `./cmd/rimsky-host-daemon-proxy/...`, `./cmd/rimsky/` (`-short`), and `./conformance/...` in `lib/protocols`. All pass.

# Certification — Align twenty silent traps to what a user expects

Status: certified with issues promoted

### Outcomes delivered

Every one of the sprint's twenty work items is realized in code with a test, and every corpus delta is applied verbatim. What a user can now observe:

- `cli-destructive-verbs-confirm` (with `admin-reset-is-scoped`): `tag rm`, `instance delete`, `instance kill`, `template undeploy`, `template rm`, `auth revoke`, `admin reset`, `lineage prune`, and `asset delete` print their target, ask `Proceed? [y/N]` on a terminal, refuse with exit 2 off a terminal without `--yes`, and send no request when refused. `admin reset` reads the `--yes` it always parsed.
- `cli-duration-flags-share-syntax`: every duration flag parses with `time.ParseDuration`; `auth create --expires 30d` is a usage error; `conformance --retention-test` is a duration.
- `cli-help-on-every-subcommand`: `-h` and `--help` on all 88 nodes of the verb tree print that node's usage on stdout and exit 0.
- `cli-json-flag-universal`: `-o json` is the one JSON spelling; `auth list` dropped `--json`; `auth show`, `auth status`, `ctx current`, and `daemon status` gained `-o`.
- `cli-output-flag-is-json-superset`: `-o yaml` carries what `-o json` carries; `-o table` names a table or fails before the verb runs; any other value fails.
- `cli-short-flags-single-dash` (`decision:short-flags-single-letter`): `-y` for `--yes`, `-f` for `--follow` on `instance events` and `messages tail`; `compose` keeps `-f` for the manifest; `--force` is long-only.
- `cli-time-window-flags-uniform`: `messages tail --since/--until` (inclusive, on `delivered_at`; `--pending` reaches undelivered messages), a new `rimsky audit` verb over `GET /v1/audit` with the same flags plus the route's other filters, `watch --until-state`, `lineage prune --until`.
- `key-expiry-emits-an-event` (amended `concept:api-key`): a sweep beside the rotation-grace sweep appends one `auth.key_expired` event per key whose declared end passes, once, through a persisted `expiry_event_at` marker added by migration 050 on both drivers; revoked keys and expiries that predate the upgrade emit nothing; the kind is readable through `/v1/audit`.
- `sensor-auth-block-uniform` (`decision:http-poll-sensor-auth-outbound`): the http-poll sensor takes the webhook sensor's `auth` block through one shared `sensorauth.AuthConfig`, sends `secret_header` on every poll, accepts `none`, refuses `hmac` and unknown modes at bind, and keeps no copy of the secret at rest.
- `http-idorkey-accepted-uniformly`: every instance-scoped route resolves an id or a key through `resolveInstance`, spells the segment `{idOrKey}`, and answers 404 on an unknown identifier; the MCP tool arguments use the same name.
- `http-list-routes-paginate`: every collection under `/v1` accepts `limit` and `cursor` and answers `{items, next_cursor}` with `next_cursor` present and empty on the last page; a malformed `limit` answers 400 and an oversized one clamps to 500; every cursor is URL-safe base64 from one shared encoder; every MCP tool over a paginating route declares `limit` and `cursor`.
- `http-status-codes-conventional`: listing messages under an unknown instance and holders under an unknown claim handle answer 404.
- `http-tag-create-idempotent`: `POST /v1/tags` answers 200 with the existing mapping on the same hash and 409 on a different one; a dry run says so.
- `mcp-standard-methods-present` (`decision:mcp-base-methods-scope`): the MCP server answers `ping` without a session, `initialize` names exactly the served set, and every unimplemented base method receives method-not-found.
- `dispatch-defaults-cover-every-node-timing-key` (decision of the same slug): `dispatch_defaults.max_retries` and `dispatch_defaults.retry_backoff` apply to every node, on the synchronous and async-callback paths alike, with `retry_backoff` replacing the default object whole.
- `conformance-covers-every-protocol`: a host-daemon conformance kit under `lib/protocols/conformance/hostdaemon/` runs as `rimsky conformance host-daemon`, so every protocol the tree defines has a battery.
- `compose-state-key-is-declarative` (`decision:compose-undeployed-is-registered`): a manifest accepts `undeployed` as `registered`, a deployed template declared `registered` plans an undeploy, two tags declaring different states on one hash are refused, and an instance on a template declared out of service is refused at load.
- `node-tags-are-selectors`: `rimsky instance nodes` shows a `TAGS` column, filters by `--tag` server-side and `--tag-prefix` client-side, and pages the listing whole.
- `stub-mode-on-every-bundled-executor`: `stub_response` and `stub_tags` live in the shared `stubprobe` package, all four bundled executors honor them under stub mode, their schemas declare both keys, and a malformed value errors.
- Decision catalog: five new bullets, in order; 270 files and 270 bullets agree.

### Divergences

Every call the executor recorded that the architect did not rewrite, every call the fixer made, the one corpus repair, and the architect's rulings, each under its identifier for after-the-fact veto. This section replaces the executor's `## Divergences`; every entry is carried whole.

Architect rulings, KICKBACK OVERTURNED (the built reading stands, tree unchanged):

F1 — `auth show`'s default output format. **Architect ruling: determined, reading (a) stands.** `auth show` defaults to the human key/value block. `-o json` prints the JSON the verb used to print with no flag. Every other `get` and `show` verb defaults to human output. The sprint's item makes `-o json` the one JSON spelling, and a read verb whose default format differs from every other read verb is the second idiom that item removes. Reading (b) would also make `-o human` a flag no other verb needs. No corpus artifact settles a CLI default format, so the item governs. The project is pre-v1, so a caller that parsed the old output adds one flag. The tree stands as the builder left it.

F2 — the axis `messages tail --since`/`--until` narrows to. **Architect ruling: determined, reading (a) stands.** The two flags window `delivered_at`. `--pending` reaches an undelivered message. The sprint's item names the route's `delivered_after` and `delivered_before` parameters outright, and the route accepts no received-at window. Reading (b) would add a pair of route parameters no work item asks for. `rimsky audit` windows `occurred_at` because that is the axis its own route carries; the two verbs share the flag names, not one column. Both flags' help states that an undelivered message falls outside every delivery window. The tree stands as the builder left it.

F4 — which side of the host-daemon protocol the conformance battery certifies. **Architect ruling: determined, reading (a) stands.** The battery acts as a daemon and certifies the proxy it dials at `--endpoint`. `concept:conformance` has the author point a battery at a running service and drive the protocol's operations against it. `concept:host-daemon-proxy` is that service, and `concept:host-daemon` is user session tooling. Reading (b) would turn `--endpoint` into an address the kit binds, and it would make this the only battery that waits to be called. That is the second idiom this sprint removes. The tree stands as the builder left it.

Corpus repair under `.ok-planner/design/`:

D43 — the decision catalog's new `http-poll-sensor-auth-outbound` bullet moved one line down, to its alphabetical place after `http-bridge-preserved`. Corpus edit in `.ok-planner/design/decisions.md`. The catalog sorts by slug and the other four new bullets already sat in order.

Executor calls, stages 1 to 5 and the standing reviewer's fix rounds:

D2 — `-o text` is retired. `ParseFormat` used to accept `text` and `table` as silent aliases for human output. The item says `-o` names a format or fails and enumerates human, json, yaml, and table. `text` is not one of those names, so it is now a usage error.

D3 — `--help` reaches the conformance and daemon subcommands. The item's parenthetical names `auth revoke`, `auth create`, `compose down`, and `compose run` as the verbs with their own flag sets, but the rule is "any node of the verb tree". The seven `conformance` subcommands and the three `daemon` subcommands also own flag sets and also exited 2 with usage on stderr, so they were fixed too. `TestConformanceSubcommandsRegisterDocumentedFlags` and its sibling now read stdout instead of stderr.

D4 — `runWithCommon`'s stop signal changed. Its callers used to stop on `code != 0`, which cannot express "stop with exit 0", the shape `--help` needs. The four-value return now yields a nil `*CommonFlags` whenever the verb must stop, and every call site reads `if common == nil { return code }`. The same change applies to `parseComposeFlags`, `parseComposeDownFlags`, `parseComposeRunFlags`, and `ParseRunArgs`, whose callers now test the returned flags pointer.

D5 — the dev-loop shortcuts name themselves. `rimsky register`, `deploy`, `undeploy`, `instantiate`, `rm-instance`, `ls`, and `logs` forward to a literal-API verb, so `--help` on them would have printed the target verb's usage. Each forwarding verb now calls a named inner variant (`runTemplateRegisterNamed`, `runInstanceListNamed`, and so on) that carries the node's own name into its flag set, so `rimsky logs --help` says `usage: rimsky logs …`. `rimsky ls --help` prints its own line naming the three sub-listings.

D6 — `-o table` refusals reach the compose verbs. `compose up`, `compose down`, `compose plan`, and `rimsky run` render no table, so they refuse `-o table` the way `instance get` does. `compose status` does render a table and accepts it. `EmitPlan` lost its `format` parameter: `RunComposePlan` now routes through `Render`, which owns every structured branch. **Corrected in stage 2 (reviewer line L2):** as stage 1 left it, the refusal held on `rimsky run` only when the verb self-hosted — the remote path still branched on `common.Format == FormatJSON` and accepted `-o table` in silence. Stage 2 routes the remote path through `Render` too, and moves every `-o table` refusal to flag resolution (D14), so the claim now holds on both paths.

D7 — `parked list`'s human output is a real table. It printed hand-rolled lowercase tab-separated lines. For `-o table` to name a table rendering on that verb, it now goes through `EmitTable` with upper-case column headers (`INSTANCE`, `NODE_ID`, `PARKED_AT`, `RESUME_AT`), matching every other listing.

D8 — the output-format fitness check changed its question. `test/plumbline/cli_output_format_test.go` asserted that a verb declaring the flag mentions `FormatJSON`. With one shared renderer no verb mentions it any more, so the check now asserts the verb routes its payload through `Render` or `EmitStructured`, and counts `RegisterOutputFlags` as declaring the flag.

D9 — `docs/` is left behind the tree. `docs/concepts.md` describes three of the traps this stage closes (`--json` on one verb only, `--help` exiting 2 on a leaf, `-o yaml` rejected) and names `--retention-test-seconds`. Those files are the release documentation corpus, revised by `/document` and expected to go stale between releases, so this stage did not touch them.

D10 — `--yes` stopped answering `--force` on `instance kill`. Before this stage the verb refused only when neither `--force` nor `--yes` was given, so `--yes` alone terminated an instance. The sprint keeps `--force` with its current meaning and gives `--yes` the prompt alone, so the verb now demands `--force` on its own and then asks the destructive-verb question, which `--yes` answers. A script that terminated with `--yes` alone now needs `--force --yes`. The reviewer's stage-1 line L10 reached the same reading.

D11 — every node read pages the listing to completion. The route paginates and the client read one page, so a hundred-node instance listed 100 nodes and said nothing about the rest. Adding `--tag` meant touching the same call, and a selector over a silently truncated list is worse than no selector. `PagedListInstanceNodes` walks the cursor the way `PagedListInstances` and `pagedListTags` already do. `instance nodes` and `instance status` moved onto it first. Reviewer line L12 then moved the three reads that decide an outcome — `rimsky run`'s outcome classification, `compose plan`'s aggregate outcome, and `compose wait`'s per-node terminal reporting — onto the same walk, because a verdict read from a truncated list is the same trap in a worse place. All five call sites page the whole listing.

D12 — `rimsky audit` carries the route's whole filter set. The work item says the verb reads `GET /v1/audit` "with the same flags", meaning `--since`/`--until`. `story:audit-log-read` promises the operator reads the feed "with filtering", and the route already accepts `kind`, `key_id`, `key_name`, `action`, `action_prefix`, `target`, `status`, and `mode`. A verb that reached the route and dropped seven of its nine filters would be a second silent trap, so each one is a flag.

D13 — `asset delete` names the instance the operator typed, not the resolved UUID. The confirmation had to sit before the verb's first HTTP call, and the instance resolve is that call. The prompt therefore reads `delete asset <alias> on instance <id-or-key>` in the operator's own words.

D14 — flag resolution refuses `-o table`, and `Render` no longer does. Stage 1 put the refusal inside `Render`, which runs after the verb has done its work: `compose up -o table` applied the whole plan and then exited 2, the streaming reads never reached `Render` at all and accepted `-o table` in silence, and `rimsky run` discarded the refusal's exit code. Table support is now a parameter each verb states where it declares its flags — `runWithCommon(name, argSpec, HasTable|NoTable, args, …)`, `parseComposeFlags(name, tables, args)`, and the `ResolveFormat(verb, tables)` call the standalone verbs make — and the refusal fires before the verb runs. `Render(format, value, human)` renders and decides nothing. This closes reviewer lines L1, L3, and L4 together.

D15 — `-o yaml` documents open with `---`. Each structured record went through its own encoder, so a stream of records ran together into one mapping with repeated keys and did not parse back. Every YAML document now opens with the separator, single-shot output included, which is what makes a stream self-delimiting.

D16 — every caller of a destructive verb now states its intent, tests included. A scripted caller that omits `--yes` gets exit 2, so each existing test that drives one gained the flag: the CLI unit tests, and the `undeploy` and `instance kill` invocations in the shortcut-verb end-to-end suite. The CLI test binary also runs with a non-terminal stdin, installed once in `TestMain`, so a future test that forgets `--yes` fails on the refusal instead of blocking on a prompt no one answers.

D17 — the `idOrKey` rename reaches the MCP tool arguments. The item names the HTTP path segment. The MCP tool schemas name each path parameter by the same word the route spells, and `substitutePathParams` looks the argument up by that name, so leaving them at `id` would have made every renamed tool fail with "missing path param". Ten tool schemas took the new name and its description; three MCP tests and one schema fitness check now name it too.

D18 — `next_cursor` is present-and-empty on the last page, never null and never absent. The item says the field is present on every page but does not say what it carries when there is no next page. A JSON null and an absent key both make a client test `page.next_cursor == ""` fail in the same way an omitted field does, and Go's zero value for the field is the empty string, so the empty string is the one spelling that costs no client branch. Every server response now emits the key unconditionally, and the CLI's shared `pageAll` walk stops on it. This is the reading reviewer line L14 asked to record.

D19 — the five in-process collections page in memory, not in SQL. The assets list, the breakpoints list, the claim holders, and the two observability service collections all read a bounded set the handler already holds — a filtered claim-handle list, an instance's breakpoints, one handle's holders, the deployment's declared services — and none of the underlying reads takes a cursor. Pushing pagination into the store would mean three new driver methods on both drivers for sets that are bounded by an instance, and the two service collections never reach a store at all. Each collection instead sorts by a stable key and slices, through one shared helper. The cursor is the shared opaque encoding, so the wire contract is the same one the store-backed collections offer and a later move into SQL changes no client.

D20 — `ping` needs no MCP session. The server required the `Mcp-Session-Id` header on every method but `initialize`. `decision:mcp-base-methods-scope` says clients send `ping` as a liveness check and that a method-not-found on it reads as a broken server; a 400 demanding a session reads the same way, and `ping` returns nothing a session could scope. `initialize` and `ping` are now the two methods a caller may send without one.

D21 — an undelivered message is outside every delivery window. Reviewer line L11 asked the delivery window to be settled in the same edit as the inclusivity fix. The sprint's item wires `messages tail --since`/`--until` to the route's `delivered_after` and `delivered_before` parameters, so the axis is `delivered_at`, not `received_at`. A message that has not been delivered has no instant on that axis, so it belongs to no window, and admitting SQL NULL into the predicate would put a message with no delivery time inside a window named by delivery times. The `--pending` filter is the way to reach one, and both flags' help now says an undelivered message is outside every delivery window, so the exclusion is stated rather than discovered.

D22 — the expiry sweep fires the auth-mutation hooks. The rotation-grace sweep fires them because a swept key becomes inactive and the control API caches whether any active key exists. A key whose declared end passes leaves the active set the same way, and the last key expiring is what puts a deployment into anonymous mode, so the expiry sweep fires them too. Without it the cached answer would outlive the fact by up to the cache's own life.

D23 — the two auth sweeps run independently in the scheduler's loop. The loop used to `continue` on a sweep error. With two sweeps in one tick that would make a rotation-sweep failure skip the expiry sweep silently, so each sweep now logs its own failure under its own kind (`AUTH.SWEEP.FAILED` and `AUTH.EXPIRYSWEEP.FAILED`) and the expiry sweep runs regardless.

D24 — `auth.key_expired` joins the `/v1/audit` allowlist. The work item says the sweep appends the event; it does not name the read surface. `/v1/audit` filters to an explicit list of `auth.*` kinds, so an event the list omits is written and unreachable through the verb the operator reads the audit feed with. The item's own title calls key expiry an audit event, so the kind is in the list.

D25 — the marker column is `expiry_event_at`, and the sweep does not revoke. The sprint asks for "a persisted marker on the key row" without naming it. The column records the instant the sweep appended the event, which is exactly what makes the event fire once. The sweep deliberately leaves `revoked_at` alone: `APIKey.ActiveAt` already treats a passed `expires_at` as inactive, and stamping `revoked_at` would make an expired key indistinguishable from a revoked one in the ledger and in `auth list`.

D26 — the deployment-wide `retry_backoff` is validated at load with the node validator's rules. The sprint says the key joins the deployment configuration and does not say what an invalid value does. A backoff the runtime cannot compute is worse deployment-wide than per node — it reaches every node — so the loader applies the same five checks `validateRetryBackoff` applies to a node and refuses the deployment, matching how `dispatch_defaults`' three deadlines already refuse a negative or sub-second value.

D27 — the retry defaults thread through the supervisor alone. `sync_rpc_deadline` threads to the supervisor; `max_quiet_period` and `max_runtime` thread to both the supervisor and the scheduler, because the scheduler's conductor enforces those deadlines. The runner's error policy — the only reader of `max_retries` and `retry_backoff` — runs under the supervisor, so the retry defaults follow the sync-RPC deadline's path and stop there.

D28 — the http-poll sensor keeps no copy of the operator's credential and takes it back from resync. **Rewritten after reviewer line L23.** Stage 4 persisted the subscription's `auth` block, secret included, in a new `auth` column of `sensor_http_state`, and cited `decision:secret-at-rest-posture` as authorization. That decision says the opposite. A bundled sensor persists no copy of the secret in its own state. It keeps only its watermark and receives the full config, secret included, through subscription resync, so rimsky's resolved-config blob is the only at-rest copy.

The sensor now follows that decision. Its state table holds `publisher_subscription_id`, `started_at`, `last_hash`, and `last_poll_at`. The bootstrap drops the `auth` column and the config columns beside it, so an existing sensor database loses the stored secret on its next start. `AttachStateDB` restores watermarks rather than watches, which is what lets the resync work: a sensor that rebuilt watches reported every subscription live, and the reconciler re-issues only the rows a publisher does not report. A restarted sensor now polls nothing until resync arrives. The bundled webhook sensor has always had that same gap, and the decision names it as the price of keeping no secret at rest.

D29 — `docs/` still says retry policy has no deployment-wide form. `docs/capabilities.md`, `docs/operating.md`, `docs/templates.md`, `docs/config.md`, `docs/concepts.md`, and two cookbook pages state that `dispatch_defaults` carries exactly three keys and that `max_retries` under it stops the deployment. That is the release documentation corpus, revised by `/document` and expected to go stale between releases, so this stage did not touch it — the same reading D9 recorded in stage 1.

D30 - repointing the citation in migration 038 changes an applied migration's digest. `decision:migrations-append-only-numbered` has the runner record each applied file's digest and refuse the next boot when the contents change, and the digest covers the whole file, comments included. Every path to a live slug - repoint, or delete the line - changes it. Under the project's pre-v1 rule the repoint is the right trade, because the alternative is a stale slug in a file nothing may ever touch again, but it must be stated: **a database that already applied migration 038 refuses its next boot**, naming the file. The recovery costs no data - `UPDATE rimsky_migrations SET digest = NULL WHERE filename = '038-instance-target-routing-identity.sql';` - because the runner backfills a null digest on the next start. The plumbline lint does not read `--` comments, so nothing caught this and nothing will catch the next one.

D31 - all three cursor encodings moved to URL-safe base64 together, not just the key cursor. The reviewer named `EncodeKeyCursor`. The claim-handle cursor and the event cursor encode a JSON object holding a timestamp and an id, ride the same query parameters, and hit the same `+`-decodes-to-space corruption. Fixing one and leaving two is the second idiom the sprint exists to remove. Cursors are ephemeral tokens the server mints and the client echoes, so nothing persisted changes meaning.

D32 - `breakpoint-hits` retires `since`, `next_since`, and `truncated`, and pages with `cursor` alone. The route carried two spellings of one watermark: `since` named a seq, and `cursor` was the opaque encoding of the same seq. A caller passing both had one of them silently dropped, and the response pair that a `since` client read to tell a truncated page from a final one - `next_since` and `truncated` - was gone. So the whole `since` shape goes, on the HTTP route and on the MCP resource alike, under `decision:pre-v1-pure-removal-for-retired-surfaces`: the parameter is removed rather than refused, because that decision rejects a parser case that names a retired shape, and an unread query parameter is what every other rimsky route does with one. The route answers `limit`/`cursor` and `next_cursor` like every other collection, the cursor encodes the seq through the shared opaque encoding, and a caller that polls for new hits keeps the `next_cursor` of its last page. The resource description and the `breakpoint:read` action description name `?limit=` and `?cursor=` and nothing else. `TestBreakpoint_ListHits_HTTPMirrorsMCPResource` still requires the two bodies to be equal.

D33 - `GET /v1/lineage/runs/{run_id}` and `GET /v1/lineage/claims/{claim_handle_id}` take no pagination. Both return a single lineage record - the newest row for that id - not a collection, and answer 404 when there is none. The item's contract is for collection routes; a `get` route has nothing to page.

D34 - the producer-outbox route no longer clips its entries to a fixed page size, and still holds only one page of them. It read every pending row, reported the true `depth`, and then silently kept only the first `DefaultServiceOutboxPageSize` entries, which is the truncation-without-a-cursor shape the item exists to remove. `limit` now defaults to that same size, so the default response is the size it always was, and `next_cursor` reaches the rest. The retained set is bounded to the page the caller asked for: the scan reads every row to report `depth` and the oldest instant, skips the rows the cursor has passed, and keeps at most `limit` plus one entry. The page key is the outbox sequence, which is the order `ListAll` reads in, so the retained prefix is the page.

D35 - every in-memory page key that orders by time carries a fixed-width time prefix. `pageByKey` compares keys as strings, so a key built from `time.RFC3339Nano` would order `.12Z` before `.1Z`. `persistence.SortableTimeKey` formats to a fixed nine-digit fraction, which is the same layout the SQLite driver already persists times in, so the string order and the instant order agree.

D36 — the shared stub success echoes `stub_tags` for every bundled executor, not only http-node. Moving the `stub_response` override into `stubprobe` raised the question of where the rest of the stub-success shape lives. http-node echoed the request's `stub_tags`; the verifiers did not. One builder serving both would have needed two shapes. `stubprobe.Success` therefore echoes the tags for every caller. The tags probe asks only for a tag the executor declares, so an executor that declares none is never asked, and answering when asked is the contract `stubmode` already states. The changed flag, the scratch, the change summary, and the error class stay per-caller, because those differ for real reasons.

D37 — a stub-mode request carrying `stub_response` settles with it whether or not `stub_probe` accompanies it. The item says the claude-agent executor drops its extra `stub_probe` precondition. It does not say what the other three do with an override presented alone. http-node and verifier-http already answered any stub-mode request with a stub success, so honoring the override there needed no new branch. verifier-shape-checks did not: under stub mode with no probe marker it ran its real checks, which are pure computation. It now answers the override when the request carries one and runs its checks otherwise. One rule therefore holds across all four executors: in stub mode, `stub_response` decides the answer. A shape-checks node under stub mode carrying neither marker still verifies its rows.

D38 — the host-daemon conformance kit certifies the server side of the protocol. `HostDaemon.Connect` is served by the host-daemon proxy and dialed by the daemon, so a battery pointed at an `--endpoint` acts as a daemon and certifies a proxy. That is the shape every other kit has — `concept:conformance` says the author points a battery at a running service, and the proxy is the service (`concept:host-daemon-proxy`) while the daemon is user session tooling. F4 records the alternative reading.

D39 — the api-key universality check classifies by the control-api constructor's qualified name. **Rewritten after reviewer line L29.** Stage 5 gave `runConformanceHostDaemon` the same exemption the sibling conformance verbs carry, and copied their reason. That reason was false: the verb registers the api-key flag, resolves the key, and presents it as `Register.api_key` to the proxy, which verifies it against the control API. The exemption's only effect was to skip the flag assertion for the one conformance verb that needs the flag. The classifier now matches `cli.NewClient` and `cli.NewClientWithKey` by their qualified names, rather than the bare selector `NewClient` that `grpc.NewClient` matches too. Every verb dialing a service over that service's own protocol therefore classifies as local. All seven conformance exemptions are gone. A new test, `TestEveryVerbPresentingAnAPIKeyAcceptsOneOnItsOwnFlagSet`, restores the protection for every verb that resolves a key, this one included.

D40 — the compose undeploy step for a declared-registered template names the tag that planned it. Every other undeploy step carries only a template hash, because the hash is all the apply path needs. This one carries the tag and a note, because an operator reading `- undeploy sha256-abc…` has no way to tell a superseded hash from a template they deliberately declared out of service. The apply path still keys on the hash alone.

D41 — `EffectiveState` reports a state the synonym map does not carry back as it was declared. The synonyms map `undeployed` onto `registered` and leave `registered` and `deployed` alone. A value the map does not carry is refused by manifest validation before the planner runs, so `EffectiveState` never decides such a manifest's plan; it reports the declared value anyway, because a `TemplateRef` built any other way would otherwise report no state at all, and a state that matches neither branch drops the entry out of the plan in silence.

D42 — five more MCP tool schemas gained the parameters their routes read. Reviewer line L26 named `instance_frame_list` and `message_list`. The fitness check written to keep those two honest found five more in the same state: `event_list`, `instance_list`, `template_list`, `tag_list`, and `node_list` each took a bare `{additionalProperties:true}` object while its route accepts filters and pagination. Fixing the two the line named would have left the check failing and the trap open. Each of the seven now names what its route reads.

Fixer calls, certification rounds 1 to 3:

D44 — `template lint` calls `Render` for its effect. The verb wrapped the call in `if code := Render(...); code != 0`, and `Render` returns 0 on both paths, so the branch was unreachable and read as a third exit path the verb does not have. `Render` keeps its signature: `return Render(...)` is the shape every verb that ends at a render already uses, and three call sites already discard the value.

D45 — `asset versions` reports a producer error the same way in every format. The structured branch returned 0 before the error check, so one failing call exited 1 in human output and 0 under `-o json`. The verb now renders once — the structured formats emit the response body, which carries the `error` field, and human output prints the message on stderr — then exits 1 whenever the field is set.

D46 — `messages tail` reads one bounded page unless the caller names a window, and says when it truncated. Draining every page made the verb's default invocation walk the whole ledger, which is neither what "tail" says nor what the pagination item asked for. `--since` or `--until` names a window, and a window is read whole, because a window truncated at 100 rows is the trap reviewer line L17 named. A bare tail reads the newest 100 rows, as it did before the change. When that page comes back with a cursor, the verb prints one line on stderr naming the row count and the flags that reach the rest, in the same shape `rimsky audit` uses under D48. A default bound that says nothing is the silent truncation this sprint removes. `--follow` prints no notice, because a follow loop is not a truncated read.

D47 — every cursor rimsky mints is URL-safe, and one function mints it. `EncodeCursor` and `DecodeCursor` in `lib/foundation/persistence/pagination.go` hold the alphabet, and the fourteen per-table encoders across the two drivers, plus the CLI's fake server, call them. Before this, three shared encoders used `base64.RawURLEncoding` and the other fifteen sites used `base64.StdEncoding`, whose `+` decodes to a space in a query string. Sharing the pair rather than sweeping the alphabet keeps a sixteenth encoder from diverging.

D48 — `rimsky audit` reads the newest 100 rows unless the caller names a window, and says when it truncated. The audit feed is an append-only ledger that grows for the life of the deployment, so draining it was unbounded in both requests and memory. The verb takes `--since`/`--until` the way `messages tail` does, and reads a named window whole. A truncated read prints one line on stderr naming the row count and the flags that reach the rest, so the bound is not a second silent trap. The alternative, a `--limit` flag, adds surface no work item asked for and leaves the default unbounded.

D49 — one pager walks every cursor in the CLI. `pageAll` is now exported as `cli.PageAll`, `pagedListTemplates` and compose's `pagedListTags` call it in place of their own loops, and the two event-streaming loops that must print as they go — `instance events` and `watch` — carry the same two termination guards the helper does. A server that echoes a cursor back now terminates every walk rather than spinning in the three that hand-rolled it.

D50 — the one-state-per-template check runs in `ComputePlan`, not in `Manifest.Validate`. A template's state belongs to its content hash, and the manifest declares state per tag entry, so two tags naming one spec can declare two states. `Validate` reads the manifest alone and has no hash: the hash comes back from `ResolveTemplateThroughDeployment`, which needs a client. `Manifest.ValidateResolvedStates` therefore takes the resolved tag-to-hash map and runs at the first point the conflict is decidable, immediately after the planner resolves every template. It refuses the manifest, naming both tags and both states, in place of the deploy-then-undeploy pair the planner used to emit.

D51 — the producer-outbox page key is the outbox sequence. It was the enqueued instant joined to the sequence. `ListAll` reads in sequence order, and bounding the retained entries to one page requires the read order and the page order to agree; a composite key over an instant the writer supplies does not guarantee that. The sequence totally orders the outbox on its own, so the key is the zero-padded sequence and the retained prefix is exactly the page. Cursors are opaque and pre-v1, so no caller holds one across the change.

D52 — the expiry back-stamp compares SQLite timestamps as text in the driver's own layout. The driver stores every instant as `2006-01-02T15:04:05.000000000Z07:00`, so migration 050 builds the same shape from SQLite's own clock — `strftime('%Y-%m-%dT%H:%M:%f', 'now') || '000000Z'` — and compares strings. A bare `datetime('now')` would compare a nineteen-character string against a thirty-character one and stamp nothing.

D53 — the persistence conformance suite takes each driver's migration filesystem. Proving the back-stamp needs the pre-upgrade state, and the migration runner applies files once and never re-runs one. The suite's new case drops the column the migration adds, replays that one file from the driver's own embedded filesystem, and then asks the sweep what it reports. `Suite` therefore takes an `fs.FS` beside the raw exec and query hooks, and each driver's test passes its own `migrations.FS`. The case runs the shipped file, not a copy of its text.

D54 — the auth sweep loop holds a logger that is never nil. Both sweep errors were logged only when the caller supplied a logger, so a nil logger dropped a failing sweep in silence, which the events standard names a finding. The loop substitutes `shared.SilentLogger{}` for a nil logger once, at entry, and every branch then emits unconditionally. A caller that wants silence passes the silent logger, which is what it already means.

D55 — the deployment-wide `max_retries` caps infrastructure retries exactly as a node's own does. The reviewer read this as a possible intent fork: one retry policy for everything a dispatch retries, or an infra cap that is rimsky's own backstop and takes no operator value. It is not a fork. The infra path already read the node's `max_retries` before this sprint — a node declaring `max_retries: 1` capped infra retries at 1 — and `decision:dispatch-defaults-cover-every-node-timing-key` exists to stop one timing key having two behaviors depending on where it is set. A deployment that sets no default leaves the cap at rimsky's own ten, because an absent default reads as zero and zero means "rimsky's cap". Two tests hold both halves.

D56 — the shortcut-verb parity test compares the two spellings with the verb's own name elided. Each shortcut now names itself in its usage banner, because the `--help` item has every node of the verb tree print its own usage; an operator who typed `rimsky ls templates` and read `usage: rimsky template list` would be reading about a verb they did not type. The two spellings are therefore indistinguishable in everything but their name, which is what the test now asserts: same exit code, and the same output once each spelling's own name is replaced by one placeholder. The behavior stands.

D57 — the stub-mode override is a declared attribute with a null default, and a null override is no override. `verifier-http`, `verifier-shape-checks`, and `http-node` advertise a closed expected-attributes schema, so a template could not name `stub_response` or `stub_tags` at all and the capability the item promises was unreachable. Declaring them is not enough on its own: the effective-schema check requires every executor-declared input to carry a `source:`, a `default:`, or `readOnly: true`, so a bare declaration would have broken every existing template on those three executors. Each key therefore declares `default: null`, the shape `verifier-http` already uses for `body`, and `stubprobe` reads a null value as an absent one. A dispatch that carries `stub_response: null` gets the ordinary stub delta, exactly as one that carries no key at all.

D58 — `since` is removed from the breakpoint-hits reads rather than refused. `decision:pre-v1-pure-removal-for-retired-surfaces` has a retired surface leave no named remnant: no recognition rule, no targeted error. A 400 naming `since` would be that remnant. The parameter is now unread, which is what every rimsky route does with a query parameter it does not take. The generic MCP package's fake resource, which modelled a `since`/`next_since` polling feed to test URI passthrough, names its own parameter `after` now, so no test restates the retired spelling on a breakpoint-hits URI.

D59 — the frame-duration test seeds its start instant from the database clock. `TestRunTick_FrameEndDetection_ObservesDBStampedDuration` wrote `time.Now().Add(-5 * time.Second)` from the host and then asserted the gap to a Postgres-stamped `ended_at` exceeded four seconds, so its verdict depended on the host clock and the container clock agreeing to within a second. It failed once under a loaded parallel sweep and passed in isolation. Both instants now come from the database, so the assertion is a function of the code alone. Found while running the suites, not named by a finding.

D60 — the CLI client and the CLI's fake server drop `since` with the route. `Client.ListBreakpointHits` still took a `since` watermark and sent it when positive, and the fake server still honored it after the real handler stopped reading it. Every caller passed zero, so the parameter carried nothing. The fake, meanwhile, proved a paging behavior the deployment no longer has. Both sides now take `cursor` alone. The fake's in-memory reader names its parameter `afterSeq`, so the retired spelling survives nowhere on the client side.

D61 — the claude-agent schema declares the stub-mode override too. Its `additionalProperties: {"readOnly": true}` admits an undeclared *output*, which makes the schema closed to inputs under the validator's own predicate, so a template naming `stub_response` or `stub_tags` on a claude-agent node was rejected at registration while the executor read both. The schema now declares each with `default: null`, the shape the other three use. The registration test drives all four bundled executors and fails when a new one appears without a case, so the item's "every bundled executor" is checked rather than asserted.

D62 — the `.rimsky/latest` symlink is replaced by a plain atomic rename, not an inode swap. `TestUpdateLatestSymlink_ConcurrentReadersNeverSeeBroken` failed under load: a reader saw `ENOENT` on a link that the directory listing showed present a moment later. A standalone probe outside this repository reproduced it — 20,000 swaps with eight readers produce spurious `ENOENT` on APFS under `renamex_np(RENAME_SWAP)`, and zero under `rename(2)` over the same name. The swap was never needed: a rename over an existing name is atomic on every platform the project targets, and a lookup racing it finds the old inode or the new one. `swapAtomicInodes` and its three platform files are gone, and so is the sentinel symlink that briefly pointed `latest` at its own directory. Two hundred consecutive runs of the test pass. Found while running the batch's checks, not named by a finding.

Refutations: none. Reversals: none. Dissolutions: none.

### Findings fixed

40 ledger rows; 39 fixed, 1 promoted.
- Sprint alignment (the corpus-change judge): 3 fixed — the catalog order (C1), a stale D11 (C2), the bare `messages tail` notice (C38) — plus the four claimed forks routed to the architect (C3–C6: three overturned, one promoted).
- Code review (three standing reviewers over 79 + 54 + 79 files): 30 fixed (C7–C26, C30–C37, C39, C40).
- Test suites (`make test-all`): 3 fixed (C27–C29, all in `lib/services/test/scenarios`).
- The mechanical floor (`make lint`, annotation integrity, plumbline lint, `catalog-toc --check`, the ok-workspaces sweep): clean on first pass and on every round.
- Subtractions: 1 repeat (the reviewer's F16 re-raised the promoted F3); 0 reversals.

### The finding ledger

This report's `## Certification ledger` section holds the table.

### Issues promoted

- `.ok-planner/issues/2026-08-25-083400-migration-digest-covers-comments.md` — a fork the architect confirmed (F3): whether an applied migration's digest should cover its comments. Reasonable owners diverge: one reads the whole-file digest as the mechanical form of "no applied file is edited"; the other reads `decision:migrations-append-only-numbered`'s rationale as guarding the SQL that ran, which a comment is not. `/verify-issues` outcome: verified with a recommended ruling — digest the SQL alone (comment and blank lines stripped), pay the one-time global re-digest pre-v1, keep the citations; awaiting your ruling.

F3 — the migration digest and the citation repoint. **Architect ruling: confirmed a genuine fork, promoted.** The repoint stands. D30 states the one recovery step a database that already applied 038 runs before its next boot. Option (c) amends `decision:migrations-append-only-numbered`, and this sprint carries no delta for that decision. Reasonable owners diverge on it: one reads the whole-file digest as the mechanical form of "no applied file is edited", and the other reads the decision's rationale as guarding the SQL that ran, which a comment is not. The cost returns at every later rename of an artifact a migration cites. I filed the question as `.ok-planner/issues/2026-08-25-083400-migration-digest-covers-comments.md`. Until the owner rules, the tree keeps the whole-file digest and the repointed citation.

The next planning ceremony takes it up.

The sprint is certified with one issue promoted. Two acts remain yours: archive the sprint — move `2026-08-24-silent-traps-align-to-user-expectation.md`, its completion report, and its ledger file to `.ok-planner/history/sprints/`, and the promoted issue file `2026-08-24-201537-silent-traps-align-to-user-expectation.md` to `.ok-planner/history/issues/` — and commit the work. I do neither without your word; on yes, the archive commit is followed by one small commit stamping `closed: <sha>` into the archived sprint.

## Certification ledger

The certification gate (`/certify-work`) keeps this table. One row per finding the loop has held this run.

The mechanical producers ran first. `make lint` exited 0. The annotation check read 504 citations across 170 changed files and found none dangling. The plumbline lint over the changed files exited 0. `catalog-toc --check` exited 0. The ok-workspaces sweep over the changed files found no mutable tag in a verification path, no compose file, and no new image reference; the Makefile consumes the run-tag script. `make test-all` ended inconclusive: `tools/gotest-guard.sh` killed the root run after 20 minutes without progress with `TestErrorPolicyFirst_WinnerCancelsInFlightLosers` in flight; that test passed alone in 2.69 s, so the stall was machine saturation. `make test-foundation` and `make test-protocols` passed. `make test-services` failed three tests, rows C27–C29. `make core-images service-images test-images test-root`, rerun on a quiet machine, passed all 97 root packages. After round 1's fixes, `make test-all` passed whole: 174 packages ok across the four modules, no failure, no inconclusive kill. After round 3's fixes, `make test-all` passed whole again: 174 packages ok, no failure, no inconclusive kill. The fixer or the architect edited a file in rounds 1, 2, and 3. Round 4 held no finding and no edit. The reviewer reported `DRY`. The judge reported clean. The mechanical producers and the suites passed. No row stands at `open`. The loop ended there.

| id | site | producer | round entered | outcome | repeats | rounds touched | note |
|---|---|---|---|---|---|---|---|
| C1 | `.ok-planner/design/decisions.md`, the `http-poll-sensor-auth-outbound` bullet | alignment judge; code reviewer (its F8) | 1 | fixed 1 | 0 | 1 | bullet sits before `http-bridge-preserved`, out of alphabetical order |
| C2 | completion report `## Divergences` D11, "keep the single-page read they had" | alignment judge | 1 | fixed 1 | 0 | 1 | stale: L12 fix paged all three sites |
| C3 | completion report `## Divergences` F1 (`auth show` default format) | alignment judge | 1 | fixed 1 | 0 | 1 | architect: KICKBACK OVERTURNED, reading (a) stands, entry rewritten as a determined call |
| C4 | completion report `## Divergences` F2 (`messages tail` window axis) | alignment judge | 1 | fixed 1 | 0 | 1 | architect: KICKBACK OVERTURNED, the `delivered_at` axis stands, entry rewritten |
| C5 | completion report `## Divergences` F3 (migration 038 digest vs citation repoint) | alignment judge; code reviewer (its F16, a repeat) | 1 | promoted `.ok-planner/issues/2026-08-25-083400-migration-digest-covers-comments.md` | 1 | 1 | architect: CONFIRMED; tree stands at (b); prose mention of `host-agent-proxy` in both 038 files repointed |
| C6 | completion report `## Divergences` F4 (host-daemon conformance battery side) | alignment judge | 1 | fixed 1 | 0 | 1 | architect: KICKBACK OVERTURNED, reading (a) stands, entry rewritten |
| C7 | `cmd/rimsky/cli/templates.go::pagedListTemplates` | code reviewer | 1 | fixed 1 | 0 | 1 | the one pager still hand-rolled, lacks `pageAll`'s termination guards |
| C8 | `cmd/rimsky/cli/templates.go` `template lint`, `if code := Render(...); code != 0` | code reviewer | 1 | fixed 1 | 0 | 1 | dead branch: `Render` returns 0 on every path |
| C9 | `cmd/rimsky/cli/asset.go` `asset versions` structured branch | code reviewer | 1 | fixed 1 | 0 | 1 | producer error exits 0 under `-o json`/`-o yaml`, 1 under human |
| C10 | `cmd/rimsky/cli/messages.go::readMessagesPage` follow=false | code reviewer | 1 | fixed 1 | 0 | 1 | `messages tail` without `--follow` drains the whole ledger |
| C11 | `lib/foundation/persistence/pagination.go` and per-table cursor encoders | code reviewer | 1 | fixed 1 | 0 | 1 | two base64 alphabets: `RawURLEncoding` on three, `StdEncoding` on the rest |
| C12 | `lib/control/controlapi/breakpoints.go::handleListBreakpointHits` | code reviewer | 1 | fixed 1 | 0 | 1 | `limit` parsed twice by `parseSinceLimit` and `parseLimit` |
| C13 | `lib/control/controlapi/app_util.go::parseLimit` clamp at `parseLimitMax` | code reviewer | 1 | fixed 1 | 0 | 1 | over-max clamp unproven after `app_util_test.go` deletion |
| C14 | `cmd/rimsky/cli/audit.go::pagedListAudit` | code reviewer | 1 | fixed 1 | 0 | 1 | `rimsky audit` unbounded: no `--limit`, no default window |
| C15 | `cmd/rimsky/cli/compose/plan.go` deploy/undeploy on one hash from two tags | code reviewer | 1 | fixed 1 | 0 | 1 | conflicting declared states on one content hash resolve to a silent undeploy |
| C16 | `cmd/rimsky/cli/compose/manifest.go::EffectiveState` | code reviewer | 1 | fixed 1 | 0 | 1 | returns `""` for an unknown state instead of `t.State` |
| C17 | `lib/control/controlapi/admin_diagnostics.go` producer-outbox route | code reviewer | 1 | fixed 1 | 0 | 1 | dropped its page cap; materializes the whole backlog twice |
| C18 | `lib/control/controlapi/admin_diagnostics.go` and `admin_waitset.go` response maps | code reviewer | 1 | fixed 1 | 0 | 1 | five typed response structs bypassed by `map[string]any`; `omitempty` drift on producer outbox |
| C19 | `lib/control/observability/handler.go::pageServiceEntries` | code reviewer | 1 | fixed 1 | 0 | 1 | second copy of `pageByKey`; move the helper to `persistence/pagination.go` |
| C20 | `cmd/rimsky/cli/compose/manifest.go::Validate` instance on a `registered` template | code reviewer | 1 | fixed 1 | 0 | 1 | undeploy runs before instance create; no validation error |
| C21 | `lib/foundation/persistence/{postgres,sqlite}/api_keys.go::SweepExpired` predicate | code reviewer | 1 | fixed 1 | 0 | 1 | fires `auth.key_expired` for keys already revoked; no `revoked_at IS NULL` term |
| C22 | migration 050 on both drivers, `expiry_event_at` backfill | code reviewer | 1 | fixed 1 | 0 | 1 | first sweep after upgrade emits one event per historical expiry |
| C23 | `lib/control/config/scheduler.go` sweep error branches | code reviewer | 1 | fixed 1 | 0 | 1 | errors swallowed when `log == nil`; dead `continue` |
| C24 | `lib/runtime/runner_error_policy.go::applyTerminalInfraError` | code reviewer | 1 | fixed 1 | 0 | 1 | deployment `max_retries` now caps infra-error retries; possible intent fork, untested |
| C25 | `cmd/rimsky/conformance_test.go` `protocols` slice | code reviewer | 1 | fixed 1 | 0 | 1 | `host-daemon` missing from the leak guard's population |
| C26 | `lib/services/executors/claude-agent/agentrun.go` stub outcome | code reviewer | 1 | fixed 1 | 0 | 1 | honors `stub_response` but not `stub_tags`, unlike the other three executors |
| C27 | `lib/services/test/scenarios/read_only_role_e2e_test.go:83` | test suites (`make test-services`) | 1 | fixed 1 | 0 | 1 | the test has no value for path param `idOrKey` on `/v1/instances/{idOrKey}/frames` |
| C28 | `lib/services/test/scenarios/cli_shortcut_verbs_e2e_test.go:60` | test suites (`make test-services`) | 1 | fixed 1 | 0 | 1 | shortcut verbs' undefined-flag output differs from the grouped forms' (usage names the shortcut, D5) |
| C29 | `lib/services/test/scenarios/mcp_transport_parity_e2e_test.go:270-272` | test suites (`make test-services`) | 1 | fixed 1 | 0 | 1 | `asset_list` and `message_send` tool calls pass `id`; the tools now take `idOrKey` |
| C30 | `lib/services/executors/{verifier-http,verifier-shape-checks,http-node}/identity.go::SchemaBytes` | code reviewer (F28) | 1 | fixed 1 | 0 | 1 | closed schemas do not declare `stub_response`/`stub_tags`, so a template cannot set them |
| C31 | `lib/services/internal/stubprobe/stubprobe.go::Tags` | code reviewer (F29) | 1 | fixed 1 | 0 | 1 | malformed `stub_tags` silently dropped where `SuccessDelta` errors |
| C32 | `lib/services/sensors/sensor-http/state_db_test.go::TestStateDB_KeepsNoCopyOfTheUpstreamCredential` | code reviewer (F30) | 1 | fixed 1 | 0 | 1 | inspects only `string` values; `bytea`/`jsonb` columns escape the guard |
| C33 | `cmd/rimsky/cli/time_window_test.go::TestAuditWalksTheWholeWindowAcrossPages` | code reviewer (F23) | 1 | fixed 1 | 0 | 1 | specious paging assertion: `Contains(out, "1"/"2"/"3")` matches the timestamp |
| C34 | `cmd/rimsky/cli/admin_test.go:23` | code reviewer (F24) | 1 | fixed 1 | 0 | 1 | dead `_ = clitest.Server{}` keeping an import alive |
| C35 | `lib/control/controlapi/breakpoints.go` and `mcp_resources.go` breakpoint-hits `since` + `cursor` | code reviewer (F25) | 1 | fixed 1 | 0 | 1 | two idioms for one watermark; both given, the larger silently wins; `next_since` gone |
| C36 | `lib/control/controlapi/mcp/server_test.go::TestMCPUnsupportedMethod` | code reviewer (F26) | 1 | fixed 1 | 0 | 1 | duplicates `TestMCPUnimplementedBaseMethodsAnswerMethodNotFound` on `prompts/list` |
| C37 | `lib/control/controlapi/mcp/server_test.go::TestMCPServesEveryMethodTheDecisionNames` | code reviewer (F27) | 1 | fixed 1 | 0 | 1 | names six served methods, tables five: `resources/read` missing |
| C38 | `cmd/rimsky/cli/messages.go` bare `messages tail` bound (D46) | alignment judge (round 2, veto test) | 2 | fixed 2 | 0 | 1 | the bare tail caps at 100 rows with no stderr notice, unlike `rimsky audit` (D48) |
| C39 | `cmd/rimsky/cli/client_instances.go::ListBreakpointHits` `since` parameter; `clitest/server.go` breakpoint-hits fake | code reviewer (F31, round 2 verification) | 3 | fixed 3 | 0 | 1 | retired `since` survives on the client (dead) and in the fake (honored), diverging from the route |
| C40 | `lib/services/executors/claude-agent/expected_attributes_schema.json`; `stub_override_registration_test.go` | code reviewer (F32, round 2 verification) | 3 | fixed 3 | 0 | 1 | claude-agent's closed schema omits `stub_response`/`stub_tags`; the universal test excludes it |
