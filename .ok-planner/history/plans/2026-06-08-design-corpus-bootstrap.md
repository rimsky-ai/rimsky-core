# Design Corpus Bootstrap Implementation Plan

**Spec:** `.ok-planner/specs/2026-06-08-design-corpus-bootstrap-design.md`
**Goal:** Bootstrap the `design/stories/` and `design/decisions/` durable catalogs, resolve three open tensions with the code work they imply (control-API URL prefix sweep, event-kind typed enum), and produce a committed proof artifact per user-outcome story.
**Architecture:** Three foundational passes (URL prefix sweep, event-kind enum, design-doc bootstrap) land first because every downstream acceptance pass depends on them. Then 62 acceptance passes, one per story, each landing the integrating wiring (if any) and a committed proof artifact that exhibits the story through the real assembled product.
**Tech Stack:** Go 1.25 (`go-chi/chi`, `jackc/pgx/v5`, `modernc.org/sqlite`, stdlib `log/slog`, `testcontainers-go`); TypeScript (`vitest`) for the claude-agent executor reference; proto/gRPC (`google.golang.org/grpc`).

---

## Plan-wide notes the implementer must read first

**Read the spec end-to-end before starting.** Every acceptance pass references the spec's story by slug for the user-outcome contract — the spec carries the Role / Capability / Acceptance / Falsifier / Proof form / Existing-artifact citation for each story; this plan carries the operational instructions for delivery.

**The spec at `.ok-planner/specs/2026-06-08-design-corpus-bootstrap-design.md` is the source of truth for story contracts.** When this plan says "deliver STORY-template-lifecycle," look up the story's body in the spec and exhibit *exactly* the Acceptance it states. The plan does not restate Acceptance; the spec is the contract.

**Pass dependency ordering.** Passes 1–3 are foundational — they reshape surfaces the acceptance passes hit. Passes 4–65 (the acceptance passes) can in principle run in any order after the foundational passes, but the plan presents them grouped by spec cluster for reviewability. `execute-plan` dispatches them serially; the order in this plan is the dispatch order.

**Load-bearing properties (carried throughout).** Three properties get traded away silently if the implementer is not warned:
- **Real assembled product.** Every proof artifact boots the actual rimsky stack (the `rimsky-all-in-one:latest` testcontainers image, or the rimsky processes in-tree for unit-style proofs) and drives it through the real entry point named in the relevant TD (HTTP route, CLI verb, gRPC RPC). In-process construction of components followed by direct handler calls is not an acceptance — it's a stub that hides whether the wiring is real.
- **Real value-delivering component.** When a story says "the executor does real work" or "the producer atomically swaps", the executor / producer in the proof is the real implementation, not a stub. Canned stubs at the value-delivering component defeat the entire pass.
- **Co-transactional invariants.** Where the spec or an `@blessed-invariant` annotation says "X and Y commit together" (notably `breakpoint.hit` event + `breakpoint_hits` row from STORY-breakpoint-debugger), the proof must drive a real path where both writes share a transaction; verifying only that both happened "eventually" is not the same property.

**No git operations.** This plan produces working-tree edits and runnable artifacts. The user commits when ready.

**Verification commands.** After each pass, the implementer runs the verification commands listed in that pass's tasks. The bottom-line gate is: `go build ./... && go test ./... && make lint` clean. Race-sensitive packages (`lib/runtime/...`, `lib/foundation/persistence/postgres/...`) are run with `-race -count=3` where the task names them. Scenario tests under `test/scenarios/...` and `lib/services/test/scenarios/...` use testcontainers and require Docker running.

---

## Pass 1: Control-API URL prefix sweep — every bare route under `/v1/`

**Goal:** Resolve `tension:control-api-version-prefix` (per `TD-protocol-version-v1-namespaced` in the spec) by mounting the entire control-API HTTP surface under `/v1/`. Every existing bare path (`/templates`, `/instances`, `/tags`, `/nodes`, `/messages`, `/events`, `/audit`, `/auth/*`, `/lineage/*`, `/admin/*`, `/diagnostics/*`, `/backfills/*`, `/lock-holders/*`, `/health`, `/mcp`) becomes `/v1/...`. The two existing `/v1/` carve-outs (`/v1/callback/...`, `/v1/observability/...`) absorb into the same prefix. Pre-v1 freedom (per `decision:pre-v1-break-freely`): no transition window; bare paths are removed.

**Scope:** Tasks 1–4

**Falsifier:** any bare-path control-API route still registered (the chi router responds to `/templates` rather than only `/v1/templates`), OR a test under `lib/control/controlapi/...`, `test/scenarios/...`, or `lib/services/test/scenarios/...` still asserts against a bare path, OR the rimsky CLI client (`cmd/rimsky/cli/...`) still constructs requests with bare paths, OR the MCP route-resolution catalog still resolves MCP tools to bare paths.

### Task 1: Move every chi route under `/v1/` in `lib/control/controlapi/app.go`

**Files:** `lib/control/controlapi/app.go`

**Steps:**
1. Open `lib/control/controlapi/app.go`. The chi router is constructed in `NewApp(deps AppDeps) http.Handler` — locate this function (around `lib/control/controlapi/app.go:171`) and find the body where `r := chi.NewRouter()` is followed by sibling `register*Routes(r, deps)` calls plus inline mounts.
2. Wrap the entire route tree under `r.Route("/v1", func(rrr chi.Router) { ... })`. Move every existing `register*Routes(r, deps)` call (and any inline `r.Mount(...)`, `r.Route(...)`, `r.Method(...)`, `r.Get(...)`, `r.Post(...)`, `r.Put(...)`, `r.Delete(...)` registrations on bare paths) under the new `/v1/` sub-router so they pass `rrr` instead of `r`.
3. The `/v1/observability/*` carve-out (currently registered via `observabilityWrapper` at around `lib/control/controlapi/app.go:203-205`) becomes `r.Route("/observability", or)` inside the new `/v1/` sub-router — adjust the wrapper accordingly so it no longer double-prefixes.
4. The `/mcp` route (the JSON-RPC streaming-HTTP surface) moves to `/v1/mcp`.
5. **`/v1/health` must remain reachable without auth.** Currently `registerHealthRoutes(r, deps)` is registered pre-auth-middleware (around `app.go:182`); when moved under `/v1/`, the same pre-auth-middleware position must be preserved (the spec's STORY-rimsky-health-check Falsifier names "requires auth" as a failure). Two ways to do this: (a) move `r.Route("/v1", ...)` itself outside the auth middleware and register both `/v1/health` and the auth pipeline group inside; (b) keep auth as a chi `r.Group(...)` inside the `/v1/` route and register `/v1/health` as a sibling-bare register before that group. Either shape works; pick the one that minimizes diff against existing structure.
6. **The executor async-callback listener is NOT touched here.** That listener is a separate `CallbackServer` at `lib/runtime/callback.go` (around line 232), already registered at `/v1/callback/{async_ack_id}` and `/v1/runs/{run_id}/attributes` on its own listener; it does not flow through `controlapi.NewApp`. Verify by `rg '/v1/callback' lib/` — the callback routes are already on `/v1/` and need no change. No-op for this task.
7. Run `go build ./lib/control/controlapi/...` and confirm it compiles.

### Task 2: Update every test under `lib/control/controlapi/...` to hit `/v1/` paths

**Files:** every `*_test.go` under `lib/control/controlapi/`

**Steps:**
1. Run `rg -l 'httptest|http\.NewRequest|http\.Request' lib/control/controlapi/` to identify test files that construct HTTP requests.
2. In each file, replace bare-path request constructions (`http.NewRequest("POST", "/instances", ...)`, `r.URL.Path = "/templates/abc"`, etc.) with the `/v1/`-prefixed form (`"/v1/instances"`, `"/v1/templates/abc"`).
3. Run `go test ./lib/control/controlapi/... -count=1` and confirm every test passes after the sweep.

### Task 3: Update every scenario test under `test/scenarios/...` and `lib/services/test/scenarios/...` to hit `/v1/` paths

**Files:** every `*_test.go` under `test/scenarios/` and `lib/services/test/scenarios/` that constructs HTTP requests against the control-API.

**Steps:**
1. Run `rg -l '"/templates"|"/instances"|"/tags"|"/nodes"|"/messages"|"/events"|"/audit"|"/auth/"|"/lineage/"|"/admin/"|"/diagnostics/"|"/backfills"|"/lock-holders/"|"/health"|"/mcp"' test/scenarios/ lib/services/test/scenarios/`.
2. In each hit, prefix the path with `/v1`. Helper-shared path constants (e.g. in `test/support/scenario/...`) should be updated once at the helper; tests that hard-code paths are updated in place.
3. Run `go test ./test/scenarios/... -count=1` and `go test ./lib/services/test/scenarios/... -count=1` (Docker must be running for the scenario harness's testcontainers). Confirm green.

### Task 4: Update the rimsky CLI client + MCP tool catalog to use `/v1/` paths

**Files:** `cmd/rimsky/cli/client.go` (the CLI's primary HTTP client); other files under `cmd/rimsky/cli/` that issue HTTP requests; `lib/control/controlapi/mcp_resources.go` and `lib/control/controlapi/mcp/catalog.go` (the MCP route resolution).

**Steps:**
1. Open `cmd/rimsky/cli/client.go` and update every bare-path URL construction to use `/v1/...`. Cross-check with `rg 'http\.NewRequest|client\.(Get|Post|Put|Delete)' cmd/rimsky/cli/` to catch any other client-issuing files.
2. Update each request site to issue `/v1/`-prefixed URLs.
3. In `lib/control/controlapi/mcp_resources.go` and the MCP catalog, update the route mapping so MCP tools resolve to the new `/v1/` paths (the catalog drives in-process tool calls back through the chi router).
4. Run `go build ./cmd/rimsky/... && go test ./cmd/rimsky/... -count=1` and confirm green.
5. Run `make lint` and confirm clean.

---

## Pass 2: Event-log kind typed enum — proto extension + emit-site sweep + persistence marshaling

**Goal:** Resolve `tension:events-kind-no-enum` (per `TD-event-log-kind-enum` in the spec) by declaring the canonical operational `rimsky_events.kind` values as an enum in `proto/v1/events.proto`, sweeping every emit site under `lib/runtime/`, `lib/graph/`, `lib/control/...`, `lib/foundation/...` to use the typed value (never a raw string), and tightening the persistence-layer marshal/unmarshal paths to defensively reject unknown strings at the unmarshal boundary. Signal-class kinds keep their existing type-path discipline (see `decision:event-log-payload-shapes`). The persistence column shape (`TEXT` today) is unchanged.

**Scope:** Tasks 5–10

**Falsifier:** the `OperationalKind` proto enum is not declared in `proto/v1/events.proto`, OR rimsky's app logic (the supervisor, scheduler, audit handler, read-API kind filters) still constructs a `rimsky_events.kind` value from a literal string anywhere, OR the persistence-layer unmarshal accepts an unknown kind string silently, OR `GET /v1/events?kind=...` / `GET /v1/audit?kind=...` accept an unknown kind without rejecting at the request boundary.

### Task 5: Add the `OperationalKind` enum to `proto/v1/events.proto`

**Files:** `lib/protocols/proto/v1/events.proto`; regenerated bindings under `lib/protocols/proto/v1/gen/`

**Steps:**
1. Open `lib/protocols/proto/v1/events.proto`. Currently the `Event.kind` field is `string kind = 5;`.
2. Add an `enum OperationalKind { ... }` declaration enumerating every operational kind currently emitted by rimsky: enumerate from the existing emit sites by running `rg -nE '"(state_transition|lock_[a-z]+|work_[a-z]+|attributes_[a-z]+|breakpoint\.[a-z]+|auth\.[a-z_]+|claim_[a-z]+|template_[a-z]+|message_[a-z]+|heartbeat_lost|operator_override|orphaned_claim_[a-z_]+|unresolved_executor|node_resolution_[a-z]+|noop_commit|no_op_commit|named_event_emitted)"' lib/ cmd/` to find every literal kind string. The enum's value name convention: `OPERATIONAL_KIND_<UPPER_SNAKE>` (e.g., `OPERATIONAL_KIND_STATE_TRANSITION`, `OPERATIONAL_KIND_LOCK_ACQUIRED`).
3. **Do NOT change `Event.kind`'s field type yet** — leave it as `string` for backward compatibility on the wire (the existing typed-payload oneof is unchanged); the typed enum is what app logic consumes internally, with marshaling to the wire `string` happening at the protocol boundary. This matches the design (storage is marshaling detail).
4. Run `make proto-gen` and confirm `lib/protocols/proto/v1/gen/events.pb.go` regenerates cleanly with the new `OperationalKind` Go enum constants.
5. Run `go build ./lib/protocols/...` and confirm green.

### Task 6: Introduce a typed-enum API in `lib/foundation/events/kinds.go` (NEW)

**Files:** `lib/foundation/events/kinds.go` (new); `lib/foundation/events/kinds_test.go` (new)

**Steps:**
1. Create `lib/foundation/events/kinds.go`. Define a typed value (e.g., `type Kind struct { code OperationalKindCode; signalPath SignalTypePath }` or simpler — a thin wrapper around the proto enum + signal-class type-path discriminator).
2. Provide constructors:
   - `OperationalKindFromProto(genv1.OperationalKind) Kind` — typed operational kind.
   - `SignalKind(path SignalTypePath) Kind` — typed signal-class kind (carries the parsed type-path).
3. Provide a `Kind.String() string` method that returns the canonical wire form (the snake_case operational name, or the signal type-path).
4. Provide a `ParseKindString(string) (Kind, error)` function for the unmarshal boundary; unknown strings return an error.
5. Write `lib/foundation/events/kinds_test.go` covering: round-trip (typed → string → typed) for every operational kind; round-trip for signal-class kinds (`terminal/...`, `transient/...`, etc.); `ParseKindString` errors on unknown / empty / malformed input.
6. Run `go test ./lib/foundation/events/... -count=1` and confirm green.

### Task 7: Sweep every event-emit site to use the typed enum

**Files:** Every `*.go` file under `lib/runtime/`, `lib/graph/`, `lib/control/`, `lib/foundation/` (excluding `_test.go` and `lib/foundation/events/`) that constructs an event row.

**Steps:**
1. The actual rimsky emit API is `Events().Append(ctx, persistence.EventAppendInput{Kind: "...", ...})`. Run `rg -n 'EventAppendInput\{|Events\(\)\.Append' lib/ cmd/` to list every call site (there are roughly 28 emit sites under `lib/runtime/`, plus sites in `lib/graph/`, `lib/foundation/audit/`, and `lib/control/controlapi/audit_read.go`).
2. The single point-of-truth change: in `lib/foundation/persistence/events.go` (around line 25 — verify via `rg 'type EventAppendInput' lib/foundation/persistence/`), change the `Kind` field from `string` to `events.Kind`. This forces every emit site to update via compile errors.
3. In each call site surfaced by the compile errors, replace the literal kind string (e.g., `"state_transition"`, `"lock_acquired"`, `"auth.access_attempted"`, `"breakpoint.hit"`) with the typed `events.Kind` value constructed via `events.OperationalKindFromProto(genv1.OperationalKind_OPERATIONAL_KIND_STATE_TRANSITION)` (or the analogous constant) for operational kinds, or `events.SignalKind(parsedPath)` for signal-class kinds.
4. **Load-bearing property:** every emit-site signature should accept `events.Kind`, not `string`. The cheaper shape (passing `string` through internal layers) must NOT be used — that's exactly the typo footgun this resolution exists to prevent. The persistence-row constructor marshals `events.Kind` to wire string only inside the persistence layer.
5. Run `go build ./... && go test ./lib/runtime/... ./lib/graph/... ./lib/control/... ./lib/foundation/... -count=1` and confirm green.

### Task 8: Tighten the persistence-layer marshal/unmarshal at write and read

**Files:** `lib/foundation/persistence/postgres/events.go`, `lib/foundation/persistence/sqlite/events.go` (and any sibling read paths)

**Steps:**
1. In each persistence driver's event-row write path, accept `events.Kind` (typed) and call `kind.String()` to produce the `TEXT` column value.
2. In each persistence driver's event-row read path, scan the `TEXT` column into a local `string`, then call `events.ParseKindString(...)`; on parse error, log the offending string with `slog.Error("events.unknown_kind_at_unmarshal", slog.String("raw", v))` and **return the parse error from the read path** — do not silently emit a synthetic `unknown` kind. An unknown string at the unmarshal boundary is a defensive error (per the spec's TD); the read path surfaces it.
3. Add unit tests in `lib/foundation/persistence/postgres/events_test.go` (testcontainers) and `lib/foundation/persistence/sqlite/events_test.go` (in-process): write a typed `events.Kind`, read it back, assert it round-trips; insert a deliberately-corrupted `kind` value via raw SQL, read through the API, assert the read returns an error citing the offending string.
4. Run `go test ./lib/foundation/persistence/postgres/... -count=1` and `go test ./lib/foundation/persistence/sqlite/... -count=1` (Docker for postgres). Confirm green.

### Task 9: Validate `?kind=` query parameter at the read-API boundary

**Files:** `lib/control/controlapi/events.go` (the `GET /events` handler — already accepts `?kind=`); `lib/control/controlapi/audit_read.go` (the `GET /audit` handler — currently pins `KindIn: auditKinds` (the auth.* allowlist) around line 65; this task adds a `?kind=` parameter that intersects with the allowlist)

**Steps:**
1. In `lib/control/controlapi/events.go`, the handler already reads a `kind` query parameter via `r.URL.Query().Get("kind")`. Call `events.ParseKindString(...)` on the non-empty value; on parse error, respond with `400 Bad Request` and a clear diagnostic naming the offending string and listing the canonical kind set (or pointing at a "see proto enum" hint).
2. In `lib/control/controlapi/audit_read.go`, add a new `?kind=` query parameter to the `GET /audit` handler — currently the handler pins kinds internally to the `auth.*` allowlist. The new parameter intersects with that allowlist: if `?kind=` is supplied and it's a non-auth-prefix kind, return `400 Bad Request` (the kind is valid in the proto enum but not in the audit surface's allowlist); if it's an auth-prefix kind, narrow the read to that single kind; if `?kind=` is absent, behave exactly as today (all auth.* kinds returned).
3. An empty `kind` parameter on either route is allowed (no filter); only non-empty unknown strings reject.
4. Add tests in `lib/control/controlapi/events_test.go` (new file) and `lib/control/controlapi/audit_read_test.go` (new file): events route — request with a known operational kind succeeds, unknown kind returns 400, no kind returns unfiltered; audit route — request with an `auth.*` kind narrows to that kind, request with a non-auth operational kind (e.g., `state_transition`) returns 400 even though the kind is in the proto enum, request with an unknown kind returns 400, no kind returns the full auth.* set.
5. Run `go test ./lib/control/controlapi/... -count=1` and confirm green.

### Task 10: Race + lint gate

**Files:** none (verification only)

**Steps:**
1. Run `go test ./lib/runtime/... ./lib/foundation/persistence/postgres/... -race -count=3` and confirm clean (the sweep touched typed-value passing in code paths the runtime exercises under concurrency).
2. Run `make lint` and confirm clean.

---

## Pass 3: Design-doc bootstrap — directories, TOCs, 62 stories + 75 decisions + 3 concept mutations + 3 tension moves

**Goal:** Land the durable-doc surface the spec's `## Design changes` section enumerates: create `.ok-planner/design/stories/` and `.ok-planner/design/decisions/` as directories with one durable file per story / decision in the spec, with bodies path-rewritten per the spec's self-containment treatment (rules 1–5 in the spec's `## Design changes` section). Mutate `concepts/module-layout.md`, `concepts/message.md`, `concepts/event-log.md` per the spec's mutation instructions. Move three tensions to `tensions/_resolved/`. Refresh `concepts.md` if any mutation touched it (it shouldn't — concept-list TOC is unaffected), and generate `stories.md` + `decisions.md` TOCs.

**Scope:** Tasks 11–17

**Falsifier:** any file enumerated in the spec's design-changes file lists (62 story files + 75 decision files + 2 TOCs) is missing, OR a story / decision body contains a file path that should have been rewritten per the self-containment rule (run `rg -l 'lib/foundation/|lib/protocols/|lib/services/|lib/runtime/|lib/graph/|lib/control/|cmd/|test/|tools/|examples/' .ok-planner/design/stories/ .ok-planner/design/decisions/` — should return zero hits), OR a tension move left the source file in `tensions/` AND in `_resolved/`, OR a concept mutation didn't apply (the old text remains in `concepts/module-layout.md` / `message.md` / `event-log.md`).

### Task 11: Create catalog directories

**Files:** `.ok-planner/design/stories/` (new dir); `.ok-planner/design/decisions/` (new dir)

**Steps:**
1. Run `mkdir -p .ok-planner/design/stories/ .ok-planner/design/decisions/`.
2. Confirm via `ls .ok-planner/design/` that both directories now exist.

### Task 12: Write 62 story files into `.ok-planner/design/stories/`

**Files:** 62 new `.ok-planner/design/stories/<slug>.md` files

**Steps:**
1. For each STORY-«slug» in the spec's `## User outcomes` section, create `.ok-planner/design/stories/<slug>.md` (the slug is the STORY's slug lowercased without the `STORY-` prefix; e.g., `STORY-template-lifecycle` → `stories/template-lifecycle.md`).
2. The file's shape:
   - Frontmatter: `story: <slug>` + `status: as-is`. No `references:` field (per the spec's frontmatter shape rule).
   - Body sections: `# <Capability headline>`, `## Role`, `## Capability`, `## Business value`, `## Acceptance`, `## Falsifier`, `## Proof`. The text comes from the STORY's body in the spec — **rewrite every path per the spec's path-rewriting rules 1–5** (e.g., `examples/` → "the examples module", `lib/services/test/scenarios/` → "the services module's scenarios test directory", `.claude/rules/rules.md` → "the after-code-changes verification rules"). The `Existing artifact:` line is stripped wholesale.
   - `## Notes` section ending with: `2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.`
3. The full list of 62 slugs to create is enumerated in the spec's `## Design changes → Story file creation` block. Work through it exhaustively.
4. After each file is written, run `rg -l 'lib/foundation/|lib/protocols/|lib/services/|lib/runtime/|lib/graph/|lib/control/|cmd/|^test/|tools/|^examples/' .ok-planner/design/stories/<slug>.md` and confirm zero hits before moving on. Any hit means the path-rewriting rule wasn't applied.
5. After all 62 files are written, run `ls .ok-planner/design/stories/ | wc -l` and confirm it equals 62.

### Task 13: Write 75 decision files into `.ok-planner/design/decisions/`

**Files:** 75 new `.ok-planner/design/decisions/<slug>.md` files

**Steps:**
1. For each TD-«slug» in the spec's `## Technical decisions` section, create `.ok-planner/design/decisions/<slug>.md` (slug rule: `TD-module-split` → `decisions/module-split.md`).
2. The file's shape:
   - Frontmatter: `decision: <slug>` + `status: as-is`. No `references:` field.
   - Body sections: `# <Decision headline>`, `## Choice`, `## Rationale`, `## Alternatives` (omit if the spec's TD has no Alternatives line).
   - Apply path-rewriting rules 1–5 to the body. The depguard, layout, language-split, and licensing TDs carry path citations that need rewriting (e.g., `lib/protocols/` → "the protocols module"); the library-choice TDs carry Go import identifiers that **pass through unchanged** per rule 5.
   - `## Notes` section ending with: `2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.`
3. The full list of 75 slugs is enumerated in the spec's `## Design changes → Decision file creation` block. Work through it exhaustively.
4. After each file is written, run `rg -l 'lib/foundation/|lib/protocols/|lib/services/|lib/runtime/|lib/graph/|lib/control/|cmd/|^test/|tools/|^examples/' .ok-planner/design/decisions/<slug>.md` and confirm zero hits (rule 5 pass-through identifiers like `jackc/pgx/v5` are not in-repo paths and won't match).
5. After all 75 files are written, run `ls .ok-planner/design/decisions/ | wc -l` and confirm it equals 75.

### Task 14: Generate the TOCs `stories.md` and `decisions.md`

**Files:** `.ok-planner/design/stories.md` (new); `.ok-planner/design/decisions.md` (new)

**Steps:**
1. Create `.ok-planner/design/stories.md`. Header: `# Story catalog (auto-generated)`. Followed by a sentence: "Read first. Then read `stories/<slug>.md` for the full body. Generated by execute-plan when a plan touches `stories/`. Do not edit by hand — changes will be overwritten."
2. Then a `## Stories` section with one bullet per story file, sorted alphabetically: `` - `<slug>` — <one-line headline from the story body>. ``. The headline is the H1 of the story file (the `# <Capability headline>` line).
3. Same for `.ok-planner/design/decisions.md` (`# Decision catalog (auto-generated)` + same intro + `## Decisions` section).
4. Verify the line counts: stories.md should have 62 bullets; decisions.md should have 75.

### Task 15: Apply the three concept mutations

**Files:** `.ok-planner/design/concepts/module-layout.md`, `.ok-planner/design/concepts/message.md`, `.ok-planner/design/concepts/event-log.md`

**Steps:**
1. **`concepts/module-layout.md`:** apply the mutation directives in the spec's `## Design changes → Concept mutations` block for module-layout. The spec instructions are scoped: the "Apache surface" / "single Go Apache island" replacements apply ONLY in the `## Licensing boundary` section, not in Notes entries. Append the 2026-06-08 Notes entry verbatim.
2. **`concepts/message.md`:** apply the mutation directive in the spec's design-changes block for message. The `## Idempotency` section's first paragraph is replaced wholesale per the spec's instructions. Append the 2026-06-08 Notes entry verbatim.
3. **`concepts/event-log.md`:** apply the mutation directive in the spec's design-changes block for event-log: the `## What it is` "free-form `kind` text column" phrase, the `## Purpose` "free-form `kind` column lets…" sentence, the `## Invariants` "kind column is free-form" line, and the `## Invariants` closing tension-reference line all get the rewrites the spec specifies. Append the 2026-06-08 Notes entry verbatim.
4. Verify each mutation: `rg -n 'four-module|free-form .kind.|When present' .ok-planner/design/concepts/module-layout.md .ok-planner/design/concepts/message.md .ok-planner/design/concepts/event-log.md` should return zero hits in the now-mutated sections (the old text is gone). Notes entries that historically referenced "four-module" or "free-form" stay as historical record — those aren't being rewritten.

### Task 16: Move three tensions to `_resolved/`

**Files:** move (`git mv` if tracked, else `mv`) `.ok-planner/design/tensions/substitution-grammar-count-drift.md`, `.ok-planner/design/tensions/control-api-version-prefix.md`, `.ok-planner/design/tensions/events-kind-no-enum.md` into `.ok-planner/design/tensions/_resolved/`.

**Steps:**
1. For each of the three tension files, run `git mv .ok-planner/design/tensions/<slug>.md .ok-planner/design/tensions/_resolved/<slug>.md` (this preserves git history).
2. Edit each moved file: set `status: resolved` in the frontmatter (was `status: open`), and add a `## Resolution` section quoting the resolution text from the spec's `## Design changes → Tension moves` block (one paragraph per tension; the spec's resolution text is the source of truth).
3. Verify with `ls .ok-planner/design/tensions/substitution-grammar-count-drift.md .ok-planner/design/tensions/control-api-version-prefix.md .ok-planner/design/tensions/events-kind-no-enum.md 2>&1 | grep -c 'No such file'` returns `3` (all three sources gone from `tensions/`).
4. Verify with `ls .ok-planner/design/tensions/_resolved/substitution-grammar-count-drift.md .ok-planner/design/tensions/_resolved/control-api-version-prefix.md .ok-planner/design/tensions/_resolved/events-kind-no-enum.md` shows three files in the resolved bucket.

### Task 17: Bootstrap verification gate

**Files:** none (verification only)

**Steps:**
1. Run `ls .ok-planner/design/stories/ | wc -l` — confirm 62.
2. Run `ls .ok-planner/design/decisions/ | wc -l` — confirm 75.
3. Run `ls .ok-planner/design/stories.md .ok-planner/design/decisions.md` — confirm both exist.
4. Run `rg -l 'lib/foundation/|lib/protocols/|lib/services/|lib/runtime/|lib/graph/|lib/control/|cmd/|^test/|tools/|^examples/' .ok-planner/design/stories/ .ok-planner/design/decisions/` — confirm zero hits (rule 1–5 path-rewriting applied everywhere).
5. Run `ls .ok-planner/design/tensions/_resolved/substitution-grammar-count-drift.md .ok-planner/design/tensions/_resolved/control-api-version-prefix.md .ok-planner/design/tensions/_resolved/events-kind-no-enum.md` — confirm all three present.

---

## Acceptance passes (Passes 4–65) — one per story

Each acceptance pass below has the same shape:

- **Story:** the STORY-«slug» from the spec
- **Goal:** deliver the story end-to-end through the real assembled product
- **Falsifier:** from the spec's story body (`Falsifier:` line) — never weaker
- **Tasks:** wiring tasks (if any) followed by exactly one proof task

For each pass, the proof task:
- Names the artifact file(s) the implementer creates or extends
- Quotes the story's `Proof:` line from the spec verbatim
- References the story by slug

**Existing-artifact handling:** for stories whose spec body cites an `Existing artifact:` that's a real file (verified in the spec), the proof task's goal is to **extend that file** so it exhibits the full story Acceptance (closing any partial-coverage gap noted in the spec) and re-runs green post-foundational-passes. For stories with `Existing artifact: fresh-proof-needed`, the proof task authors a new file.

**Reading the story body during execution:** the implementer must read the story in the spec before authoring the proof, then re-read after authoring to confirm the artifact exhibits every clause of the Acceptance. The spec's story is the contract; this plan is the operational shell.

---

## Pass 4: STORY-template-lifecycle (acceptance pass — STORY-template-lifecycle)

**Goal:** Deliver `STORY-template-lifecycle` end-to-end through the real control-API at `/v1/templates`.
**Scope:** Task 18
**Falsifier:** Deployed-vs-undeployed state is recorded but not gated on at instance creation (an undeployed template still produces a running instance), OR pre-flight validation persists, OR delete succeeds while live instances reference the template.

### Task 18: Author the executable proof for STORY-template-lifecycle

**Files:** `test/scenarios/template_lifecycle_e2e_test.go` (new)

**Story:** STORY-template-lifecycle
**Proof form (from spec):** executable proof exercising the full lifecycle against the assembled all-in-one stack.

**Steps:**
1. Read STORY-template-lifecycle in the spec for the full Acceptance contract.
2. Create the scenario test using the existing test harness (`test/support/scenario/harness.go`). The shape follows the pattern of `test/scenarios/lifecycle_force_terminate_fullstack_test.go` (boot `rimsky-all-in-one:latest` via testcontainers, drive HTTP).
3. The test asserts, in order, that against the running stack: `POST /v1/templates` with a valid spec returns 201 + a hash; `GET /v1/templates/{hash}` returns the persisted template; `POST /v1/templates/validate` returns findings without persisting (a follow-up `GET /v1/templates` shows no new row); `POST /v1/templates/{hash}/deploy` flips deploy state and a subsequent `POST /v1/instances` against the hash returns 201; `POST /v1/templates/{hash}/undeploy` reverses the state and a subsequent `POST /v1/instances` returns 4xx; `DELETE /v1/templates/{hash}` returns 409 while the prior instance still references it and 204 after the instance is removed.
4. Use real templates with the canonical naming (`project-alpha`, etc. per `decision:project-agnostic`).
5. Run `go test ./test/scenarios/ -run TestTemplateLifecycle -count=1` and confirm green. Docker must be running.

---

## Pass 5: STORY-instance-lifecycle (acceptance pass — STORY-instance-lifecycle)

**Goal:** Deliver `STORY-instance-lifecycle` end-to-end. The spec cites an existing artifact (`test/scenarios/lifecycle_force_terminate_fullstack_test.go`) covering the force-terminate leg; extend it (or author a sibling file) to cover the create/get/list/pause/resume/delete legs too.
**Scope:** Task 19
**Falsifier:** Pause is recorded but the supervisor keeps dispatching against the instance, OR force-terminate writes a row but doesn't propagate to the in-flight node-run, OR delete succeeds non-terminal.

### Task 19: Extend lifecycle proof to cover the full instance lifecycle

**Files:** `test/scenarios/lifecycle_force_terminate_fullstack_test.go` (existing; extend) OR `test/scenarios/instance_lifecycle_fullstack_test.go` (new sibling)

**Story:** STORY-instance-lifecycle
**Proof form (from spec):** executable proof.

**Steps:**
1. Read STORY-instance-lifecycle in the spec.
2. Decide whether to extend the existing file or author a sibling. The existing file is focused on force-terminate; if extending it would dilute that focus, author `instance_lifecycle_fullstack_test.go` as a sibling that exercises create / list / get / pause / resume / delete-non-terminal-rejected / delete-terminal-succeeds, and leave the force-terminate file as-is (it remains the proof for the terminate leg).
3. The new / extended test boots the all-in-one stack, runs an instance through the full lifecycle, and asserts each transition via the real `/v1/...` routes + the supervisor's observable state (event log, instance row).
4. **Load-bearing property:** pause must be observable as supervisor stopping new dispatches against the instance — assert by observing `GET /v1/events?instance_id=...&kind=...` shows no new dispatch events during the pause window. The cheaper shape (asserting only that the pause flag was written) is not the acceptance.
5. Run `go test ./test/scenarios/ -run TestInstanceLifecycle -count=1` and confirm green.

---

## Pass 6: STORY-tag-management (acceptance pass — STORY-tag-management)

**Goal:** Deliver `STORY-tag-management` end-to-end.
**Scope:** Task 20
**Falsifier:** Tag rebind isn't picked up by subsequent instance creation (resolves to the prior hash), OR tag deletion leaves the name still resolving.

### Task 20: Author the executable proof for STORY-tag-management

**Files:** `test/scenarios/tag_management_e2e_test.go` (new)

**Story:** STORY-tag-management
**Proof form (from spec):** executable proof.

**Steps:**
1. Read STORY-tag-management in the spec.
2. Author the scenario test against the all-in-one stack: register two distinct templates (call them `T_alpha`, `T_beta`); `POST /v1/tags` binding a tag to `T_alpha`; `POST /v1/instances` against the tag, assert the resulting instance references `T_alpha`'s hash; `PUT /v1/tags/{tag}` rebinding to `T_beta`; assert a fresh `POST /v1/instances` against the tag now references `T_beta`'s hash AND that the earlier instance still references `T_alpha`'s hash (the rebind didn't migrate existing instances); `DELETE /v1/tags/{tag}`; assert subsequent `POST /v1/instances` against the (now-deleted) tag is refused.
3. Run `go test ./test/scenarios/ -run TestTagManagement -count=1` and confirm green.

---

## Pass 7: STORY-node-admin (acceptance pass — STORY-node-admin)

**Goal:** Deliver `STORY-node-admin`. The spec cites `test/scenarios/cascade_operator_frame_in_e2e_test.go` covering the in-cascade invalidate leg; extend or author a sibling for the get + reset legs.
**Scope:** Task 21
**Falsifier:** Invalidate flips state but the supervisor never picks the node up, OR the in-cascade option produces a separate frame rather than joining the running one, OR reset clears the visible counter but the supervisor still treats the node as exhausted.

### Task 21: Author / extend executable proof for STORY-node-admin

**Files:** `test/scenarios/cascade_operator_frame_in_e2e_test.go` (existing; extend) OR `test/scenarios/node_admin_e2e_test.go` (new sibling)

**Steps:**
1. Read STORY-node-admin in the spec.
2. If extending the existing file: add test cases for `GET /v1/nodes/{id}` and `POST /v1/nodes/{id}/reset`. If authoring a sibling: cover get / freshly-enqueued-frame invalidate / reset legs (the in-cascade leg stays in the existing file).
3. For `reset`: drive a node to a failed terminal with error count > 0; call reset; assert the next dispatch is not skipped due to error-budget exhaustion AND the error counter is cleared (observable via `GET /v1/nodes/{id}`).
4. Run `go test ./test/scenarios/ -run 'TestNodeAdmin|TestCascadeOperatorFrameIn' -count=1` and confirm green.

---

## Pass 8: STORY-message-bus (acceptance pass — STORY-message-bus)

**Goal:** Deliver `STORY-message-bus`. The spec cites `lib/control/controlapi/idempotency_matrix_test.go` and `lib/control/controlapi/idempotency_sender_subject_test.go` as qualifying proof.
**Scope:** Task 22
**Falsifier:** A second emission with the same key produces a second envelope, OR the no-key request is silently accepted, OR a publisher named the same as an operator-sender replays the operator's emit.

### Task 22: Re-verify the existing idempotency matrix tests post-foundational-passes

**Files:** `lib/control/controlapi/idempotency_matrix_test.go`, `lib/control/controlapi/idempotency_sender_subject_test.go` (existing; verify still green after URL prefix sweep)

**Story:** STORY-message-bus
**Proof form (from spec):** executable proof — the per-status matrix.

**Steps:**
1. Re-run `go test ./lib/control/controlapi/ -run 'TestIdempotency' -count=1` after Pass 1 (URL prefix sweep updated the paths in these tests).
2. Confirm both tests pass and assert the full Acceptance: 400 without header, 201 first, 200 replay with original `message_id`, distinct senders never collide.
3. If any sub-test of the matrix doesn't exercise a clause of the spec's Acceptance, extend it (e.g., if there's no test for `operator + publisher named "operator"` distinct-sender bucketing, add one).

---

## Pass 9: STORY-event-log-read (acceptance pass — STORY-event-log-read)

**Goal:** Deliver `STORY-event-log-read`. The spec cites `lib/services/test/scenarios/cli_watch_chronological_e2e_test.go` (chronological-ordering proof) and `test/scenarios/breakpoints/hit_emits_event_test.go` (breakpoint-on-feed proof).
**Scope:** Task 23
**Falsifier:** Events are returned source-grouped rather than timestamp-ordered, OR a breakpoint hit that actually occurred between two events appears outside the window.

### Task 23: Re-verify existing event-log-read tests post-foundational-passes

**Files:** `lib/services/test/scenarios/cli_watch_chronological_e2e_test.go`, `test/scenarios/breakpoints/hit_emits_event_test.go` (both existing; verify green)

**Steps:**
1. Re-run both tests after Pass 1 (URL sweep) and Pass 2 (event-kind enum) — the `rimsky watch` CLI and event-log read filter both touched.
2. Confirm both pass.
3. If the chronological test only exercises one interleaving (event ↔ breakpoint), extend it to also exercise event ↔ message and event ↔ state_transition interleavings, since the spec's Acceptance says "node lifecycle transitions, breakpoint hits, message activity, and supervisor decisions" all appear in true chronological order.

---

## Pass 10: STORY-audit-log-read (acceptance pass — STORY-audit-log-read)

**Goal:** Deliver `STORY-audit-log-read`. The spec cites `test/scenarios/auth/audit_read_test.go`.
**Scope:** Task 24
**Falsifier:** A real access denied doesn't appear in the audit, OR dry-run-mode attempts are absent, OR actor identity is dropped from the record.

### Task 24: Re-verify and extend STORY-audit-log-read proof

**Files:** `test/scenarios/auth/audit_read_test.go` (existing; verify + extend if gaps)

**Steps:**
1. Re-run the test after the URL sweep.
2. Confirm the spec's Acceptance clauses all hold: actor identity / action name / outcome / resource target / dry-run-mode attempts / denied attempts all in the audit. If any clause is uncovered, extend the test.
3. Run `go test ./test/scenarios/auth -run TestAuditRead -count=1` green.

---

## Pass 11: STORY-breakpoint-debugger (acceptance pass — STORY-breakpoint-debugger)

**Goal:** Deliver `STORY-breakpoint-debugger`. The spec cites `test/scenarios/breakpoints/hit_emits_event_test.go` for the unified-feed leg; extend or sibling for install / list / delete / resume.
**Scope:** Task 25
**Falsifier:** Hit appears on one surface but not the other (not co-transactional), OR resume's overlay isn't applied at the next dispatch, OR breakpoint deletion leaves orphaned hits.

### Task 25: Extend STORY-breakpoint-debugger proof

**Files:** `test/scenarios/breakpoints/hit_emits_event_test.go` (existing) OR `test/scenarios/breakpoints/debugger_lifecycle_e2e_test.go` (new sibling)

**Steps:**
1. Read STORY-breakpoint-debugger in the spec.
2. Extend / sibling to cover: install via `POST /v1/instances/{id}/breakpoints`; list via `GET /v1/instances/{id}/breakpoints`; observe hit on `/v1/events` AND `GET /v1/instances/{id}/breakpoint-hits` co-transactionally; resume with overlay via `POST /v1/instances/{id}/breakpoints/{bp}/resume` carrying overlay attributes, assert the next dispatch's attribute bag carries the overlay (observable via `GET /v1/nodes/{id}` latest-attribute surface); delete via `DELETE /v1/instances/{id}/breakpoints/{bp}` cascade-clears hits.
3. **Load-bearing co-transactional property:** the spec's STORY-observability-breakpoint-hit-event (shipped) tested this; the consolidated story extends with surrounding flow. Do not weaken the co-transactional assertion — both surfaces must reflect the hit in the same transaction, not "eventually." The verification: run with `-race -count=3` to surface any non-transactional racing.
4. Run `go test ./test/scenarios/breakpoints/... -race -count=3` and confirm green.

---

## Pass 12: STORY-asset-management (acceptance pass — STORY-asset-management)

**Goal:** Deliver `STORY-asset-management` end-to-end.
**Scope:** Task 26
**Falsifier:** Materialize trigger doesn't actually cause a producing dispatch, OR the version-history surface returns rows that don't match what really materialized.

### Task 26: Author the executable proof for STORY-asset-management

**Files:** `test/scenarios/asset_management_e2e_test.go` (new)

**Steps:**
1. Read STORY-asset-management in the spec.
2. Author against a template that declares durable claims against a data-processing-capable producer (per `concept:asset`).
3. Drive: list assets via `GET /v1/instances/{id}/assets`; retrieve a single asset; materialize via `POST /v1/instances/{id}/assets/{alias}/materialize`; observe a real producing dispatch via event log; assert a new version row in `GET /v1/instances/{id}/assets/{alias}/versions`; read materialization-history; delete via `DELETE /v1/instances/{id}/assets/{alias}`.
4. **Load-bearing property:** the materialize trigger must produce a real producing dispatch (observable via `GET /v1/events?kind=work_started&node=<producing-node>`); the cheaper shape (writing a new version row without dispatching) is the falsifier.
5. Run `go test ./test/scenarios/ -run TestAssetManagement -count=1` and confirm green.

---

## Pass 13: STORY-backfill-ops (acceptance pass — STORY-backfill-ops)

**Goal:** Deliver `STORY-backfill-ops`. The spec cites the force-terminate-fullstack test as partial coverage of the override leg.
**Scope:** Task 27
**Falsifier:** Override silently dropped (supervisor uses template default), OR cancel is recorded but in-flight partitions keep running, OR the per-partition progress lies about what dispatched.

### Task 27: Author / extend STORY-backfill-ops proof

**Files:** `test/scenarios/backfill_partition_override_fullstack_test.go` (existing) OR `test/scenarios/backfill_ops_lifecycle_e2e_test.go` (new sibling)

**Steps:**
1. Read STORY-backfill-ops in the spec.
2. The existing file covers the partition-selector-override leg. Either extend or author a sibling for list / get / partition-progress / cancel legs.
3. For cancel: start a backfill, observe in-flight partitions in `GET /v1/backfills/{op}/partitions`; `POST /v1/backfills/{op}/cancel`; assert in-flight partitions transition to `cancelled` through the real supervisor cancel path (not by direct row write).
4. Run `go test ./test/scenarios/ -run 'TestBackfill' -count=1` and confirm green.

---

## Pass 14: STORY-lineage-exploration (acceptance pass — STORY-lineage-exploration)

**Goal:** Deliver `STORY-lineage-exploration`.
**Scope:** Task 28
**Falsifier:** A real upstream producer is missing from the ancestor walk, OR a real downstream consumer is missing from the descendant walk.

### Task 28: Author the executable proof for STORY-lineage-exploration

**Files:** `test/scenarios/lineage_exploration_e2e_test.go` (new)

**Steps:**
1. Read STORY-lineage-exploration in the spec.
2. Drive a template producing a real lineage graph: a producer node + a consumer node consuming the producer's claim. After the instance reaches terminal, query lineage via `GET /v1/lineage/runs/{run_id}` + `/ancestors` + `/descendants`. Assert the ancestor walk includes the upstream producer and the descendant walk includes the downstream consumer.
3. Additionally exercise the `GET /v1/lineage/claims/{claim_handle_id}`, `GET /v1/lineage/by-source/{src_type}/{src_id}`, `GET /v1/lineage/by-producer/{executor_name}` pivots.
4. Run `go test ./test/scenarios/ -run TestLineageExploration -count=1` and confirm green.

---

## Pass 15: STORY-lineage-admin (acceptance pass — STORY-lineage-admin)

**Goal:** Deliver `STORY-lineage-admin`.
**Scope:** Task 29
**Falsifier:** Prune removes records at the cutoff boundary, OR removes records newer than cutoff, OR silently drops the cutoff and returns a no-op count.

### Task 29: Author the executable proof for STORY-lineage-admin

**Files:** `test/scenarios/lineage_admin_prune_e2e_test.go` (new)

**Steps:**
1. Read STORY-lineage-admin in the spec.
2. Seed lineage rows with varied ages (e.g., insert rows with `occurred_at = now() - interval '7 days'`, `now() - interval '1 hour'`, etc., via the harness's test-DB access).
3. `POST /v1/admin/lineage/prune` with a 24h cutoff; assert (via follow-up `GET /v1/lineage/...`) that rows older than 24h are gone and rows at/newer than 24h remain. Assert the response body reports the deleted row count.
4. Run `go test ./test/scenarios/ -run TestLineageAdmin -count=1` and confirm green.

---

## Pass 16: STORY-api-key-management (acceptance pass — STORY-api-key-management)

**Goal:** Deliver `STORY-api-key-management`. The spec cites `test/scenarios/auth/lifecycle_test.go`.
**Scope:** Task 30
**Falsifier:** Revoke leaves the old plaintext still accepted, OR rotate's grace window collapses to zero (old key dies immediately) or never expires, OR auth-init succeeds when the keys table is non-empty.

### Task 30: Re-verify and extend STORY-api-key-management proof

**Files:** `test/scenarios/auth/lifecycle_test.go` (existing; verify + extend)

**Steps:**
1. Read STORY-api-key-management in the spec.
2. Re-run after URL sweep. Confirm Acceptance: bootstrap admin via `rimsky auth init` returning plaintext exactly once; mint scoped keys; list/get without plaintext; revoke makes the plaintext stop working; rotate produces new plaintext with old still accepted during grace; auth-status surface accurate.
3. If any Acceptance clause is uncovered, extend.
4. **Load-bearing property:** rotate's grace window is observable — old plaintext works inside the window, fails after. Use a configured short grace (a few seconds) and wait it out in the test (or use the harness's time-fastforward if available).
5. Run `go test ./test/scenarios/auth -run TestLifecycle -count=1` and confirm green.

---

## Pass 17: STORY-runtime-diagnostics (acceptance pass — STORY-runtime-diagnostics)

**Goal:** Deliver `STORY-runtime-diagnostics`. The spec cites `test/scenarios/parked_lifecycle_test.go` for the parked-node leg.
**Scope:** Task 31
**Falsifier:** A parked node that's really parked isn't on the parked surface, OR a wait-set edge the supervisor is consulting is missing from the wait-set surface.

### Task 31: Extend STORY-runtime-diagnostics proof

**Files:** `test/scenarios/parked_lifecycle_test.go` (existing; verify) AND `test/scenarios/runtime_diagnostics_e2e_test.go` (new sibling for wait-sets + held-frames + claim-holders)

**Steps:**
1. Read STORY-runtime-diagnostics in the spec.
2. Drive an instance whose nodes park, gate on senders in the wait-set, and hold a claim. Query each diagnostic surface via `GET /v1/diagnostics/parked`, `GET /v1/admin/diagnostics/wait-sets`, `GET /v1/admin/diagnostics/held-frames`, `GET /v1/lock-holders/{claim_handle_id}/claim-holders`. Assert the supervisor's actual state is reflected.
3. Run `go test ./test/scenarios/ -run 'TestParkedLifecycle|TestRuntimeDiagnostics' -count=1` and confirm green.

---

## Pass 18: STORY-client-context (acceptance pass — STORY-client-context)

**Goal:** Deliver `STORY-client-context`.
**Scope:** Task 32
**Falsifier:** Switched context isn't picked up by the next command, OR removed context still resolves.

### Task 32: Author the demo proof for STORY-client-context

**Files:** `examples/client-context-demo.sh` (new); `cmd/rimsky/cli/ctx_demo_test.go` (new — driver test that runs the demo script against two locally-spawned control-API endpoints)

**Story:** STORY-client-context
**Proof form (from spec):** demo — a runnable script walking through register / switch / use / remove, with two real local control-api endpoints to make the switch observable.

**Steps:**
1. Author `examples/client-context-demo.sh`: spawn two `rimsky-all-in-one` containers on different ports (in the demo, document the assumed-already-running state); `rimsky ctx add staging --endpoint <url1>`; `rimsky ctx add prod --endpoint <url2>`; `rimsky ctx use staging`; `rimsky ls instances` (observable as hitting staging); `rimsky ctx use prod`; `rimsky ls instances` (observable as hitting prod); `rimsky ctx current`; `rimsky ctx rm staging`.
2. Author a driver Go test under `cmd/rimsky/cli/ctx_demo_test.go` that boots two containers via testcontainers, runs the demo script as a subprocess, and asserts the script's exit code is 0 and the per-step output matches the expected pattern.
3. Run `go test ./cmd/rimsky/cli/ -run TestCtxDemo -count=1` and confirm green.

---

## Pass 19: STORY-operator-onboarding (acceptance pass — STORY-operator-onboarding)

**Goal:** Deliver `STORY-operator-onboarding`.
**Scope:** Task 33
**Falsifier:** The shipped example isn't a real runnable templatespec (would need modification to run), OR `rimsky run` is a stub that prints a fake ID without driving register + deploy + instantiate.

### Task 33: Author the onboarding demo

**Files:** `examples/onboarding-demo.sh` (new); `examples/onboarding-template.yaml` (new — the shipped runnable template the demo points at); `lib/services/test/scenarios/onboarding_demo_e2e_test.go` (new driver test)

**Steps:**
1. Author a minimal-but-real template (`examples/onboarding-template.yaml`) referencing an executor that does real work (e.g., `http-node` against a public HTTP fixture, or `verifier-shape-checks` against an inline dataset). It must run end-to-end against the all-in-one stack without modification.
2. Author the demo script that runs `rimsky run examples/onboarding-template.yaml`, asserts a printed instance ID, exits 0, and watches the instance terminal via `rimsky watch <id>`.
3. Author the driver test (testcontainers + subprocess) that runs the demo script and asserts exit code 0 + instance reaches terminal.
4. Update the README to reference the demo per the spec's Acceptance ("the README's documented `rimsky run` invocation succeeds as written").
5. Run `go test ./lib/services/test/scenarios/ -run TestOnboardingDemo -count=1` and confirm green.

---

## Pass 20: STORY-compose-lifecycle (acceptance pass — STORY-compose-lifecycle)

**Goal:** Deliver `STORY-compose-lifecycle`. The spec cites `lib/services/test/scenarios/cli_compose_up_down_e2e_test.go`.
**Scope:** Task 34
**Falsifier:** Any compose verb returns without performing its reconcile, OR `compose down` touches resources outside the project namespace, OR a compose verb shells out to docker/kubectl.

### Task 34: Re-verify STORY-compose-lifecycle proof

**Files:** `lib/services/test/scenarios/cli_compose_up_down_e2e_test.go` (existing; verify + extend if gaps)

**Steps:**
1. Read STORY-compose-lifecycle in the spec.
2. Re-run post-URL-sweep.
3. The spec's Acceptance covers `up`, `down`, `plan`, `status`. Ensure all four are exercised by the existing test (or a sibling). Extend if any is missing.
4. Confirm no compose verb shells out to docker/kubectl (search the source: `rg 'docker|kubectl' cmd/rimsky/cli/compose/`); the test asserts behavior, not source absence, but a source check helps prevent regression.
5. Run `go test ./lib/services/test/scenarios/ -run TestCliComposeUpDown -count=1` and confirm green.

---

## Pass 21: STORY-compose-namespace-guard (acceptance pass — STORY-compose-namespace-guard)

**Goal:** Deliver `STORY-compose-namespace-guard`. Existing artifact qualifies.
**Scope:** Task 35
**Falsifier:** A non-compose caller holding `tag:create` or `instance:create` succeeds at creating a `compose:`-prefixed resource (the guard is client-side only, not server-enforced), OR the compose machinery is also refused.

### Task 35: Re-verify STORY-compose-namespace-guard proof

**Files:** `lib/services/test/scenarios/control_api_compose_prefix_guard_e2e_test.go` (existing; verify)

**Steps:**
1. Re-run post-URL-sweep.
2. Confirm green.

---

## Pass 22: STORY-mcp-transport (acceptance pass — STORY-mcp-transport)

**Goal:** Deliver `STORY-mcp-transport`.
**Scope:** Task 36
**Falsifier:** An MCP tool gate is weaker than the equivalent HTTP route's gate (bypasses auth), OR an MCP tool returns a canned response without invoking the real handler.

### Task 36: Author STORY-mcp-transport proof

**Files:** `lib/services/test/scenarios/mcp_transport_parity_e2e_test.go` (new)

**Steps:**
1. Read STORY-mcp-transport in the spec.
2. The test boots the stack and stands up an in-test MCP client over the stack's `/v1/mcp` JSON-RPC surface (no shared MCP client package exists in-tree — author the minimal client inline in the test file). The client discovers the tool catalog, then for each tool category (template, tag, instance, node, message, event, audit, breakpoint, asset, backfill, lineage, diagnostics, auth) invokes representative read + mutation tools and asserts: (a) the auth gate fires identically (mint a key without permission, attempt the tool, assert deny matches the HTTP-route deny); (b) the response shape matches what the HTTP route would return; (c) the observable state results match.
3. The pass focuses on parity, not coverage of every tool — sample one read + one mutation per category to keep the test bounded.
4. Run `go test ./lib/services/test/scenarios/ -run TestMcpTransportParity -count=1` and confirm green.

---

## Pass 23: STORY-anonymous-mode-bootstrap (acceptance pass — STORY-anonymous-mode-bootstrap)

**Goal:** Deliver `STORY-anonymous-mode-bootstrap`.
**Scope:** Task 37
**Falsifier:** Anonymous mode stays open after a key is minted (server still accepts unauthenticated requests), OR `rimsky auth init` succeeds on a deployment that already has keys, OR the status surface lies about which mode is active.

### Task 37: Author STORY-anonymous-mode-bootstrap proof

**Files:** `test/scenarios/auth/anonymous_mode_bootstrap_e2e_test.go` (new)

**Steps:**
1. Read STORY-anonymous-mode-bootstrap in the spec.
2. Boot a fresh all-in-one container with an empty keys table. Issue requests without a bearer; assert they succeed. Run `rimsky auth init`; assert plaintext returned exactly once; assert subsequent unauthenticated requests are refused. Confirm `rimsky auth init` against the same container refuses (keys table non-empty). Query `GET /v1/auth/status` throughout and assert the auth-mode field correctly transitions.
3. Run `go test ./test/scenarios/auth -run TestAnonymousModeBootstrap -count=1` and confirm green.

---

## Pass 24: STORY-dry-run-request-flag (acceptance pass — STORY-dry-run-request-flag)

**Goal:** Deliver `STORY-dry-run-request-flag`. The spec cites `test/scenarios/auth/dry_run_test.go`.
**Scope:** Task 38
**Falsifier:** A dry-run write persists state, OR returns a canned envelope unrelated to the inputs (validation didn't actually run), OR a read returns the dry-run marker.

### Task 38: Re-verify STORY-dry-run-request-flag proof

**Files:** `test/scenarios/auth/dry_run_test.go` (existing; verify + extend if gaps)

**Steps:**
1. Re-run post-URL-sweep. Confirm green.
2. If the test doesn't exercise validation-actually-runs (sending a spec that should fail validation in dry-run and asserting the failure surfaces), extend.

---

## Pass 25: STORY-dry-run-mode-floor (acceptance pass — STORY-dry-run-mode-floor)

**Goal:** Deliver `STORY-dry-run-mode-floor`. The spec cites `test/scenarios/auth/dry_run_identity_bound_test.go`.
**Scope:** Task 39
**Falsifier:** A dry-run-pinned key's write actually persists state, OR the audit misses the attempt, OR no comparison shows the floor is identity-bound.

### Task 39: Re-verify STORY-dry-run-mode-floor proof

**Files:** `test/scenarios/auth/dry_run_identity_bound_test.go` (existing; verify)

**Steps:**
1. Re-run post-URL-sweep. Confirm green.

---

## Pass 26: STORY-grant-scope-enforcement (acceptance pass — STORY-grant-scope-enforcement)

**Goal:** Deliver `STORY-grant-scope-enforcement`. The spec cites `test/scenarios/auth/grant_scope_lifecycle_test.go`.
**Scope:** Task 40
**Falsifier:** An out-of-scope request succeeds, OR a same-action operation later in the lifecycle silently bypasses the scope check.

### Task 40: Re-verify STORY-grant-scope-enforcement proof

**Files:** `test/scenarios/auth/grant_scope_lifecycle_test.go` (existing; verify)

**Steps:**
1. Re-run post-URL-sweep. Confirm green.

---

## Pass 27: STORY-forensic-last-attribute (acceptance pass — STORY-forensic-last-attribute)

**Goal:** Deliver `STORY-forensic-last-attribute`. The spec cites `test/scenarios/observability_latest_attribute_fullstack_test.go`.
**Scope:** Task 41
**Falsifier:** The latest-attribute surface returns an earlier run's bag (stale), OR returns synthesized values, OR is absent on a node that has executed.

### Task 41: Re-verify STORY-forensic-last-attribute proof

**Files:** `test/scenarios/observability_latest_attribute_fullstack_test.go` (existing; verify)

**Steps:**
1. Re-run post-URL-sweep. Confirm green.

---

## Pass 28: STORY-rules-doc-accuracy (acceptance pass — STORY-rules-doc-accuracy)

**Goal:** Deliver `STORY-rules-doc-accuracy`. The spec cites `tools/rulesdoc/rulesdoc_test.go`.
**Scope:** Task 42
**Falsifier:** The check accepts a non-existent path (text-search only, no resolve), OR the check is informational and doesn't fail CI.

### Task 42: Re-verify STORY-rules-doc-accuracy proof

**Files:** `tools/rulesdoc/rulesdoc_test.go` (existing; verify)

**Steps:**
1. Re-run after foundational passes. Confirm green.
2. Verify the test is wired into `make lint` or `go test ./...` so a regression fails CI (per the spec's Falsifier).

---

## Pass 29: STORY-claim-scope-substitution (acceptance pass — STORY-claim-scope-substitution)

**Goal:** Deliver `STORY-claim-scope-substitution`. The spec cites `test/scenarios/stores/claim_scope_directive_e2e_test.go`.
**Scope:** Task 43
**Falsifier:** Legacy `scope` spelling is silently accepted, OR canonical `claim_scope` resolves to empty / wrong bytes at dispatch.

### Task 43: Re-verify STORY-claim-scope-substitution proof

**Files:** `test/scenarios/stores/claim_scope_directive_e2e_test.go` (existing; verify)

**Steps:**
1. Re-run after foundational passes. Confirm green.

---

## Pass 30: STORY-substitution-doc-accuracy (acceptance pass — STORY-substitution-doc-accuracy)

**Goal:** Deliver `STORY-substitution-doc-accuracy`. The spec cites `lib/graph/attribute/substitution_test.go`.
**Scope:** Task 44
**Falsifier:** The check is informational only (doesn't fail CI), OR text-matches the doc without ASTs over the resolver code.

### Task 44: Re-verify STORY-substitution-doc-accuracy proof

**Files:** `lib/graph/attribute/substitution_test.go` (existing; verify the `headerBulletPattern` accuracy gate is wired into `go test`)

**Steps:**
1. Run `go test ./lib/graph/attribute -run TestSubstitution -count=1` (or whichever test func contains the accuracy gate). Confirm green.

---

## Pass 31: STORY-ref-validation-mode (acceptance pass — STORY-ref-validation-mode)

**Goal:** Deliver `STORY-ref-validation-mode`. The spec cites `test/scenarios/attributes/ref_validation_mode_e2e_test.go`.
**Scope:** Task 45
**Falsifier:** Any mode's stated behavior isn't realized, OR the implicit always-on soft-fail heuristic is still present alongside the explicit modes.

### Task 45: Re-verify STORY-ref-validation-mode proof

**Files:** `test/scenarios/attributes/ref_validation_mode_e2e_test.go` (existing; verify)

**Steps:**
1. Re-run after foundational passes. Confirm green.

---

## Pass 32: STORY-mandatory-instantiation-gate (acceptance pass — STORY-mandatory-instantiation-gate)

**Goal:** Deliver `STORY-mandatory-instantiation-gate`. The spec cites `test/scenarios/attributes/instantiation_static_config_gate_e2e_test.go`.
**Scope:** Task 46
**Falsifier:** Value-constraint violation slips through, OR the rejection cites only a shape error rather than the value-constraint violation.

### Task 46: Re-verify STORY-mandatory-instantiation-gate proof

**Files:** `test/scenarios/attributes/instantiation_static_config_gate_e2e_test.go` (existing; verify)

**Steps:**
1. Re-run after foundational passes. Confirm green.

---

## Pass 33: STORY-lenient-marker (acceptance pass — STORY-lenient-marker)

**Goal:** Deliver `STORY-lenient-marker`. The spec cites `test/scenarios/attributes/lenient_marker_recovery_test.go`.
**Scope:** Task 47
**Falsifier:** The `?` marker is silently treated like no-marker (lenient dispatch fails when source absent), OR no-marker is silently treated like `?`.

### Task 47: Re-verify STORY-lenient-marker proof

**Files:** `test/scenarios/attributes/lenient_marker_recovery_test.go` (existing; verify)

**Steps:**
1. Re-run after foundational passes. Confirm green.

---

## Pass 34: STORY-verifier-severity-partition (acceptance pass — STORY-verifier-severity-partition)

**Goal:** Deliver `STORY-verifier-severity-partition`. The spec cites `lib/services/executors/verifier-shape-checks/server_test.go` + `validation_test.go` for in-process; the cross-stack leg needs a scenario test.
**Scope:** Task 48
**Falsifier:** Warning blocks commit, OR error doesn't block commit, OR the severity field is declared but unused.

### Task 48: Author cross-stack STORY-verifier-severity-partition proof

**Files:** `test/scenarios/verifier_severity_partition_e2e_test.go` (new)

**Steps:**
1. Author against the all-in-one stack with the verifier-shape-checks bundled service. Drive a template whose verifier node carries one `severity: warning` failing check + one `severity: error` passing check against an in-bounds dataset; assert terminal success + warning recorded. Then drive a second dispatch against an out-of-bounds dataset; assert terminal error + commit blocked.
2. **Note from the spec:** the runtime treats severity as `warning` vs non-`warning` (any non-`warning` string is treated as blocking — see `tension:quality-rule-severity-string-footgun`). The test exercises only the two known-good values; the typo footgun is out of scope here.
3. Run `go test ./test/scenarios/ -run TestVerifierSeverityPartition -count=1` and confirm green.

---

## Pass 35: STORY-template-fan-out (acceptance pass — STORY-template-fan-out)

**Goal:** Deliver `STORY-template-fan-out`.
**Scope:** Task 49
**Falsifier:** Sub-claims are materialized but not dispatched concurrently (serialized), OR the parent settles before all sub-claims resolve, OR aggregate outcome doesn't reflect the sub-claim resolutions.

### Task 49: Author STORY-template-fan-out proof

**Files:** `test/scenarios/template_fan_out_e2e_test.go` (new)

**Steps:**
1. Author against the all-in-one stack. Use a claim-producer that supports `SplitScope` and returns N sub-scopes (the bundled postgres or filesystem store). Drive a template with a fan-out node; assert: (a) the supervisor materializes N sub-claim rows; (b) N node-runs dispatch concurrently (observable as overlapping `work_started` events); (c) parent settles once all sub-runs reach terminal; (d) aggregate outcome reflects sub-claim resolutions (e.g., one sub-claim Abandon propagates to a parent Error).
2. Run `go test ./test/scenarios/ -run TestTemplateFanOut -count=1` and confirm green.

---

## Pass 36: STORY-template-sub-graph-delegation (acceptance pass — STORY-template-sub-graph-delegation)

**Goal:** Deliver `STORY-template-sub-graph-delegation`.
**Scope:** Task 50
**Falsifier:** The delegate node settles before the sub-graph does, OR the sub-graph's terminal outcome doesn't propagate to the parent.

### Task 50: Author STORY-template-sub-graph-delegation proof

**Files:** `test/scenarios/template_sub_graph_delegation_e2e_test.go` (new)

**Steps:**
1. Author a parent template with a node carrying `delegate: <graph-name>`; a separate template providing the named sub-graph (with entry/exit nodes). Drive an instance; assert the delegate node settles only after the sub-graph settles, and the sub-graph's terminal outcome (success/error) propagates to the parent.
2. Run `go test ./test/scenarios/ -run TestTemplateSubGraphDelegation -count=1` and confirm green.

---

## Pass 37: STORY-template-error-policy (acceptance pass — STORY-template-error-policy)

**Goal:** Deliver `STORY-template-error-policy`.
**Scope:** Task 51
**Falsifier:** Any of the four actions has no observable effect (the runtime acts the same regardless of the declared action), OR an action's effect doesn't match the declaration.

### Task 51: Author STORY-template-error-policy proof

**Files:** `test/scenarios/template_error_policy_e2e_test.go` (new)

**Steps:**
1. Author four templates (or one template with parameterized error classes), each mapping a specific error class to one of the four actions (`pass`, `give_up`, `retry`, `discard_claims_then_retry`). Drive each through an instance where the executor errors with the mapped class. Assert each action's observable effect: `pass` continues the cascade; `give_up` terminates the node and skips downstream; `retry` re-dispatches; `discard_claims_then_retry` releases held claims before re-dispatch (observable via `GET /v1/lock-holders/...`).
2. Run `go test ./test/scenarios/ -run TestTemplateErrorPolicy -count=1` and confirm green.

---

## Pass 38: STORY-template-subscriptions (acceptance pass — STORY-template-subscriptions)

**Goal:** Deliver `STORY-template-subscriptions`.
**Scope:** Task 52
**Falsifier:** Subscription fires the node on a non-matching payload (predicate ignored), OR doesn't fire on a matching one, OR trailing-`*` doesn't match its prefix.

### Task 52: Author STORY-template-subscriptions proof

**Files:** `test/scenarios/template_subscriptions_cel_e2e_test.go` (new)

**Steps:**
1. Author a template with a `subscribes:` entry that uses a CEL predicate (`payload.tenant == "alpha"`). Drive an instance; emit a signal whose payload matches the predicate; assert the subscribed node fires. Emit a non-matching payload; assert no fire. Re-test with a trailing-`*` prefix; assert it matches every type-path with that prefix.
2. Run `go test ./test/scenarios/ -run TestTemplateSubscriptions -count=1` and confirm green.

---

## Pass 39: STORY-executor-protocol (acceptance pass — STORY-executor-protocol)

**Goal:** Deliver `STORY-executor-protocol`. The spec cites `examples/executor/executor_test.go` for in-process partial coverage; needs cross-stack rimsky-dispatch end-to-end.
**Scope:** Task 53
**Falsifier:** A registered executor advertising a declared error class emits it but the policy router treats it as generic, OR an event the executor emits doesn't appear on the event log, OR attributes resolved against the executor's schema bypass the schema validation.

### Task 53: Author cross-stack STORY-executor-protocol proof

**Files:** `examples/executor/README.md` (new — extend with cross-stack walkthrough); `examples/executor/main_e2e_test.go` (new — drives the example against a running rimsky stack via testcontainers)

**Story:** STORY-executor-protocol
**Proof form (from spec):** example — `examples/executor/` extended with a worked walkthrough that boots a running rimsky and exhibits each protocol surface end-to-end.

**Steps:**
1. Extend `examples/executor/main.go` (if needed) so the executor advertises a declared error class and emits a named event.
2. Author `examples/executor/main_e2e_test.go`: boot `rimsky-all-in-one`; register the executor as a service (via the host-agent late-bind mechanism); deploy a template referencing it; drive an instance; assert (a) the executor receives Execute, (b) named events appear on `/v1/events`, (c) declared error class routes through `error_types`, (d) attribute schema validation rejects misshapen attributes at registration.
3. Update `examples/executor/README.md` to describe the walkthrough.
4. Run `go test ./examples/executor -run TestE2E -count=1` and confirm green.

---

## Pass 40: STORY-executor-trace-observability (acceptance pass — STORY-executor-trace-observability)

**Goal:** Deliver `STORY-executor-trace-observability`.
**Scope:** Task 54
**Falsifier:** Trace stream silently drops events under load, OR trace history returns rows that don't correspond to what the executor actually emitted, OR the trace surface is absent for an executor that advertised trace support.

### Task 54: Author STORY-executor-trace-observability proof

**Files:** `test/scenarios/executor_trace_observability_e2e_test.go` (new)

**Steps:**
1. Author against an executor that advertises trace support (the bundled `claude-agent` or `http-node` may already; if not, extend the executor example used in Pass 39). Boot the stack with a dispatched node in flight; subscribe to the trace stream via the observability surface (the executor's `StreamTrace` RPC); assert real-time events arrive as the executor emits them. After terminal, query `GetTrace` and assert the full record matches what was streamed.
2. **Load-bearing property:** the test drives the assembled product through the real observability surface, not the executor in-process. The cheaper shape (asserting only that the executor's StreamTrace method returns events) is not the acceptance — the operator-side query is the contract.
3. Run `go test ./test/scenarios/ -run TestExecutorTraceObservability -count=1` and confirm green.

---

## Pass 41: STORY-http-node (acceptance pass — STORY-http-node)

**Goal:** Deliver `STORY-http-node`. The spec cites `lib/services/executors/http-node/server_test.go` for in-process; cross-stack 429 + error-class field needs scenario coverage.
**Scope:** Task 55
**Falsifier:** 429 errors a node-run instead of parking, OR the `resume_at` isn't honored by the supervisor, OR the configured error-class JSON field is ignored.

### Task 55: Author cross-stack STORY-http-node proof

**Files:** `test/scenarios/http_node_e2e_test.go` (new)

**Steps:**
1. Stand up a fake upstream server (`httptest.NewServer`) that returns 200, 429 (with Retry-After), 4xx (with the configured error-class JSON field), and 4xx (without it). Boot the all-in-one stack; deploy a template referencing the bundled `http-node`; drive dispatches against each upstream behavior.
2. Assert: 200 → attributes_delta populated; 429 → node-run `parked` with corresponding `resume_at` + supervisor wakes at that time + 200 on retry → terminal success; 4xx with configured field → typed `http/<class>` terminal error; 4xx without → `_unspecified` leaf.
3. Run `go test ./test/scenarios/ -run TestHttpNode -count=1` and confirm green.

---

## Pass 42: STORY-claude-agent (acceptance pass — STORY-claude-agent)

**Goal:** Deliver `STORY-claude-agent`. The spec cites multiple `lib/services/executors/claude-agent/src/*.test.ts` for in-process; cross-stack with real Claude CLI may be infeasible — fall back to a cross-stack proof against a stubbed CLI that exhibits the protocol shape end-to-end.
**Scope:** Task 56
**Falsifier:** The sign-off accepts a signature over stale output (bound to `null` when output was emitted incrementally), OR `allow_inline=false` is silently accepted alongside an inline server definition, OR a declared error class fires but the policy router treats it as generic, OR an env-var-referenced credential persists in plaintext attributes.

### Task 56: Re-verify + extend STORY-claude-agent proof

**Files:** `lib/services/executors/claude-agent/src/signoff-gate.e2e.test.ts`, `mcp-servers-wiring.test.ts`, `rate-limit.test.ts`, `observability.test.ts`, `agent-run.test.ts`, `lifecycle.e2e.test.ts` (existing; re-verify); `lib/services/test/scenarios/claude_agent_cross_stack_e2e_test.go` (new — cross-stack proof using a fake-CLI runner)

**Steps:**
1. Re-run all existing TS tests after the foundational passes: `cd lib/services/executors/claude-agent && npm test`. Confirm green.
2. The existing TS tests qualify for the executor-side behavior. For the cross-stack leg ("CLI spawned, agent does real work, async-callback returns"), the real Claude CLI is impractical in CI. Author `lib/services/test/scenarios/claude_agent_cross_stack_e2e_test.go` against a stub-CLI that emits the Claude CLI's wire shape (so the claude-agent executor's CLI-runner path is exercised end-to-end, with the rimsky stack on the other side). Assert each Acceptance clause: signoff gate accepts the real-bound-output signature; MCP catalog refuses inline when `allow_inline=false`; declared error classes route via policy; env-var-referenced credentials don't persist in plaintext attributes.
3. Run `go test ./lib/services/test/scenarios/ -run TestClaudeAgentCrossStack -count=1` and confirm green.

---

## Pass 43: STORY-verifier-http (acceptance pass — STORY-verifier-http)

**Goal:** Deliver `STORY-verifier-http`. The spec cites `lib/services/executors/verifier-http/executor_test.go` for in-process; needs cross-stack.
**Scope:** Task 57
**Falsifier:** The verifier resolves to success when the upstream returned 5xx, OR the upstream's class field is dropped, OR the payload posted is canned.

### Task 57: Author cross-stack STORY-verifier-http proof

**Files:** `test/scenarios/verifier_http_e2e_test.go` (new)

**Steps:**
1. Stand up a fake verification service (`httptest.NewServer`) that echoes the inbound claim payload back in its response (so the test can assert the real claim bytes reach the upstream). Boot the all-in-one stack with the verifier-http bundled service. Drive: a 2xx payload → terminal success; a 4xx-with-class payload → terminal error with typed class; assert the upstream received the real claim payload (echo-back verifies).
2. Run `go test ./test/scenarios/ -run TestVerifierHttp -count=1` and confirm green.

---

## Pass 44: STORY-publisher-protocol (acceptance pass — STORY-publisher-protocol)

**Goal:** Deliver `STORY-publisher-protocol`. The spec cites `examples/publisher/publisher_test.go` for in-process + `test/scenarios/sensor/message_routing_test.go` + `lib/services/test/scenarios/sensor_cascade_e2e_test.go` for partial cross-stack; needs `ListSubscriptions` reconcile.
**Scope:** Task 58
**Falsifier:** Subscribe is acknowledged but messages never reach the message endpoint, OR the post-restart reconcile re-subscribes already-active subscriptions, OR the publisher emits without the dedup header and is silently accepted.

### Task 58: Extend STORY-publisher-protocol proof

**Files:** `examples/publisher/main_e2e_test.go` (new — cross-stack against running rimsky); `examples/publisher/README.md` (extend with walkthrough)

**Steps:**
1. Author the cross-stack test: boot rimsky; register the example publisher; observe Subscribe via the example publisher's exposed test hook; emit messages, observe them arrive at the instance; restart rimsky (or simulate via `ResyncPublisherSubscriptions`); assert `ListSubscriptions` is called and the reconcile reflects current state without re-subscribing already-active subscriptions.
2. Run `go test ./examples/publisher -run TestE2E -count=1` and confirm green.

---

## Pass 45: STORY-sensor-cron (acceptance pass — STORY-sensor-cron)

**Goal:** Deliver `STORY-sensor-cron`. The spec cites multiple existing tests for sensor-internal behavior + `lib/services/test/scenarios/sensor_cascade_e2e_test.go` for cascade direction.
**Scope:** Task 59
**Falsifier:** State persists but the binary refuses to honor it on restart, OR two replicas fire only once per window (silent leader election), OR cron advancement uses wall clock instead of the row's prior `next_fire_at`.

### Task 59: Re-verify + extend STORY-sensor-cron proof

**Files:** `lib/services/sensors/sensor-cron/state_db_test.go`, `replica_posture_test.go`, `multi_replica_test.go`, `sensor_test.go` (existing; verify); `lib/services/test/scenarios/sensor_cron_restart_recovery_e2e_test.go` (new — cross-stack restart-recovery proof)

**Steps:**
1. Re-run existing sensor-cron tests after foundational passes. Confirm green.
2. Author the new cross-stack restart-recovery test: boot rimsky + sensor-cron with state DSN pointing at a real Postgres (via testcontainers); register a cron subscription with `next_fire_at` future; stop the sensor; restart; assert it fires at the originally-scheduled window without rimsky re-issuing Subscribe.
3. Run `go test ./lib/services/test/scenarios/ -run TestSensorCronRestartRecovery -count=1` and confirm green.

---

## Pass 46: STORY-sensor-http (acceptance pass — STORY-sensor-http)

**Goal:** Deliver `STORY-sensor-http`.
**Scope:** Task 60
**Falsifier:** Polling skips a window, OR the body filter is declared but unused, OR a process restart drops the polling watermark.

### Task 60: Author cross-stack STORY-sensor-http proof

**Files:** `lib/services/test/scenarios/sensor_http_e2e_test.go` (new)

**Steps:**
1. Stand up a fake upstream (`httptest.NewServer`) returning 200 with a parameterized body. Boot rimsky + sensor-http; register a polling subscription with body filter; advance time to trigger polling; assert messages arrive at the targeted instance only when body matches filter. Restart sensor; assert polling watermark is preserved (no re-polling already-seen windows).
2. Run `go test ./lib/services/test/scenarios/ -run TestSensorHttp -count=1` and confirm green.

---

## Pass 47: STORY-sensor-webhook (acceptance pass — STORY-sensor-webhook)

**Goal:** Deliver `STORY-sensor-webhook`.
**Scope:** Task 61
**Falsifier:** Inbound POST acknowledged before the message is persisted in rimsky, OR the path-prefix filter is declared but unused, OR the request body translation is canned.

### Task 61: Author cross-stack STORY-sensor-webhook proof

**Files:** `lib/services/test/scenarios/sensor_webhook_e2e_test.go` (new)

**Steps:**
1. Boot rimsky + sensor-webhook with a path-prefix subscription. POST inbound to a matching path; assert (a) the inbound is acknowledged only after the message is persisted in rimsky (observe sequence via the response body and a subsequent `GET /v1/instances/{id}/messages`); (b) a POST outside the path prefix is refused; (c) the message payload reflects the real inbound bytes.
2. Run `go test ./lib/services/test/scenarios/ -run TestSensorWebhook -count=1` and confirm green.

---

## Pass 48: STORY-sensor-object-store (acceptance pass — STORY-sensor-object-store)

**Goal:** Deliver `STORY-sensor-object-store`.
**Scope:** Task 62
**Falsifier:** Restart re-emits already-discovered objects, OR the configured backend is ignored, OR metadata in the emitted message is canned.

### Task 62: Author cross-stack STORY-sensor-object-store proof

**Files:** `lib/services/test/scenarios/sensor_object_store_e2e_test.go` (new)

**Steps:**
1. Stand up a real S3-compatible store (minio via testcontainers, or the existing filesystem backend per the spec's note that backends are pluggable). Boot rimsky + sensor-object-store; configure the subscription to point at the test bucket; drop a new object; assert exactly one message emitted with the real object metadata. Restart sensor; drop another object; assert prior is NOT re-emitted.
2. Run `go test ./lib/services/test/scenarios/ -run TestSensorObjectStore -count=1` and confirm green.

---

## Pass 49: STORY-claim-producer-protocol (acceptance pass — STORY-claim-producer-protocol)

**Goal:** Deliver `STORY-claim-producer-protocol`. The spec cites `examples/claimproducer/claimproducer_test.go` + `lib/protocols/conformance/claimproducer/runner_terminals_test.go` for partial coverage.
**Scope:** Task 63
**Falsifier:** A registered producer's `Open` is bypassed, OR Commit/Abandon/Release are called but the producer's effect is canned, OR a write-semantics the producer didn't advertise is silently accepted at registration.

### Task 63: Author cross-stack STORY-claim-producer-protocol proof

**Files:** `examples/claimproducer/main_e2e_test.go` (new); `examples/claimproducer/README.md` (extend with walkthrough)

**Steps:**
1. Boot rimsky; register the example producer; deploy a template referencing it; drive a dispatch; assert real Open + real Commit/Abandon/Release. Assert a template referencing an un-advertised write-semantics is refused at registration.
2. Run `go test ./examples/claimproducer -run TestE2E -count=1` and confirm green.

---

## Pass 50: STORY-claim-producer-scopes-conflict (acceptance pass — STORY-claim-producer-scopes-conflict)

**Goal:** Deliver `STORY-claim-producer-scopes-conflict`. The spec cites `lib/services/test/scenarios/scopes_conflict/scopes_conflict_test.go`.
**Scope:** Task 64
**Falsifier:** Both writers acquire, OR the fan-out path skips the consult, OR producers without the capability are still asked.

### Task 64: Re-verify STORY-claim-producer-scopes-conflict proof

**Files:** `lib/services/test/scenarios/scopes_conflict/scopes_conflict_test.go` (existing; verify)

**Steps:**
1. Re-run after foundational passes. Confirm green.

---

## Pass 51: STORY-claim-producer-conformance (acceptance pass — STORY-claim-producer-conformance)

**Goal:** Deliver `STORY-claim-producer-conformance`. The spec cites multiple existing tests covering terminals + 9b probe + CLI.
**Scope:** Task 65
**Falsifier:** The 9b probe passes a dishonest producer, OR a duplicate-terminal-call failure is reported as pass, OR the CLI exits zero on failure.

### Task 65: Re-verify STORY-claim-producer-conformance proof

**Files:** `lib/protocols/conformance/claimproducer/runner_terminals_test.go`, `lib/services/test/scenarios/conformance_9b/probe_test.go`, `producers_test.go`, `lib/services/test/scenarios/atomic_staging/conformance_claimproducer_cli_test.go` (existing; verify)

**Steps:**
1. Re-run all four after foundational passes. Confirm green.

---

## Pass 52: STORY-claim-producer-observability (acceptance pass — STORY-claim-producer-observability)

**Goal:** Deliver `STORY-claim-producer-observability`. The spec cites per-store observability in-process tests; cross-stack operator-dashboard surface needs coverage.
**Scope:** Task 66
**Falsifier:** Streamed claim state lags or drops, OR an admin view the producer declared isn't surfaced through the dashboard route, OR the inventory pagination synthesizes rows.

### Task 66: Author cross-stack STORY-claim-producer-observability proof

**Files:** `lib/services/test/scenarios/claim_producer_observability_dashboard_e2e_test.go` (new)

**Steps:**
1. Boot rimsky + the bundled postgres or filesystem store; subscribe to the observability surface via `/v1/observability/...`; query claim detail and stream live claim-state changes; assert real producer state surfaces; render a producer-declared admin view; paginate inventory.
2. Run `go test ./lib/services/test/scenarios/ -run TestClaimProducerObservabilityDashboard -count=1` and confirm green.

---

## Pass 53: STORY-store-filesystem (acceptance pass — STORY-store-filesystem)

**Goal:** Deliver `STORY-store-filesystem`. The spec cites multiple existing tests for store-internal + atomic-staging cross-stack.
**Scope:** Task 67
**Falsifier:** Commit's swap is a copy-then-overwrite, OR the explicit-sync route doesn't actually refresh the queue, OR staging dir survives `Abandon`.

### Task 67: Re-verify STORY-store-filesystem proof

**Files:** `lib/services/stores/filesystem/store/store_test.go`, `ledger_test.go`, `pick_policy_test.go`, `drained_test.go`, `admin_sync_test.go`, `examples/atomic-staging-fs-producer/atomic_staging_test.go`, `lib/services/test/scenarios/atomic_staging/fs_held_swap_e2e_test.go` (existing; verify)

**Steps:**
1. Re-run all after foundational passes. Confirm green.

---

## Pass 54: STORY-store-postgres (acceptance pass — STORY-store-postgres)

**Goal:** Deliver `STORY-store-postgres`. Multiple existing tests cover store-internal + atomic-staging + verifier + error-classes.
**Scope:** Task 68
**Falsifier:** Atomic-staging schema is created but Commit doesn't atomically swap, OR `row_count_ratio` runs a non-aggregate query, OR `pg/swap_failed` is emitted as a generic error class, OR `pg/claim_unavailable` doesn't fire on a real empty-queue Open.

### Task 68: Re-verify STORY-store-postgres proof

**Files:** `lib/services/stores/postgres/store/store_test.go`, `atomic_staging_test.go`, `ledger_test.go`, `lib/services/test/scenarios/atomic_staging/pg_verifier_test.go`, `pg_verifier_commit_abandon_test.go`, `lib/services/test/scenarios/pg_error_classes/pg_error_classes_test.go` (existing; verify)

**Steps:**
1. Re-run all after foundational passes. Confirm green.

---

## Pass 55: STORY-validation-author (acceptance pass — STORY-validation-author)

**Goal:** Deliver `STORY-validation-author`. The spec cites `examples/validation/validation_test.go` for in-process; needs cross-stack.
**Scope:** Task 69
**Falsifier:** Error-severity finding doesn't block registration, OR warning-severity finding blocks registration, OR validator is registered but `Validate` is never called.

### Task 69: Author cross-stack STORY-validation-author proof

**Files:** `examples/validation/main_e2e_test.go` (new); `examples/validation/README.md` (extend)

**Steps:**
1. Boot rimsky; register the example service advertising Validation; deploy templates that should trigger error-severity (refused) and warning-severity (registered with warning surfaced); assert each.
2. Run `go test ./examples/validation -run TestE2E -count=1` and confirm green.

---

## Pass 56: STORY-data-processing-author (acceptance pass — STORY-data-processing-author)

**Goal:** Deliver `STORY-data-processing-author`. The spec cites `examples/data-processing/dataprocessing_test.go` + `test/scenarios/leaf_candidate_handle_e2e_test.go` for partial cross-stack.
**Scope:** Task 70
**Falsifier:** `BeginCandidate` is never called on a fan-out partition, OR `CommitCandidate` is called but the producer's effect is canned, OR `AbandonCandidate` is skipped on leaf failure, OR a declared version doesn't appear in `ListVersions`.

### Task 70: Author cross-stack STORY-data-processing-author proof

**Files:** `examples/data-processing/main_e2e_test.go` (new — extends the existing leaf-candidate-handle coverage with ListVersions / ListPartitions / GetVersionSchema); `examples/data-processing/README.md` (extend)

**Steps:**
1. Boot rimsky; register the example data-processing service; drive a fan-out + leaf success path (covered by existing scenario); add coverage for ListVersions / ListPartitions / GetVersionSchema reads after commit.
2. Run `go test ./examples/data-processing -run TestE2E -count=1` and confirm green.

---

## Pass 57: STORY-lifecycle-subscriber-author (acceptance pass — STORY-lifecycle-subscriber-author)

**Goal:** Deliver `STORY-lifecycle-subscriber-author`. The spec cites `examples/lifecyclesubscriber/subscriber_test.go` for in-process + `test/scenarios/host_agent_latebind_all_protocols_test.go` exhibits three of seven callbacks.
**Scope:** Task 71
**Falsifier:** A callback fires for the wrong transition, OR a documented context field is missing from the callback payload, OR the subscriber's failure response on a callback is ignored by rimsky (fire-and-forget).

### Task 71: Author cross-stack STORY-lifecycle-subscriber-author proof

**Files:** `examples/lifecyclesubscriber/main_e2e_test.go` (new — exercises all seven callbacks against running rimsky); `examples/lifecyclesubscriber/README.md` (extend)

**Steps:**
1. Boot rimsky; register the example subscriber; drive: register a template (assert OnTemplateRegistered with context fields), deploy (OnTemplateDeployed), create instance (OnInstanceCreated with service_bindings + owner_api_key_id), close a run-scope (OnRunScopeTerminal with terminal_reason + instance_id), terminate instance (OnInstanceTerminated), undeploy (OnTemplateUndeployed), deregister (OnTemplateDeregistered). For each callback, assert the documented context fields are populated. Drive a callback whose subscriber returns a failure; assert rimsky honors the failure synchronously (not fire-and-forget).
2. Run `go test ./examples/lifecyclesubscriber -run TestE2E -count=1` and confirm green.

---

## Pass 58: STORY-subscriber-openlineage (acceptance pass — STORY-subscriber-openlineage)

**Goal:** Deliver `STORY-subscriber-openlineage`. The spec cites `lib/services/subscribers/openlineage/subscriber_test.go` + `emitter_test.go` for in-process; cross-stack needs a real receiver.
**Scope:** Task 72
**Falsifier:** Subscriber posts to receiver but with malformed OpenLineage JSON, OR a lifecycle event the subscriber should emit on is skipped, OR the emitted event's IDs don't correspond to the rimsky-side IDs.

### Task 72: Author cross-stack STORY-subscriber-openlineage proof

**Files:** `lib/services/test/scenarios/subscriber_openlineage_e2e_test.go` (new)

**Steps:**
1. Stand up a fake OpenLineage receiver (`httptest.NewServer`) that validates inbound JSON against the OpenLineage 1.x schema (use the existing `openlineage-go` validation if available; otherwise inline a schema check). Boot rimsky + the openlineage subscriber; deploy a template; reach run-scope terminal; assert the receiver receives well-formed OpenLineage 1.x events; assert event IDs match rimsky-side IDs.
2. Run `go test ./lib/services/test/scenarios/ -run TestSubscriberOpenlineage -count=1` and confirm green.

---

## Pass 59: STORY-host-agent-late-bind-all-protocols (acceptance pass — STORY-host-agent-late-bind-all-protocols)

**Goal:** Deliver `STORY-host-agent-late-bind-all-protocols`. The spec cites `test/scenarios/host_agent_latebind_all_protocols_test.go` + `lib/runtime/hostagent/dispatch_unary_test.go`.
**Scope:** Task 73
**Falsifier:** Any of the five protocols returns `Unimplemented` through the proxy, OR a dispatch's effect is canned at the proxy layer rather than reaching the spawned binary.

### Task 73: Re-verify STORY-host-agent-late-bind-all-protocols proof

**Files:** `test/scenarios/host_agent_latebind_all_protocols_test.go`, `lib/runtime/hostagent/dispatch_unary_test.go` (existing; verify)

**Steps:**
1. Re-run after foundational passes. Confirm green.

---

## Pass 60: STORY-host-agent-per-run-scope-isolation (acceptance pass — STORY-host-agent-per-run-scope-isolation)

**Goal:** Deliver `STORY-host-agent-per-run-scope-isolation`.
**Scope:** Task 74
**Falsifier:** The two run-scopes share a single child, OR terminating one run-scope reaps both children, OR a terminated run-scope's child survives.

### Task 74: Re-verify STORY-host-agent-per-run-scope-isolation proof

**Files:** `test/scenarios/host_agent_per_run_scope_isolation_test.go`, `test/scenarios/host_agent_reap_test.go` (existing; verify)

**Steps:**
1. Re-run after foundational passes. Confirm green.

---

## Pass 61: STORY-host-agent-per-binding-overrides (acceptance pass — STORY-host-agent-per-binding-overrides)

**Goal:** Deliver `STORY-host-agent-per-binding-overrides`.
**Scope:** Task 75
**Falsifier:** An override is declared but ignored, OR the per-binding timeout has no effect.

### Task 75: Re-verify STORY-host-agent-per-binding-overrides proof

**Files:** `test/scenarios/host_agent_per_binding_exec_overrides_test.go` (existing; verify)

**Steps:**
1. Re-run after foundational passes. Confirm green.

---

## Pass 62: STORY-host-agent-anonymous-mode (acceptance pass — STORY-host-agent-anonymous-mode)

**Goal:** Deliver `STORY-host-agent-anonymous-mode`.
**Scope:** Task 76
**Falsifier:** Dispatch terminates with `host_agent_not_connected` despite the agent being connected, OR the dispatch reaches a different agent.

### Task 76: Re-verify STORY-host-agent-anonymous-mode proof

**Files:** `test/scenarios/host_agent_anonymous_mode_latebind_test.go` (existing; verify)

**Steps:**
1. Re-run after foundational passes. Confirm green.

---

## Pass 63: STORY-host-agent-control-plane (acceptance pass — STORY-host-agent-control-plane)

**Goal:** Deliver `STORY-host-agent-control-plane`. The spec cites `test/scenarios/host_agent_reap_test.go` for the reap leg; needs full start/status/stop demo.
**Scope:** Task 77
**Falsifier:** `stop` exits cleanly but leaves zombie children, OR `status` reports `connected` when the bidi stream is actually down, OR `start` silently succeeds with a misconfigured proxy URL.

### Task 77: Author STORY-host-agent-control-plane demo proof

**Files:** `examples/host-agent-control-plane-demo.sh` (new); `test/scenarios/host_agent_control_plane_demo_test.go` (new — driver test)

**Story:** STORY-host-agent-control-plane
**Proof form (from spec):** demo.

**Steps:**
1. Author the demo script: boot a `rimsky-host-agent-proxy` container; `rimsky agent start --proxy <url>`; `rimsky agent status` (expect connected); spawn a child via a dispatch; `rimsky agent stop`; verify exit code 0 + no zombie children remain. Also exercise the failure path: `rimsky agent start --proxy http://bogus` should refuse with a clear diagnostic, not silently succeed.
2. Author the driver test that runs the demo script as a subprocess and asserts exit code + output patterns.
3. Run `go test ./test/scenarios/ -run TestHostAgentControlPlaneDemo -count=1` and confirm green.

---

## Pass 64: STORY-rimsky-deployment-bootstrap (acceptance pass — STORY-rimsky-deployment-bootstrap)

**Goal:** Deliver `STORY-rimsky-deployment-bootstrap`. The spec cites `cmd/rimsky-entrypoint/main_test.go`.
**Scope:** Task 78
**Falsifier:** Migrations race when the three-container split fires three simultaneous `rimsky-entrypoint` processes, OR a three-container split never migrates, OR an unknown command silently spawns the all-in-one path.

### Task 78: Re-verify STORY-rimsky-deployment-bootstrap proof

**Files:** `cmd/rimsky-entrypoint/main_test.go` (existing; verify)

**Steps:**
1. Re-run after foundational passes. Confirm green.

---

## Pass 65: STORY-rimsky-health-check (acceptance pass — STORY-rimsky-health-check)

**Goal:** Deliver `STORY-rimsky-health-check`.
**Scope:** Task 79
**Falsifier:** Health route returns success while a critical dependency is down (false-positive), OR requires auth.

### Task 79: Author STORY-rimsky-health-check proof

**Files:** `test/scenarios/health_check_e2e_test.go` (new)

**Steps:**
1. Boot the all-in-one stack; `GET /v1/health` without bearer; assert 2xx and a health body. Sever the persistence connection (stop the Postgres container, or use the harness's database-down primitive); assert `GET /v1/health` now returns non-success.
2. Run `go test ./test/scenarios/ -run TestHealthCheck -count=1` and confirm green.

---

## Manual checks after completion

(None — this plan's stories are all delivered through automated acceptance passes with executable proofs. No story requires human-in-the-loop verification.)

---

## End-of-run handoff notes (for the user)

**Autonomous calls the plan made:**

1. **TD-event-log-kind-enum implementation shape.** The spec's `decision:event-log-kind-enum` says rimsky's app logic consumes typed values exclusively but doesn't prescribe whether the type lives in `lib/foundation/events/` or elsewhere. The plan defaults to `lib/foundation/events/kinds.go` (NEW) — a new sub-package of foundation — on the rationale that the events package is small, focused, and depended on by every emit site, which already span multiple layers; a new home is cleaner than retrofitting an existing package.

2. **URL prefix sweep at the chi-router level vs. per-handler.** The plan opts for a single `r.Route("/v1", ...)` wrap in `lib/control/controlapi/app.go` rather than rewriting every individual route registration. Same observable behavior, one-line change vs. dozens — the cheaper shape (rewriting every registration) buys nothing.

3. **Cross-stack proof for STORY-claude-agent uses a stub Claude CLI.** The real Claude CLI in CI is impractical (credentials + cost). The plan stubs the CLI's wire shape so the claude-agent executor's CLI-runner path is exercised end-to-end. The cheaper shape (TS-only in-process tests) is not the acceptance because the rimsky-side dispatch is the value-delivering surface; the stub CLI replaces the third-party Claude binary but the executor + rimsky are both real.

4. **`STORY-mcp-transport` proof samples one read + one mutation per tool category** rather than every tool. The Acceptance is parity — sampling exhibits parity efficiently; testing every tool would balloon the pass without strengthening the proof.

5. **`STORY-anonymous-mode-bootstrap` and `STORY-host-agent-control-plane` exercise failure paths** (re-running `auth init` on a non-empty keys table; misconfigured proxy URL) where the spec's Falsifier names them but doesn't require a specific test shape. The plan adds those explicitly because the Falsifier holds only if the failure path is reachable.

**Pre-existing patterns the plan matched:**

- Scenario tests under `test/scenarios/...` use the existing harness (`test/support/scenario/harness.go`). All new scenario tests follow the pattern of `test/scenarios/lifecycle_force_terminate_fullstack_test.go` and siblings.
- Cross-stack tests for bundled services go under `lib/services/test/scenarios/...` (per the prevailing convention — there's a separate test directory for service-scenario tests because they boot the bundled-service images rather than the core stack alone).
- Example-module tests go under `examples/<protocol>/` and follow the existing `*_test.go` shape there.
- The two existing `/v1/` carve-outs (`/v1/callback/...`, `/v1/observability/...`) absorb into the new prefix cleanly — no special handling required.
