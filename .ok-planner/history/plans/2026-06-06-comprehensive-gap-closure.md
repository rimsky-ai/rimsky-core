# Comprehensive Gap Closure Implementation Plan

**Spec:** .ok-planner/specs/2026-06-06-comprehensive-gap-closure-design.md
**Goal:** Close all 43 user-outcome gaps from the comprehensive gap-closure spec — restoring lost functionality, wiring half-built surfaces, and enforcing designed invariants — each proven by a red→green test and a per-story end-to-end acceptance gate.
**Architecture:** Go core (lib/graph → lib/runtime → lib/control; lib/foundation; lib/protocols) plus the bundled consumption-side services under lib/services (stores, sensors, executors incl. the TypeScript claude-agent) and the Apache examples/ module; gRPC/protobuf wire, chi HTTP, Postgres (pgx) / SQLite, testcontainers for full-stack tests.
**Tech Stack:** Go, TypeScript (claude-agent executor), Postgres + SQLite, gRPC/protobuf, go-chi, testcontainers-go.

---

## Pass CLICTRL-1: Doc-drift — `.claude/rules/rules.md` deploy-path accuracy gate

**Goal:** Replace the dead `deploy/build-images.sh` + `deploy/docker-compose.yml` references in `rules.md` with the real `make core-images` + testcontainers-harness mechanism, and add an automated accuracy check that fails when `rules.md` cites a filesystem path that does not exist on disk.
**Scope:** Tasks CLICTRL-1.1–1.2
**End state:** working
**Verification:** `go test ./tools/rulesdoc/... -run TestRulesDoc_CitedPathsExist -count=1`
**Proves:** Every filesystem path the rules doc instructs a contributor to run resolves against the current tree; no `deploy/`, bare `executors/claude-agent`, or `docs/` reference survives.
**Red-when-absent:** Before the doc fix the accuracy test fails on the assertion that the cited dead paths (`deploy/build-images.sh`, `deploy/docker-compose.yml`, `executors/claude-agent/{,node_modules/,dist/}`, `docs/2026-04-25-stores-redesign.md`) do not exist (failed assertion naming the missing paths), not an infra error.

### Task CLICTRL-1.1: RED — accuracy test that scans `rules.md` for non-existent cited paths
**Files:** `tools/rulesdoc/rulesdoc_test.go` (new), `tools/rulesdoc/doc.go` (new, package stub so the test package has a home — `package rulesdoc` with a doc comment only). (Confirm: `tools/` is a top-level module dir per CLAUDE.md; if `tools/` has no go.mod and is covered by the root module, place under `tools/rulesdoc/` in the root module — verify with `go list ./tools/...` and adjust the import-free test accordingly.)
**Steps:**
1. Write `TestRulesDoc_CitedPathsExist`: read `../../.claude/rules/rules.md` (resolve repo root by walking up from the test file dir until a `go.work` or `.git` is found, so the test is CWD-independent). Extract every backtick-quoted token that looks like a repo-relative filesystem path: contains a `/` AND (ends in `/` i.e. a trailing-slash directory ref, OR ends in a known file extension `.sh|.yml|.yaml|.md|.go`). Exclude URLs (`http://`, `https://`), `make ...` targets, glob-bearing tokens (`*` — e.g. `lib/protocols/proto/v1/*.proto`), and brace-expansion tokens (`{` — e.g. `…/{postgres,sqlite}/…`), since those are illustrative not literal `os.Stat`-able paths. For each remaining extracted path, assert `os.Stat(filepath.Join(root, path))` succeeds; collect every miss and `t.Errorf` listing the offenders.
2. Additionally assert the specific positive/negative contract: `rules.md` content contains the substring `make core-images` AND contains NONE of the currently-dead refs — `deploy/build-images.sh`, `deploy/docker-compose.yml` (`:20`), the bare `executors/claude-agent` prefix outside `lib/services/` (`:21`,`:46`), nor `docs/2026-04-25-stores-redesign.md` (`:51`). (Phrase the executor-prefix check so the corrected `lib/services/executors/claude-agent/…` paths are NOT flagged — e.g. assert the file contains no occurrence of `` `executors/claude-agent ``-with-leading-backtick.)
**Verification:** `! go test ./tools/rulesdoc/... -run TestRulesDoc_CitedPathsExist -count=1` (passes only while the test FAILS today — `deploy/build-images.sh` and `deploy/docker-compose.yml` are cited but absent on disk).

### Task CLICTRL-1.2: GREEN — correct every dead backtick repo-path in rules.md
**Files:** `.claude/rules/rules.md` (multiple bullets — the accuracy test from CLICTRL-1.1 scans the WHOLE file, so all dead repo-relative paths must resolve, not only the `deploy/` one).
**Grounded (verified on disk 2026-06-07):** these backtick repo-relative paths in the current `rules.md` do NOT resolve and must be corrected; the others (`lib/foundation/persistence/{postgres,sqlite}/migrations/`, `lib/protocols/proto/v1/*.proto`, `test/scenarios/...`, `lib/foundation/persistence/...`, `lib/runtime/...`, `lib/graph/scheduler/...`, `cmd/rimsky`, `.golangci.yml`, `cold-read/` (a real top-level dir), the bare `cold-read-cheatsheet.md` (no `/`, not matched)) all resolve and stay as-is:
- `:20` — `deploy/build-images.sh` and `deploy/docker-compose.yml` (no `deploy/` dir exists).
- `:21` — `executors/claude-agent/` (twice: the bold label `(executors/claude-agent/)` and the inline `cd executors/claude-agent` command) → real path is `lib/services/executors/claude-agent/`.
- `:46` — `executors/claude-agent/node_modules/` and `executors/claude-agent/dist/` (Search-Scoping exclusion list) → `lib/services/executors/claude-agent/node_modules/` and `lib/services/executors/claude-agent/dist/`.
- `:51` — the parenthetical example `docs/2026-04-25-stores-redesign.md` (no `docs/` dir; the bullet's own prose says proposals go in `.ok-planner/sketches/`) → make the example consistent with the bullet: `.ok-planner/sketches/2026-04-25-stores-redesign.md`.
**Steps:**
1. `:20` — Replace the bullet text that says rebuild via `deploy/build-images.sh` and bring up `deploy/docker-compose.yml` to reach `/health` with the real mechanism: rebuild the touched core images with `make core-images` (and `make service-images` for bundled-service changes) and verify the stack via the testcontainers-based services harness under `lib/services/test/` (e.g. `go test ./lib/services/test/scenarios/... -count=1`), which boots `rimsky-all-in-one:latest` and drives a node to terminal. Cite only paths/targets that exist (`make core-images`, `make service-images`, `lib/services/test/`).
2. `:21` — Repoint the TypeScript-executor bullet's two `executors/claude-agent` references to `lib/services/executors/claude-agent` (the bold label and the `cd …` command).
3. `:46` — In the Search-Scoping exclusion list, change `executors/claude-agent/node_modules/` → `lib/services/executors/claude-agent/node_modules/` and `executors/claude-agent/dist/` → `lib/services/executors/claude-agent/dist/`.
4. `:51` — Change the `(e.g. docs/2026-04-25-stores-redesign.md)` example to `(e.g. .ok-planner/sketches/2026-04-25-stores-redesign.md)` so the cited path resolves and matches the bullet's stated location. (If a `.ok-planner/sketches/` file by that exact name is not on disk, drop the parenthetical example entirely rather than cite a missing file — the rule text alone is sufficient.)
5. Re-read the whole file after editing and confirm no other backtick repo-relative path remains unresolved (`cold-read/` at `:41` is a real top-level dir and stays).
**Verification:** `go test ./tools/rulesdoc/... -run TestRulesDoc_CitedPathsExist -count=1` (now PASSES — every cited filesystem path resolves, no `deploy/` ref, no `executors/claude-agent/` ref, `make core-images` present).

---

## Pass CLICTRL-2: control-api — mandatory `Idempotency-Key` on `POST /instances/{id}/messages`

**Goal:** The message-emit handler rejects any request that omits the `Idempotency-Key` header with 400, inserting no idempotency row and no message envelope; a present key returns 201 on first insert and 200 on replay with the same `message_id`.
**Scope:** Tasks CLICTRL-2.1–2.3
**End state:** working
**Verification:** `go test ./lib/control/controlapi/ -run TestCreateMessage_MissingIdempotencyKeyRejected -count=1`
**Proves:** A missing-key emit returns 400 with a header-required diagnostic and persists nothing (zero rows on `GET /instances/{id}/messages`, no `rimsky_message_idempotencies` row); a keyed emit returns 201; a replay returns 200 with the identical message_id.
**Red-when-absent:** Before the guard, the missing-key POST returns 201 (the existing `TestMessages_PostListGet` proves un-keyed succeeds), so `TestCreateMessage_MissingIdempotencyKeyRejected`'s `require.Equal(400, …)` fails — a failed assertion, not infra.
**Gate-scope note (2026-06-07):** the pass-level gate is scoped to ONLY the new `TestCreateMessage_MissingIdempotencyKeyRejected`. The replay term (`TestCreateMessage_IdempotencyKeyDuplicateReturnsExisting`) was REMOVED from this gate because it already EXISTS and PASSES on the current tree (dedup logic is already wired) — including it made the OR-filter gate green-from-birth. The per-task `! go test` RED checks (CLICTRL-2.1) and the broader replay/regression runs (CLICTRL-2.2/2.3 Verification) still exercise the replay path; this pass gate only needs the NEW red→green test.

### Task CLICTRL-2.1: RED — test that a keyless emit is rejected 400 and persists nothing
**Files:** `lib/control/controlapi/messages_test.go` (add `TestCreateMessage_MissingIdempotencyKeyRejected`).
**Steps:**
1. Using `newHarness` + `newInstanceForMessages`, POST to `/instances/{id}/messages` with a valid invalidate body (`kind:invalidate`, `target:root`) and NO `Idempotency-Key` header (use `h.httpJSON`, which sets no idempotency header).
2. Assert HTTP 400 and an error body whose message names the required header (`require.Contains(strings.ToLower(out["error"]), "idempotency-key")`).
3. Assert no envelope persisted: `GET /instances/{id}/messages` returns zero messages. Assert no idempotency row: read via `h.persist.MessageIdempotencies()` lookup for that instance/sender (or assert the messages list is empty, which is sufficient since the envelope insert is gated in the same tx).
**Verification:** `! go test ./lib/control/controlapi/ -run TestCreateMessage_MissingIdempotencyKeyRejected -count=1` (passes only while RED — handler returns 201 today).

### Task CLICTRL-2.2: GREEN — require the header in `handleCreateMessage`
**Files:** `lib/control/controlapi/messages.go` (the `idempotencyKey := req.Header.Get("Idempotency-Key")` read at :167 and the handler body).
**Steps:**
1. After reading `idempotencyKey` and before opening the persistence transaction, add a guard: `if strings.TrimSpace(idempotencyKey) == "" { badRequest(w, "Idempotency-Key header is required") ; return }`. Place it after the body/kind/sender-kind validation so a malformed body still 400s on the more-specific reason first (match existing ordering: the publisher-capability checks run inside the tx, but the header is request-level — guard it pre-tx, alongside the other request-level 400s).
2. Now that the key is always non-empty, the `if idempotencyKey != ""` branch at :217 is unconditionally taken; leave the dedup INSERT/lookup logic intact (it already returns the original id with `replayed=true` → 200). Remove the now-dead empty-key fallthrough if any (keep behavior identical for the present-key path).
3. Update the dry-run path: the dry-run gate at :208 returns before the dedup insert; keep the header guard ahead of the dry-run branch so a keyless dry-run is also rejected (a dry-run preview of an emit must still carry the key it would dedup on).
**Verification:** `go test ./lib/control/controlapi/ -run 'TestCreateMessage_MissingIdempotencyKeyRejected|TestMessages_PostListGet|TestCreateMessage_IdempotencyKeyDuplicateReturnsExisting' -count=1` — the new test PASSES; replay still 200.

### Task CLICTRL-2.3: GREEN — fix the checked-in keyless test that asserts 201
**Files:** `lib/control/controlapi/messages_test.go` (`TestMessages_PostListGet` at :51-56, and the other helper sites that POST a message with no idempotency header: `TestMessages_ListByFrameID` :103, `TestMessages_PostInvalidKind`/`TestMessages_TargetTerminatedInstanceConflict` POST keyless bodies, and the publisher-capability tests `TestCreateMessage_SenderKind*`).
**Steps:**
1. For every test whose intent is a *successful* emit (`TestMessages_PostListGet`, `TestMessages_ListByFrameID` `post()`, `TestCreateMessage_SenderKindPublisherActiveSubscriptionSucceeds`), switch the POST to `h.httpJSONWithHeaders(..., map[string]string{"Idempotency-Key": "key-"+uuid.NewString()})` and keep the 201 assertion.
2. For tests whose intent is a *non-201 reject that fires BEFORE the header guard would* (invalid kind, terminated instance, missing-sub 400, stopped/unknown/wrong-instance 403, invalid sender_kind 400): these must still hit their own status. Add an `Idempotency-Key` header to each so the header guard does not pre-empt the intended error — except `TestMessages_PostInvalidKind`/`SenderKind*BadRequest`/`MissingSubscriptionIDBadRequest` whose 400 fires inside body validation BEFORE the tx; confirm ordering so each test still asserts its own status (guard runs after kind/sender-kind validation per CLICTRL-2.2 step 1). Add the header to the terminated-instance and publisher-capability tests (whose checks run inside the tx, after the guard).
3. Run the full messages test file to confirm no regression in the 12 existing funcs.
**Verification:** `go test ./lib/control/controlapi/ -run TestMessages -count=1 && go test ./lib/control/controlapi/ -run TestCreateMessage -count=1` (all pass; no test still asserts a keyless 201).

---

## Pass CLICTRL-3: control-api — per-status idempotency + publisher-capability matrix

**Goal:** Land the full per-status acceptance matrix the 2026-05-17 plan demanded so each idempotency/publisher-capability HTTP status is pinned by its own named test (first-insert 201, replay 200 same id, missing-key 400, distinct-sender no-collision, active-sub success, stopped-sub 403, unknown-sub 403, wrong-instance 403, missing-sub-id 400).
**Scope:** Tasks CLICTRL-3.1–3.2
**End state:** working
**Verification:** `go test ./lib/control/controlapi/ -run TestIdempotencyMatrix -count=1`
**Proves:** Every status path is exercised green through the real handler; flipping any single status in `handleCreateMessage` turns exactly its matrix sub-test red.
**Red-when-absent:** The matrix test does not exist (and the missing-key 400 path returns 201 today), so the consolidated `TestIdempotencyMatrix` fails on the 400 sub-assertion before CLICTRL-2's fix; written after CLICTRL-2 the matrix is the regression lock.

### Task CLICTRL-3.1: RED — consolidated status matrix test driving the real handler
**Files:** `lib/control/controlapi/idempotency_matrix_test.go` (new). Reuse `newHarness`, `newInstanceForMessages`, `insertPublisherSubscription`, `httpJSON`/`httpJSONWithHeaders` from `app_test.go`/`messages_test.go`.
**Steps:**
1. Write `TestIdempotencyMatrix` with table-driven or sequential sub-cases (one `t.Run` per status), each driving a REAL `POST /instances/{id}/messages` through the harness:
   - `first_insert_201`: keyed operator emit → 201, capture message_id.
   - `replay_200_same_id`: same key+sender replay → 200 and identical message_id.
   - `missing_key_400`: no header → 400 (this sub-case is what fails pre-CLICTRL-2; serves as the regression anchor).
   - `distinct_sender_no_collision`: same key, operator vs publisher sender → both 201, distinct ids (active sub seeded).
   - `active_sub_success`: publisher emit with active sub → 201.
   - `stopped_sub_403`, `unknown_sub_403`, `wrong_instance_403`: 403 each.
   - `missing_sub_id_400`: publisher sender, no `publisher_subscription_id` → 400.
   Each success case carries an `Idempotency-Key` header; capability-failure cases carry one too (so the 403/400 is the capability reason, not the header guard).
2. Assert the persisted side where the spec names it: replay leaves a single envelope (`GET …/messages` count unchanged), distinct-sender yields two envelopes.
**Verification:** `! go test ./lib/control/controlapi/ -run TestIdempotencyMatrix -count=1` if run BEFORE CLICTRL-2 lands (the `missing_key_400` sub-case fails). NOTE for the orchestrator: if CLICTRL-2 is already green when this task runs, demonstrate red-for-the-right-reason by temporarily asserting `missing_key_400` expects 400 while the handler is reverted, OR sequence CLICTRL-3.1 to author the test and run `! go test -run TestIdempotencyMatrix/missing_key_400` against a tree with CLICTRL-2.2 reverted; record the exit. (See Pass-3 verification-evidence note below.)

### Task CLICTRL-3.2: GREEN — confirm matrix passes against the fixed handler
**Files:** none (handler already fixed in CLICTRL-2.2). If any sub-case reveals a real status defect (e.g. unknown-sub returns 400 not 403), fix `handleCreateMessage` to the spec's status and note it.
**Steps:**
1. Run the matrix; for any sub-case that is red because the handler emits the wrong status, fix the handler at the named site (`messages.go` publisher-capability mapping :294-296 for 403; missing-sub-id :157-159 for 400) — do NOT weaken the test.
2. Confirm each status maps to exactly one sub-test by flipping one status locally (manual reasoning in the report; no committed change).
**Verification:** `go test ./lib/control/controlapi/ -run TestIdempotencyMatrix -count=1` (PASS, every sub-case green).

---

## Pass CLICTRL-4: control-api — server-side `compose:` reserved-prefix guard (resolves `tension:compose-prefix-client-side`)

**Goal:** The control-api itself rejects tag-create and instance-create whose tag / instance_key uses the reserved `compose:` prefix with 400 + a reserved-prefix diagnostic, UNLESS the request carries the trusted compose-origin marker (set only by the compose engine). No row is created on rejection.
**Scope:** Tasks CLICTRL-4.1–4.4
**End state:** working
**Verification:** `go test ./lib/control/controlapi/ -run 'TestCreateTag_ComposePrefixRejected|TestCreateInstance_ComposePrefixRejected|TestCreateTag_ComposeOriginAllowed' -count=1`
**Proves:** A raw `POST /tags` `{"tag":"compose:…"}` (no marker) → 400, no tag on `GET /tags`; a `POST /instances` with `instance_key` `compose:…` (no marker) → 400, no instance; the same writes WITH the compose-origin marker succeed.
**Red-when-absent:** Today `validTag` accepts `:` and the instance-key path applies no prefix check, so the create succeeds (2xx) — the rejection assertions fail; right-reason red (failed status assertion), not infra.

### Task CLICTRL-4.1: RED — server rejects compose-prefixed tag and instance_key from a foreign client
**Files:** `lib/control/controlapi/compose_prefix_test.go` (new). Reuse `newHarness`, `validTemplateBody`, `httpJSON`/`httpJSONWithHeaders`.
**Steps:**
1. `TestCreateTag_ComposePrefixRejected`: register+deploy a template, then `POST /tags` `{"tag":"compose:my-app:v1","template":<hash>}` with NO compose-origin header → assert 400 and an error naming the reserved prefix; then `GET /tags` and assert no tag with that name appears.
2. `TestCreateInstance_ComposePrefixRejected`: register+deploy a template, then `POST /instances` `{"template":<hash>,"instance_key":"compose:my-app:i1"}` with NO marker → assert 400 + reserved-prefix diagnostic; assert `GET /instances/{key}` 404 (no instance created).
3. `TestCreateTag_ComposeOriginAllowed`: same `POST /tags` body but WITH the trusted header (the marker name fixed in CLICTRL-4.2, e.g. `X-Rimsky-Compose-Origin: 1`) → assert 201 and the tag appears in `GET /tags`.
**Verification:** `! go test ./lib/control/controlapi/ -run 'TestCreateTag_ComposePrefixRejected|TestCreateInstance_ComposePrefixRejected' -count=1` (passes only while RED — creates succeed today).

### Task CLICTRL-4.2: GREEN — guard `handleCreateTag` and `handleCreateInstance`
**Files:** `lib/control/controlapi/tags.go` (`handleCreateTag` :62; `validTag` :30 stays as-is for syntax), `lib/control/controlapi/instances.go` (`handleCreateInstance` :286, after the empty-key normalization at :303), and a shared helper file `lib/control/controlapi/compose_prefix.go` (new) holding `const composeReservedPrefix = "compose:"`, `const composeOriginHeader = "X-Rimsky-Compose-Origin"`, and `func isComposeOrigin(r *http.Request) bool { return r.Header.Get(composeOriginHeader) == "1" }`.
**Steps:**
1. In `handleCreateTag`, after `validTag` passes: `if strings.HasPrefix(body.Tag, composeReservedPrefix) && !isComposeOrigin(req) { badRequest(w, "tag uses reserved prefix \"compose:\" (managed by the compose command)"); return }`.
2. In `handleCreateInstance`, after the empty-key normalization (:303) and before the template lookup: `if body.InstanceKey != nil && strings.HasPrefix(*body.InstanceKey, composeReservedPrefix) && !isComposeOrigin(req) { badRequest(w, "instance_key uses reserved prefix \"compose:\" (managed by the compose command)"); return }`.
3. Place both guards ahead of any persistence write so a rejection creates nothing.
**Verification:** `go test ./lib/control/controlapi/ -run 'TestCreateTag_ComposePrefixRejected|TestCreateInstance_ComposePrefixRejected|TestCreateTag_ComposeOriginAllowed' -count=1` (all pass).

### Task CLICTRL-4.3: GREEN — concept-doc edits (resolves the tension)
**Files:** `.ok-planner/design/concepts/tag.md`, `.ok-planner/design/concepts/control-api.md`, `.ok-planner/design/tensions/compose-prefix-client-side.md` → move to `.ok-planner/design/tensions/_resolved/compose-prefix-client-side.md`.
**Steps:**
1. `concept:tag` — rewrite the "Open within this concept" bullet (currently "enforced client-side only … see tension:compose-prefix-client-side") to an Invariant: "The `compose:<project>:<...>` tag prefix is reserved and **server-enforced**: tag-create rejects a `compose:`-prefixed name unless the request originates from the privileged compose path. Enforcement is at the source of truth, not a CLI courtesy." Append a dated Notes entry: `2026-06-07 — compose-prefix reservation moved from client-side convention to server-enforced invariant per spec:2026-06-06-comprehensive-gap-closure.` Keep the entry path-free (slug-form only).
2. `concept:control-api` — update BOTH compose-prefix mentions (grounded 2026-06-07): (a) the Invariant bullet at `#30` currently reads "The compose tag/instance-key prefix is reserved for the compose command but enforcement is client-side only; the server accepts any string." → change to: "The compose tag/instance-key prefix is server-enforced: tag-create and instance-create reject the reserved prefix from non-compose origins." (b) the SECOND residual mention at `#48` currently reads "The compose prefix reservation is client-side only — see `tension:compose-prefix-client-side`." → change to the server-enforced statement (path-free, no live-tension cite since the tension is now resolved): "The compose prefix reservation is server-enforced (see the Invariant above)." Append the same dated Notes entry once.
3. Move the tension file under `_resolved/`, set its frontmatter `status: resolved`, and add a Resolution note (path-free) recording the server-guard + compose-origin marker resolution.
**Verification:** `test -f .ok-planner/design/tensions/_resolved/compose-prefix-client-side.md && ! test -f .ok-planner/design/tensions/compose-prefix-client-side.md && grep -q 'server-enforced' .ok-planner/design/concepts/tag.md && grep -q 'server-enforced' .ok-planner/design/concepts/control-api.md`

### Task CLICTRL-4.4: GREEN — Client sets the compose-origin marker (consumed by the compose engine)
**Files:** `cmd/rimsky/cli/client.go` (add `composeOrigin bool` field + `func (c *Client) SetComposeOrigin(v bool)`; in `doStatus` set `req.Header.Set("X-Rimsky-Compose-Origin","1")` when `c.composeOrigin`).
**Steps:**
1. Add the field, setter, and header injection in `doStatus` (alongside the existing User-Agent/Authorization sets at :126-130).
2. (Consumed in Pass CLICTRL-5: the compose engine calls `c.SetComposeOrigin(true)` on the client it builds, so compose-originated writes carry the marker and succeed past the new guard.)
**Verification:** `go build ./cmd/... && go test ./cmd/rimsky/cli/ -run TestClient -count=1`

---

## Pass CLICTRL-5: CLI — restore the compose engine (`rimsky compose up|down|plan|status`)

**Goal:** Recover the deleted app-layer compose engine from `70a0b98^`, adapt it to `cmd/rimsky/cli`, wire `rimsky compose <verb>`, and have `compose up` reconcile a `rimsky-compose.yml` against an already-running stack (register+deploy templates, create one instance per member, all `compose:`-prefixed and carrying the compose-origin marker), with `compose down` tearing them down. No docker/infra invocation, no config materialization.
**Scope:** Tasks CLICTRL-5.1–5.6
**End state:** working
**Verification:** `go test ./cmd/rimsky/cli/compose/... -count=1`
**Proves:** The recovered manifest/plan/apply/state/down engine builds and its recovered unit tests pass against `httptest`; `rimsky compose up <manifest>` exits 0 and produces compose-prefixed tags+instances; `compose down` removes them.
**Red-when-absent:** The `compose` subpackage does not exist; `cmd/rimsky/main.go` has no `compose` case (unknown command → exit 2). The recovered tests fail to compile/run until the engine is restored.

### Task CLICTRL-5.1: RED — recover compose source from git (engine only, infra halves dropped)
**Files:** new `cmd/rimsky/cli/compose/{cmd.go,manifest.go,resolver.go,state.go,plan.go,apply.go,down.go}` recovered via `git show 70a0b98^:control/cli/compose/<file>`; recovered tests `cmd/rimsky/cli/compose/{manifest_test.go,resolver_test.go,state_test.go,plan_test.go,apply_test.go,down_test.go}`. DROP `dev.go`, `dev_test.go`.
**Steps:**
1. For each kept file, `git show 70a0b98^:control/cli/compose/<file> > cmd/rimsky/cli/compose/<file>`.
2. Rewrite imports: `github.com/rimsky-ai/rimsky-core/control/cli` → `github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli`; `github.com/rimsky-ai/rimsky-core/graph/node` → `…/lib/graph/node`; `github.com/rimsky-ai/rimsky-core/graph/template/canonical` → `…/lib/graph/template/canonical`.
3. In `cmd.go`, delete `DispatchDev` and its `dev`-verb switch (keep only `Dispatch` for `up|down|plan|status`).
4. In `manifest.go`, delete the `Infra`, `InfraCommand`, `RimskyConfig` types + the `Infra`/`RimskyConfig` fields on `Manifest` and their `Validate()` clauses (the cut infra/scaffold half). Keep `Project`/`Context`/`Templates`/`Instances` + all tag/instance/restart/state validation, `PrefixedTag`/`PrefixedInstanceKey`/`ResolveTemplateRef`/`EffectiveState`/`EffectiveRestart`. Replace `cli.ReservedTagPrefix` references with the existing constant in `cmd/rimsky/cli` (`ReservedTagPrefix` at `templates.go:209`).
5. In `down.go`, delete the `infra.down` hook (`runCommand` + the infra-flag handling) and the `--infra` flag; keep `ComputeDownPlan` + `RunComposeDown`/`runComposeDownWithManifest` (app-layer teardown only).
6. In the recovered tests, delete `dev_test.go` and the infra sub-tests in `down_test.go` (`TestRunComposeDown_InfraFlag*`) and `manifest_test.go` (`TestValidate_BadInfraURL`, `TestValidate_BadTimeout`, and the `context: dev`/`Infra:` fixtures). Repoint the package name to `compose` and imports to `cmd/rimsky/cli`.
**Verification:** `! go vet ./cmd/rimsky/cli/compose/... 2>&1 | grep -q .` is NOT the gate — instead: `! go test ./cmd/rimsky/cli/compose/... -count=1` is expected to be unbuildable until 5.2 wires the marker; treat 5.1+5.2 as one RED→GREEN unit. Record the build error from `go build ./cmd/rimsky/cli/compose/...` (expect undefined-symbol or unused-import while mid-recovery) as the right-reason red.

### Task CLICTRL-5.2: GREEN — compose engine builds and sets the compose-origin marker on its client
**Files:** `cmd/rimsky/cli/compose/apply.go` (`clientForManifest` :395) and `down.go` (client construction :146).
**Steps:**
1. After `cli.NewClient(endpoint)` in `clientForManifest` and in `runComposeDown`'s client build, call `c.SetComposeOrigin(true)` (the setter added in CLICTRL-4.4) so every compose write carries `X-Rimsky-Compose-Origin: 1` and passes the server guard.
2. Resolve any remaining symbol mismatches between the recovered code and current `cmd/rimsky/cli` (e.g. confirm `cli.RegisterTemplateRequest.Source`, `cli.TruncHash`, `cli.IsConflict`, `cli.AnsiGreen`, `cli.EmitTable`, `cli.ResolveEndpointForCompose` signatures match; adapt call sites if a signature drifted).
3. Run `goimports`/`gofmt` on the new files.
**Verification:** `go build ./cmd/rimsky/cli/compose/... && go test ./cmd/rimsky/cli/compose/... -count=1` (recovered unit tests pass against httptest).

### Task CLICTRL-5.3: RED — `rimsky compose` dispatch is wired
**Files:** `cmd/rimsky/cli/compose/compose_dispatch_test.go` (new) OR a `main`-level test. Because dispatch lives in `cmd/rimsky/main.go` (package main, not easily unit-testable), assert dispatch via the subpackage `Dispatch` entrypoint: `TestComposeDispatch_UnknownSubcommand` asserts `compose.Dispatch(ctx, []string{"bogus"})` returns 2, and `TestComposeDispatch_NoArgs` returns 2 — proving the dispatcher exists and routes. (The `main.go` wiring is covered by the acceptance gate, which runs the real binary path via `cli`-level invocation.)
**Steps:**
1. Write the two dispatch tests against `compose.Dispatch`.
**Verification:** `! go test ./cmd/rimsky/cli/compose/ -run TestComposeDispatch -count=1` before 5.4 wiring if `Dispatch` was accidentally dropped; if `Dispatch` is present from recovery this is green-after-5.2 — in that case the load-bearing RED is the `main.go` `compose` case (5.4), proven by the acceptance gate. Record which.

### Task CLICTRL-5.4: GREEN — add the `compose` case to `cmd/rimsky/main.go`
**Files:** `cmd/rimsky/main.go` (the top-level switch at :23-82; the root-usage printer at :349).
**Steps:**
1. Add `case "compose": os.Exit(dispatchCompose(os.Args[2:]))` and a `func dispatchCompose(args []string) int { return compose.Dispatch(context.Background(), args) }`, importing `github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli/compose`.
2. Add a `Compose:` block to `printRootUsage` listing `compose up | down | plan | status`.
**Verification:** `go build ./cmd/... && go run ./cmd/rimsky compose 2>&1 | grep -q 'up|down|plan|status'` (usage prints; exit nonzero is fine for the no-subcommand case — the grep is the assertion).

### Task CLICTRL-5.5: GREEN — update the CLI reserved-prefix messaging (now backed by a real verb)
**Files:** `cmd/rimsky/cli/tags.go` (:34-35, :156-157, :186-187), `cmd/rimsky/cli/templates.go` (:236-238), `cmd/rimsky/cli/run.go` (:140-143).
**Steps:**
1. The client-side rejections stay (defense-in-depth) but their message text "managed by `compose`" is now accurate since the verb exists — no behavior change required; verify the strings still read correctly. No edit unless a message references a nonexistent command.
**Verification:** `go test ./cmd/rimsky/cli/ -run 'TestRunTag|TestRunTemplate' -count=1`

### Task CLICTRL-5.6: GREEN — `examples/` ships a `rimsky-compose.yml` (used by the acceptance gate)
**Files:** `examples/compose/rimsky-compose.yml` (new), `examples/compose/template-a.yml` + `examples/compose/template-b.yml` (new, each a single `executor: stub` node spec), `examples/README.md` (add a `compose/` row + a fenced `rimsky compose up` block).
**Steps:**
1. Author `template-a.yml`/`template-b.yml` as minimal TemplateSpecs (`name`, `version`, `frame_resolution_mode: serial_queue`, `nodes: [{type: worker, executor: stub}]`) that round-trip through `node.ValidateTemplate`.
2. Author `rimsky-compose.yml`: `project: project-alpha`, two `templates:` entries (paths `./template-a.yml`/`./template-b.yml`, bare tags `tpl-a@1`/`tpl-b@1`, `state: deployed`), two `instances:` (one per template, names `inst-a`/`inst-b`). Use generic illustrative names per the project-agnostic rule.
3. Add the `examples/README.md` row and a fenced block showing `rimsky compose up -f examples/compose/rimsky-compose.yml`.
**Verification:** `go test ./cmd/rimsky/cli/compose/ -run TestComposeManifestExampleLoads -count=1` — add a small test that `LoadManifest("…/examples/compose/rimsky-compose.yml")` parses and validates clean and that each referenced template spec round-trips `node.ValidateTemplate`.

---

## Pass CLICTRL-6: control-api — `settling_signal_type` on `GET /nodes/{id}`

**Goal:** The node-detail response surfaces the node's settling signal type (read from the persisted `NodeRow.SettlingSignalType`), absent/empty for an unsettled node.
**Scope:** Tasks CLICTRL-6.1–6.2
**End state:** working
**Verification:** `go test ./lib/control/controlapi/ -run TestGetNode_SettlingSignalType -count=1`
**Proves:** After a node persists a `settling_signal_type`, `GET /nodes/{id}` JSON includes a `settling_signal_type` field carrying that canonical value; a node without one omits the field.
**Red-when-absent:** `nodeResponse` has no such field today, so the test's `require.Equal(<value>, out["settling_signal_type"])` fails on a nil/absent key — failed assertion, not infra.

### Task CLICTRL-6.1: RED — node detail returns the persisted signal type
**Files:** `lib/control/controlapi/nodes_test.go` (new — does not exist on the current tree; TEMPLCASCADE-6.1 also targets this same file, so whichever pass lands first creates it and the other appends — both are `package controlapi`). Reuse `newHarness`; seed a node row with `SettlingSignalType` set via the persistence layer.
**Steps:**
1. Register+deploy a template and create an instance so a node row exists; resolve its node id via `GET /instances/{id}/nodes`.
2. Set the node's `settling_signal_type` to a known canonical value through the REAL persisted column the handler reads. There is no `SetSettlingSignalType` primitive (grounded 2026-06-07 — it does not exist). The real writer is `RunTreeTable.UpdateStateAndOutcome(ctx, tx, runID, state, settlingSignalType *string)` (interface `lib/foundation/persistence/run_tree.go`; postgres impl `lib/foundation/persistence/postgres/run_tree.go:165`, which runs `UPDATE rimsky_node_runs SET state = $2, settling_signal_type = $3 WHERE id = $1`). Prefer driving a real settle through the stub executor if available in this harness; otherwise call `h.persist.RunTree().UpdateStateAndOutcome(ctx, tx, runID, <state>, &signalType)` inside a `h.persist.Transaction` (or seed the column via a raw `UPDATE rimsky_node_runs SET settling_signal_type = $1 WHERE id = $2` on the test's pg transaction). Either way it is the same persisted column `toNodeResponse` projects.
3. `GET /nodes/{nodeID}` and assert `out["settling_signal_type"]` equals the seeded value.
4. Second case: a freshly-created node (no settle) → assert the key is absent/empty.
**Verification:** `! go test ./lib/control/controlapi/ -run TestGetNode_SettlingSignalType -count=1` (passes only while RED — field missing from response today).

### Task CLICTRL-6.2: GREEN — add the field to `nodeResponse`/`toNodeResponse`
**Files:** `lib/control/controlapi/nodes.go` (`nodeResponse` :32, `toNodeResponse` :52).
**Steps:**
1. Add `SettlingSignalType string `json:"settling_signal_type,omitempty"`` to `nodeResponse`.
2. In `toNodeResponse`, set it from `n.SettlingSignalType` (deref the `*string`, leaving "" when nil so `omitempty` drops it). Mirror the projection already in `backfills.go::backfillPartitionRow`.
3. Add the field to `cmd/rimsky/cli/client.go::Node` (the CLI's node read struct at :667) so `rimsky node get` surfaces it (keep the wire shape consistent across the client).
**Verification:** `go test ./lib/control/controlapi/ -run TestGetNode_SettlingSignalType -count=1 && go build ./cmd/...` (PASS; CLI builds with the new field).

---

## Pass CLICTRL-7: CLI — `rimsky watch` chronological (timestamp-merged) feed

**Goal:** `rimsky watch <id>` prints events, breakpoint hits, and the terminal line in true timestamp order across the three sources within each poll window (a hit between two events sits between them), not source-grouped.
**Scope:** Tasks CLICTRL-7.1–7.2
**End state:** working
**Verification:** `go test ./cmd/rimsky/cli/ -run TestRunWatch_Chronological -count=1`
**Proves:** Given an interleaved sequence (event@t1, hit@t2, event@t3) within one poll window, captured `watch` stdout shows the hit line between the two event lines by timestamp.
**Red-when-absent:** `RunWatch` drains all events, then all hits, then terminal — so the hit prints AFTER both events regardless of timestamp. The ordering assertion fails on the current source-grouped output (failed assertion).

### Task CLICTRL-7.1: RED — interleaved-timestamp ordering test
**Files:** `cmd/rimsky/cli/watch_test.go` (new). Reuse `setupClitest`, `srv.State.AddEvent`/`AddBreakpointHit`/`SetInstanceTerminated`, `captureStdout` (from `templates_test.go`/`instances_test.go`).
**Steps:**
1. Confirm the clitest fake lets a test set `OccurredAt` on an Event and `hit_at` on a breakpoint hit (inspect `srv.State.AddEvent`/`AddBreakpointHit`; if `AddBreakpointHit` does not accept a timestamp, extend the fake's hit map to carry a settable `hit_at`). The fake is test-only scaffolding, so extending it is in-bounds.
2. Seed: event A `OccurredAt=t1`, breakpoint hit `hit_at=t2` (t1<t2<t3), event B `OccurredAt=t3`, all in one poll window; then `SetInstanceTerminated`.
3. Capture `RunWatch(ctx, []string{"--poll-interval","10s", id})` stdout. Parse the printed lines; assert the breakpoint.hit line's position is strictly between event A's and event B's lines (compare line indices), i.e. order is A, hit, B, terminal.
**Verification:** `! go test ./cmd/rimsky/cli/ -run TestRunWatch_Chronological -count=1` (passes only while RED — current output is A, B, hit).

### Task CLICTRL-7.2: GREEN — merge the three sources by timestamp per poll cycle
**Files:** `cmd/rimsky/cli/watch.go` (`RunWatch` :62-142; the print helpers :148-186).
**Steps:**
1. Refactor the per-poll body: instead of printing events inline during drain then hits during drain, accumulate this cycle's new rows into a single `[]watchLine` slice where each entry carries a parsed timestamp + a render closure (event → `printWatchEvent`, hit → `printWatchHit`). Drain all event pages and all hit pages into the slice first.
2. Sort the slice by timestamp (parse `Event.OccurredAt` via the same layout the server emits, RFC3339; parse the hit's `hit_at`); stable-sort so equal timestamps keep arrival order. Then render in sorted order.
3. Keep the high-watermarks (`lastSeenID`, `sinceSeq`) exactly as today so cross-cycle dedup is unchanged; the sort is within-cycle. Terminal check stays last and prints after the merged batch.
4. Update the docstring (:8-13) to state the feed is timestamp-merged across the three sources per poll cycle (it already claims "one chronological feed" — make it true). Update `cmd/rimsky/main.go:361` help text only if wording drifted (no functional change).
**Verification:** `go test ./cmd/rimsky/cli/ -run 'TestRunWatch_Chronological|TestRunWatch_ExitsOnTerminal|TestRunWatch_DrainsAllHitsBeforeTerminal' -count=1` (new test passes; existing watch tests still green).

---

## Verification-evidence note for CLICTRL (run-now results)

I ran the green-from-birth probe to confirm the trap applies to the not-yet-authored tests:
- `go test ./lib/control/controlapi/ -run TestThisDoesNotExistAtAll_XYZ` → exit 0, "no tests to run". CONFIRMED: every gate whose test is authored by a RED task must be proven red by running `! go test -run <NewTest>` AFTER the test file is written (not against the bare current tree). Each RED task's Verification uses the `!`-inverted form for exactly this reason.
- The two doc/handler gates that CAN be reasoned about against the current tree:
  - Multiple cited paths in `rules.md` are dead on disk (verified 2026-06-07): `deploy/` does NOT exist (`:20`); `executors/claude-agent` does NOT exist — the real path is `lib/services/executors/claude-agent` (`:21`,`:46`); `docs/` does NOT exist (`:51` parenthetical example) — the file lives under `.ok-planner/`. So CLICTRL-1.1's authored test is red-for-the-right-reason on each. (`cold-read/` at `:41` IS a real top-level dir and resolves.)
  - `lib/control/controlapi/messages.go:217,301` confirm the keyless path returns 201 with no required-header guard, and `messages_test.go:51-56` (`TestMessages_PostListGet`) POSTs keyless and asserts 201 → CLICTRL-2.1 + the `missing_key_400` matrix sub-case are red-for-the-right-reason.
- Docker-dependent acceptance gates (G1, G2, G3, G5, G6, G7) were NOT run here (require `make core-images` + a Docker socket); each lists its expected red reason in its gate entry.

---

## Pass AUTHSTORES-1: concept-doc edits for grant `mode` + `scope` (dry-run + permission)
**Goal:** Apply the spec Design-changes to `concept:dry-run` and `concept:permission` so the
durable design states the restored identity-bound `mode` (a floor the caller cannot escalate past)
and the new `scope` resource-selector field + scope-match invariant, before the conforming code lands.
**Scope:** Two concept docs only; no code. **End state:** working
**Verification:** `cmd:test -run xxx_noop` not applicable — verify by `grep`:
`grep -q 'identity-bound' .ok-planner/design/concepts/dry-run.md && grep -q 'scope' .ok-planner/design/concepts/permission.md && echo OK`
**Proves:** the design surface carries the un-deferred per-grant mode + resource scoping before
the code conforms to it. **Red-when-absent:** the grep prints nothing (no "identity-bound" mode
language in dry-run.md, no "scope"-field invariant in permission.md) — the docs still state the
reversed position.

### Task AUTHSTORES-1.1: dry-run.md — restore grant-mode-as-floor
**Files:** `.ok-planner/design/concepts/dry-run.md`
**Steps:**
1. In `## What it is`, change "The flag is the *only* source of dry-run … the grant carries no mode"
   to: dry-run resolves from EITHER the `?dry_run=true` request flag OR an identity-bound grant
   `mode` (see `concept:permission`); the grant mode is a **floor** — a key whose matched grant
   entry carries `mode: dry_run` runs every covered write in dry-run regardless of the flag, and the
   caller cannot escalate past it (flag-absent or `?dry_run=false` does NOT lift an identity-bound
   `dry_run` floor; the flag can only ADD dry-run on top of an `execute` grant).
2. In `## Boundaries`, change "the request flag is orthogonal to the binary grant" to: the resolved
   mode is the max-restriction of (request flag, matched grant entry's mode); `concept:permission`
   owns the grant `mode` field, `concept:dry-run` owns its resolution + the dry-run branches.
3. Append a `## Notes` entry:
   `- 2026-06-06 — Per spec:2026-06-06-comprehensive-gap-closure-design (S-auth-identity-bound-dryrun): reverses the 2026-05-29 "flag is the only source" decision. Dry-run resolves from EITHER the request flag OR an identity-bound grant mode; the grant mode is a floor the caller cannot escalate past. The per-grant mode modifier (un-deferred in concept:permission) is restored.`
**Verification:** `grep -c 'identity-bound' .ok-planner/design/concepts/dry-run.md` (≥1)

### Task AUTHSTORES-1.2: permission.md — restore grant `mode`, add `scope` field + scope-match invariant
**Files:** `.ok-planner/design/concepts/permission.md`
**Steps:**
1. In `## What it is`, change "each entry is just an **action string** … There is no `mode`
   modifier" to: each entry is `{action, mode?, scope?}` — the action string (wildcard grammar
   below), an optional `mode` (`execute` default | `dry_run`, an identity-bound dry-run floor owned
   by `concept:dry-run`), and an optional `scope` (a resource selector evaluated alongside the
   action match).
2. Add a `## Scope match` section: a `scope` selector restricts the entry to requests whose target
   resource satisfies the selector (e.g. `{template_tag: "analytics"}` restricts a
   `template:register` grant to templates tagged `analytics`). Selector keys are per-action resource
   dimensions; an entry with no `scope` matches any target of its action (today's behavior).
3. In `## Invariants`, change "Set-membership evaluation. A request is allowed iff any grant entry
   matches its action" to: a request is allowed iff some entry's action matches AND that entry's
   scope (if present) is satisfied by the request's target resource. Add an invariant: **Scoped
   entries are least-privilege** — a `scope`-bearing entry allows ONLY requests whose target resource
   satisfies the selector; an out-of-scope request of the same action is denied (403) unless another
   entry independently allows it. Add an invariant: **Grant mode is a floor** — the matched entry's
   `mode` (default `execute`) is the most permissive mode the request may run at; the dry-run flag
   may restrict further but never escalate (see `concept:dry-run`).
4. In `## Boundaries`, remove "per-action *resource* scoping (V2 territory)" from the "Does NOT own"
   list and add resource scoping to "Owns".
5. Append a `## Notes` entry:
   `- 2026-06-06 — Per spec:2026-06-06-comprehensive-gap-closure-design (S-auth-identity-bound-dryrun, S-auth-grant-scope-enforced): restores the per-grant `mode` modifier (an identity-bound dry-run floor) and un-defers the 2026-05-15 V2 deferral of resource scoping — adds a `scope` resource-selector field with scope-match semantics evaluated alongside the action match, plus the least-privilege invariant.`
**Verification:** `grep -c 'Scope match' .ok-planner/design/concepts/permission.md` (≥1)

---

## Pass AUTHSTORES-2: grant entry carries `mode` + `scope` (parser + evaluator)
**Goal:** Restore `Mode` and add `Scope` to `auth.GrantEntry`, parse them out of `Extras`, and make
`CheckGrant` surface the matched entry's mode + evaluate the scope selector — the foundation both
auth stories build on.
**Scope:** `lib/foundation/auth` (grant.go, check.go, new scope evaluator); no controlapi wiring yet.
**End state:** broken-intentional (restored by AUTHSTORES-3 / AUTHSTORES-4 wiring) — the new
`CheckGrant` shape compiles and the unit proof passes, but no handler consumes it yet.
**Verification:** `go test ./lib/foundation/auth/... -run 'TestCheckGrant_ModeFloor|TestCheckGrant_ScopeMatch' -count=1`
**Proves:** the grant parser preserves `mode`/`scope` and the evaluator returns the matched entry's
mode and honors a resource selector — set-membership-with-scope. **Red-when-absent:** today
`GrantEntry` has no `Mode`/`Scope` fields and `CheckGrant.CheckResult` has no `Mode`; the test fails
to compile / the assertions fail because mode is always execute and scope is ignored.
NOTE: this is a foundation-shaping pass; the *observable*-outcome proofs are AUTHSTORES-3/4. This
pass's test is a typed-evaluator unit test, justified only as scaffolding the e2e gates rely on.

### Task AUTHSTORES-2.1: add `Mode` + `Scope` to GrantEntry, parse from JSON
**Files:** `lib/foundation/auth/grant.go`
**Steps:**
1. Add `Mode Mode \`json:"mode,omitempty"\`` and `Scope map[string]string \`json:"scope,omitempty"\``
   fields to `GrantEntry` (keep `Extras` for genuinely-unknown keys).
2. In `UnmarshalJSON`, after extracting `action`, decode a `mode` key into `g.Mode` (validate it is
   `""` | `execute` | `dry_run`; reject other strings with `ErrInvalidGrant`) and a `scope` key into
   `g.Scope`; `delete` both from `raw` so they no longer fall into `Extras`. Default empty `Mode` to
   `ModeExecute` at read time (leave the field empty on the struct; the evaluator defaults it).
3. In `MarshalJSON`, emit `mode` (when non-empty, after `action`) and `scope` (sorted keys) in the
   deterministic key order before `Extras`, preserving byte-stable round-trip.
4. Update the `GrantEntry` doc comment: it now carries an identity-bound `mode` floor and a `scope`
   resource selector consumed by the matcher.
**Verification:** `go build ./lib/foundation/auth/...`

### Task AUTHSTORES-2.2: CheckGrant returns matched mode + honors scope
**Files:** `lib/foundation/auth/check.go`, new `lib/foundation/auth/scope.go`,
new `lib/foundation/auth/check_mode_scope_test.go`
**Steps:**
1. Add a `ScopeMatches(entryScope map[string]string, target map[string]string) bool` evaluator in
   `scope.go`: empty/nil `entryScope` → true (unscoped); otherwise every key in `entryScope` must be
   present in `target` with an equal value (subset-satisfaction). Document the contract.
2. Add `Mode Mode` to `CheckResult`. Change `CheckGrant` signature to
   `CheckGrant(grant Grant, requestAction string, target map[string]string) CheckResult`: iterate
   entries, an entry matches iff `ActionMatches(e.Action, requestAction) && ScopeMatches(e.Scope, target)`;
   on first match return `{Allowed:true, MatchedIdx:i, Mode: e.Mode or ModeExecute}`.
3. Update the existing caller in `auth_middleware.go::gateByAction` (267) to pass a target map
   (empty map for now — AUTHSTORES-4 populates it) and read `res.Mode`. Update any other
   `CheckGrant(` callers + `lib/foundation/auth/check_test.go` to the new signature.
4. Write `TestCheckGrant_ModeFloor` (an entry `{action:"instance:create", mode:"dry_run"}` yields
   `CheckResult.Mode == ModeDryRun`; an `execute`/unset entry yields `ModeExecute`) and
   `TestCheckGrant_ScopeMatch` (entry `{action:"template:register", scope:{template_tag:"analytics"}}`
   allows target `{template_tag:"analytics"}`, denies `{template_tag:"billing"}`, denies missing key).
**Verification:** `go test ./lib/foundation/auth/... -run 'TestCheckGrant_ModeFloor|TestCheckGrant_ScopeMatch' -count=1`

---

## Pass AUTHSTORES-3: identity-bound dry-run resolved from grant mode (RED)
**Goal:** Author the failing end-to-end proof that a key minted with `mode: dry_run` on a write
grant entry previews-but-never-commits even WITHOUT `?dry_run=true`, and that an ordinary
execute-capable key with no flag really commits.
**Scope:** New scenario test in `test/scenarios/auth`. Real assembled control-api over SQLite.
**End state:** broken-intentional (restored green by AUTHSTORES-4)
**Verification:** `! go test ./test/scenarios/auth/ -run TestDryRun_IdentityBoundFloor -count=1`
**Proves:** the test is red TODAY for the right reason — the grant mode is dropped, so the
`mode:dry_run` key actually creates an instance (the assertion that no row was persisted fails).
**Red-when-absent:** without the production change the `mode:dry_run` key's `POST /instances` (no
flag) returns 201 + persisted row instead of the `would_have_created` envelope. (No Docker needed —
SQLite fixture.)

### Task AUTHSTORES-3.1: write TestDryRun_IdentityBoundFloor
**Files:** `test/scenarios/auth/dry_run_identity_bound_test.go`
**Steps:**
1. Using `newAuthFixture`, mint admin (anonymous path), then mint a key with grant
   `[{action:"instance:create", mode:"dry_run"}, {action:"*:read"}]`. Register+deploy a 1-node
   template via the admin key (reuse the `seedDryRunNode` template shape: name/version/
   frame_resolution_mode + one `{type:"n1"}` node).
2. With the `mode:dry_run` key, `POST /instances` with `{template:<hash>}` and **no** `?dry_run`
   flag. Assert HTTP 200, body has `dry_run:true` and a `would_have_created` object with
   `instance_id == "dry-run-not-persisted"`. Then `GET /instances` (admin key) and assert no new
   instance row exists. Then read the latest `auth.access_attempted` event (via
   `f.db.Tables().Events().List`, kind `auth.EventAccessAttempted`) for action `instance:create` and
   assert `executed == false`, `mode == "dry_run"`.
3. Mint a SECOND ordinary key with grant `[{action:"instance:create"}, {action:"*:read"}]` (no mode).
   `POST /instances` with no flag; assert 201 and that `GET /instances` now shows the created
   instance — proving attempt-only is carried by the first key's identity, not the request flag.
**Verification:** `! go test ./test/scenarios/auth/ -run TestDryRun_IdentityBoundFloor -count=1`
(expect FAIL today)

---

## Pass AUTHSTORES-4: wire grant mode into the request-mode resolution (GREEN)
**Goal:** Make `gateByAction` resolve the per-request mode as the floor of (grant mode, dry-run
flag) so an identity-bound `dry_run` grant forces preview and the flag can only add dry-run.
**Scope:** `lib/control/controlapi/auth_middleware.go`. **End state:** working
**Verification:** `go test ./test/scenarios/auth/ -run TestDryRun_IdentityBoundFloor -count=1`
**Proves:** AUTHSTORES-3's named test now PASSES — the `mode:dry_run` key previews without the flag,
the ordinary key commits, and the audit row records `executed:false` for the floored write.
**Red-when-absent:** revert the resolution change and the test flips back to red (the floor is
ignored). RUN this gate against the current tree after the edit; report exit 0.

### Task AUTHSTORES-4.1: resolve mode = floor(grant mode, flag)
**Files:** `lib/control/controlapi/auth_middleware.go`
**Steps:**
1. In `gateByAction`, after `res := auth.CheckGrant(...)` (now returning `res.Mode`), compute the
   effective mode: start from `res.Mode` (default `execute`); if `r.URL.Query().Get("dry_run")=="true"`
   set `dry_run`; the grant `dry_run` is a floor (once `res.Mode == ModeDryRun` the flag's
   absence/`false` cannot lift it). Net rule: `mode = ModeDryRun` iff `res.Mode == ModeDryRun ||
   flag==true`, else `ModeExecute`.
2. Update the comment block at 299-305 (currently "the request mode is the per-request flag, not a
   grant-entry modifier") to describe the floor semantics + the restored `@concept: permission`
   mode field; keep the `@concept: dry-run` annotation.
**Verification:** `go test ./test/scenarios/auth/ -run TestDryRun_IdentityBoundFloor -count=1`

---

## Pass AUTHSTORES-5: grant scope enforced at the permission gate (RED)
**Goal:** Author the failing proof that a scoped write grant denies an out-of-scope request (403 +
`auth.access_denied` audit) while allowing the in-scope one, with persisted state confirming the
out-of-scope resource was never created.
**Scope:** New scenario test in `test/scenarios/auth`. Real assembled control-api over SQLite.
**End state:** broken-intentional (restored green by AUTHSTORES-6)
**Verification:** `! go test ./test/scenarios/auth/ -run TestGrantScope_TemplateTagEnforced -count=1`
**Proves:** red TODAY for the right reason — scope is parsed into `Extras` and ignored, so the
out-of-scope `template:register` succeeds (the 403 + no-persist assertion fails).
**Red-when-absent:** without scope wiring the `billing`-tagged register returns 201 + persisted row
instead of 403. (No Docker — SQLite fixture.)

### Task AUTHSTORES-5.1: write TestGrantScope_TemplateTagEnforced
**Files:** `test/scenarios/auth/grant_scope_test.go`
**Steps:**
1. Using `newAuthFixture`, mint admin; mint a key with grant
   `[{action:"template:register", scope:{template_tag:"analytics"}}, {action:"*:read"}]`.
2. In-scope: `POST /templates` (scoped key) with body `{spec:{name,version,frame_resolution_mode,
   nodes:[{type:n1}]}, tag:"analytics"}`. Assert 201 and that `GET /templates` (admin) shows the
   template + tag.
3. Out-of-scope: `POST /templates` (scoped key) with the same spec but `tag:"billing"`. Assert HTTP
   403 (permission denied on scope, not action). Read the latest `auth.access_denied` event for
   action `template:register` and assert `denial_reason == "permission_denied"`. Assert via admin
   `GET /templates?tag=billing` (or the tags surface) that no `billing`-tagged template was created.
**Verification:** `! go test ./test/scenarios/auth/ -run TestGrantScope_TemplateTagEnforced -count=1`
(expect FAIL today)

---

## Pass AUTHSTORES-6: populate the request target + enforce scope (GREEN)
**Goal:** Have `gateByAction` build the request's target-resource map and pass it to `CheckGrant`
so the scope evaluator rejects out-of-scope writes — least-privilege delegation made real.
**Scope:** `lib/control/controlapi/auth_middleware.go` + a small per-action target extractor.
**End state:** working
**Verification:** `go test ./test/scenarios/auth/ -run 'TestGrantScope_TemplateTagEnforced|TestDryRun_IdentityBoundFloor' -count=1`
**Proves:** AUTHSTORES-5's named test PASSES — in-scope 201, out-of-scope 403 + `auth.access_denied`,
no out-of-scope row; and AUTHSTORES-4 still green (no regression). **Red-when-absent:** revert the
target-extraction and the scope test flips red (scope ignored → 201). RUN this gate; report exit 0.

### Task AUTHSTORES-6.1: build the request target map and pass to CheckGrant
**Files:** `lib/control/controlapi/auth_middleware.go`, new
`lib/control/controlapi/auth_request_target.go`
**Steps:**
1. Add `requestTarget(action string, body []byte, r *http.Request) map[string]string` in the new
   file: for `template:register` parse the `tag` field from the (already-captured) JSON body into
   `{template_tag: <tag>}` (and any additional tags into the map's value space — for V1 a single
   `template_tag` selector is sufficient per the spec's example). Return an empty map for actions
   with no scopeable dimension (unscoped grants still match by the empty-scope rule).
2. In `gateByAction`, after `captureBody` yields `body`, compute `target := requestTarget(action,
   body, r)` and call `auth.CheckGrant(ident.Permissions, action, target)`. Keep the existing
   `emitDenied(..., DenialPermissionDenied)` path on `!res.Allowed` so the out-of-scope denial lands
   an `auth.access_denied` audit row.
3. Add the `@concept: permission` annotation on the new file noting it implements the scope-match
   target extraction.
**Verification:** `go test ./test/scenarios/auth/ -run 'TestGrantScope_TemplateTagEnforced|TestDryRun_IdentityBoundFloor' -count=1`

---

## Pass AUTHSTORES-7: 9b serialization probe in the conformance suite (RED)
**Goal:** Author the failing proof that the claim-producer conformance suite, driven through the
REAL `claimproducer.Run` against two real producers over gRPC, reports `ok` for an honest
snapshot-delegating `staged_async` producer and `FAIL` (naming invariant 9b) for a producer that
internally serializes concurrent reader Opens behind an open writer.
**Scope:** New conformance scenario test under `lib/services/test/scenarios` driving two in-test
gRPC producers; the test asserts on the NOT-YET-ADDED `Serialization9b` check row.
**End state:** broken-intentional (restored green by AUTHSTORES-8)
**Verification:** `! go test ./lib/services/test/scenarios/conformance_9b/ -run TestClaimProducer9b_Probe -count=1`
**Proves:** red TODAY because `runner.go::Run` emits no 9b check — the test's assertion that the
results contain a `Serialization9b` row (ok for A, FAIL for B) finds no such row.
**Red-when-absent:** missing the probe, the dishonest producer B passes the suite clean. (Docker:
the producers are in-test gRPC servers — `bufconn` or a local TCP listener, no testcontainers —
but the conformance scenario module sits under `lib/services/test`; gate as a Go test, no images.)

### Task AUTHSTORES-7.1: in-test honest + dishonest staged_async producers
**Files:** `lib/services/test/scenarios/conformance_9b/producers_test.go`
**Steps:**
1. Implement two `claimproducer.ClaimProducer` Go types that both advertise
   `WriteSemanticsAllowed:[staged_async]` and `RealizedWriteSemantics: staged_async` on Open:
   - **honest**: Open returns promptly for every call (snapshot delegation simulated — no internal
     lock held across the writer's lifetime); two concurrent reader Opens against an open writer
     both return promptly.
   - **dishonest**: holds an internal `sync.Mutex` (or a "writer open" gate) for the writer's claim
     lifetime such that a second reader `Open` on the same scope blocks until the writer's claim is
     terminal-ed (Release/Commit/Abandon) — the reader-lease serialization 9b forbids.
2. Stand each up on a gRPC server (`genv1.RegisterClaimProducerServer` over `serverkit.Listen` on a
   local port, mirroring `examples/claimproducer/main.go`) so the probe drives them over the wire.
**Verification:** `go build ./lib/services/test/scenarios/conformance_9b/...`

### Task AUTHSTORES-7.2: write TestClaimProducer9b_Probe asserting the (absent) 9b row
**Files:** `lib/services/test/scenarios/conformance_9b/probe_test.go`
**Steps:**
1. Dial producer A (honest) via `harness.DialClaimProducer`; run `cpconf.Run(ctx, clientA)`; assert
   the results contain a check named `Serialization9b` with `Err == nil`.
2. Dial producer B (dishonest); run `cpconf.Run(ctx, clientB)`; assert the results contain
   `Serialization9b` with `Err != nil` and the error message names invariant `9b`.
**Verification:** `! go test ./lib/services/test/scenarios/conformance_9b/ -run TestClaimProducer9b_Probe -count=1`
(expect FAIL today — no `Serialization9b` row exists)

---

## Pass AUTHSTORES-8: add the 9b serialization check to the runner (GREEN)
**Goal:** Add a `Serialization9b` check to `claimproducer.Run` that, for `staged_async`-advertising
producers, opens a writer claim and then fires two concurrent reader Opens on the same scope,
failing (naming invariant 9b) if a reader Open blocks until the writer terminal-s.
**Scope:** `lib/protocols/conformance/claimproducer/runner.go` (+ a focused check helper file).
Update the CLI wrapper docstring to stop over/under-claiming. **End state:** working
**Verification:** `go test ./lib/services/test/scenarios/conformance_9b/ -run TestClaimProducer9b_Probe -count=1`
**Proves:** AUTHSTORES-7's named test PASSES — honest A reports `ok`, dishonest B reports `FAIL`
with a 9b-named message, driven through the real runner over gRPC. **Red-when-absent:** delete the
check and the probe test reverts to red. RUN this gate; report exit 0.

### Task AUTHSTORES-8.1: implement checkSerialization9b
**Files:** `lib/protocols/conformance/claimproducer/serialization9b.go`,
`lib/protocols/conformance/claimproducer/runner.go`
**Steps:**
1. Add `checkSerialization9b(ctx, c, caps) CheckResult`: SKIP (return `{Name:"Serialization9bSkipped"}`)
   unless `caps.Contains(WriteSemanticsStagedAsync)`. Otherwise: Open a writer claim (IntentReadWrite)
   on a synthetic scope; with the writer still open, launch two goroutines each calling `c.Open` with
   IntentRead on the SAME byte-equal scope under a bounded timeout (e.g. 2s). If either reader Open
   does not return within the timeout while the writer is open → fail with an error message naming
   `@blessed-invariant 9b` (reader-lease serialization forbidden for staged_async). Then release the
   writer + readers cleanly (Release/Abandon) so the producer state is consistent. On both readers
   returning promptly → `{Name:"Serialization9b"}` (ok).
2. Call `checkSerialization9b` from `Run` on a path GUARANTEED to run whenever the producer
   advertises `staged_async` — NOT after the Uniformity block. Grounded (2026-06-07):
   `runner.go::Run` has many early-returns BEFORE the Uniformity block (e.g. the drain
   carve-out `if !out2.Available { … runOptionalChecks; return }` at `runner.go:107-113`,
   plus the OpenFirst/OpenSecond error returns at `#94-105`), so a real drain/pick-policy
   producer returns before reaching line 135 and the 9b check would never fire. Place it
   instead inside `runOptionalChecks` (the universal funnel — it is appended on EVERY return
   path in `Run`), OR immediately after the `EnvelopeNonEmpty` row is recorded (`#56`),
   capability-gated on `caps.Contains(WriteSemanticsStagedAsync)`. Folding it into
   `runOptionalChecks` (alongside the existing SplitScope / ScopesConflict probes, which are
   already capability-gated SKIP-or-run checks) is the cleanest seam and matches the file's
   pattern. (Drop the prior "append after the Uniformity block" wording — it was wrong: the
   drain early-return at `runner.go:107-113` sits before it.)
3. Update the CLI wrapper docstring at `cmd/rimsky/conformance.go` (~163) to accurately list the 9b
   probe alongside the existing checks (do not over-claim the four runtime verbs — that is the sibling
   `S-conformance-claimproducer-terminals` story; only correct text this story touches).
**Verification:** `go test ./lib/services/test/scenarios/conformance_9b/ -run TestClaimProducer9b_Probe -count=1`

---

## Pass AUTHSTORES-9: producer ScopesConflict consulted in acquisition + sub-claim path (RED)
**Goal:** Author the failing full-stack proof that a producer advertising `SupportsScopesConflict`
with a prefix-overlap predicate actually blocks a second writer whose scope overlaps (but is NOT
byte-equal) the first, both on the top-level acquisition path and the fan-out sub-claim path.
**Scope:** New full-stack scenario under `lib/services/test/scenarios` (a custom overlap producer +
two nodes acquiring overlapping scopes through a real rimsky stack). **End state:** broken-intentional
(restored green by AUTHSTORES-10)
**Verification:** `! go test ./lib/services/test/scenarios/scopes_conflict/ -run TestScopesConflict_OverlapHeldOff -count=1`
**Proves:** red TODAY because `evaluateClaimScopeConflict` compares only byte-equal — two
prefix-overlapping non-byte-equal scopes both acquire, so the "only one acquires" assertion fails.
**Red-when-absent:** without the wiring both overlapping writers hold simultaneously, violating
invariant 4b. (Docker: real rimsky stack via testcontainers + the overlap producer over gRPC; run
`make core-images` + `make service-images` first.)

### Task AUTHSTORES-9.1: overlap producer + overlapping-scopes template
**Files:** `lib/services/test/scenarios/scopes_conflict/overlap_producer_test.go`,
`lib/services/test/scenarios/scopes_conflict/scopes_conflict_test.go`
**Steps:**
1. Implement a `claimproducer.ClaimProducer` advertising `SupportsScopesConflict:true` whose
   `ScopesConflict(a,b)` returns true when either scope's selector string is a prefix of the other
   (prefix-containment), e.g. `tenant/a/*` vs `tenant/a/x`. Stand it up on a gRPC server reachable
   on the harness network (a new `harness` helper `StartCustomClaimProducer` that runs an in-test
   gRPC server bound on a container-reachable host port, or run the producer in-process and expose
   it via `WithHostPortAccess`; the simplest wired form is an in-test gRPC server + the rimsky
   container dialing it via host-gateway — capture whichever the harness supports).
2. Build a template with two nodes acquiring claims on overlapping (non-byte-equal) scopes against
   that producer (`scope` selectors `tenant/a/x` and `tenant/a/y` where the producer treats them as
   overlapping by a shared `tenant/a` prefix — choose selectors so the predicate fires).
3. `TestScopesConflict_OverlapHeldOff`: create+run the instance; assert via the node-state
   observability surface / `claim_handles` that only ONE node acquires (the second is held-off /
   routed unavailable), proving `ScopesConflict` was consulted.
**Verification:** `! go test ./lib/services/test/scenarios/scopes_conflict/ -run TestScopesConflict_OverlapHeldOff -count=1`
(expect FAIL today)

### Task AUTHSTORES-9.2: fan-out sub-claim overlap case
**Files:** `lib/services/test/scenarios/scopes_conflict/scopes_conflict_test.go`
**Steps:**
1. Add a fan-out parent whose `SplitScope` yields two overlapping sub-scopes (the same overlap
   predicate). Drive the instance and assert the acquisition tx does NOT commit both overlapping
   sub-claim rows (the conflicting sub-claim is rejected) — observable via the `claim_handles`
   sub-claim rows / event log.
2. Fold this into the named test as a sub-case (or a second `t.Run`) so the same
   `! go test … -run TestScopesConflict_OverlapHeldOff` gate covers both paths.
**Verification:** `! go test ./lib/services/test/scenarios/scopes_conflict/ -run TestScopesConflict_OverlapHeldOff -count=1`

---

## Pass AUTHSTORES-10: call producer ScopesConflict in acquire + sub-claim (GREEN)
**Goal:** Make `evaluateClaimScopeConflict` and the sub-claim INSERT path consult the producer's
`ScopesConflict` (via the resolved producer / `peer.Client`) when the producer advertises it,
falling back to byte-equal otherwise — enforcing invariant 4b for non-trivial overlap.
**Scope:** `lib/runtime/runner_acquire_claims.go`, `lib/runtime/runner_subclaim.go`.
**End state:** working
**Verification:** `go test ./lib/services/test/scenarios/scopes_conflict/ -run TestScopesConflict_OverlapHeldOff -count=1`
**Proves:** AUTHSTORES-9's named test PASSES — only one of two overlapping writers acquires, and the
conflicting fan-out sub-claim is rejected, with the producer's `ScopesConflict` invoked.
**Red-when-absent:** revert to byte-equal-only and both overlapping writers acquire (test red). RUN
this gate; report exit 0. ALSO run `go test ./lib/runtime/... -race -count=1` (the conflict check
sits in the acquisition tx, a race-sensitive path).

### Task AUTHSTORES-10.1: consult ScopesConflict in evaluateClaimScopeConflict
**Files:** `lib/runtime/runner_acquire_claims.go`
**Steps:**
1. Thread the resolved producer `s` (already in scope in `acquireClaim`) into
   `evaluateClaimScopeConflict` (or fetch its `Capabilities`). For each existing holder row, when the
   producer advertises `SupportsScopesConflict`, call `s.ScopesConflict(ctx, candidateScope,
   h.ClaimScopeData)` instead of `locks.ClaimScopesByteEqual`; on a producer that does not advertise,
   keep the byte-equal path (current behavior). Preserve the existing `ModeCoexists` matrix check on
   a positive conflict. Keep the same-node-skip branch.
2. Update the function's doc comment + the acquireClaim comment block (29-44) to state producer-aware
   conflict per invariant 4b; add/confirm the `@blessed-invariant 4b` annotation at the conflict
   site.
**Verification:** `go build ./lib/runtime/... && go test ./lib/runtime/... -run TestAcquire -count=1`

### Task AUTHSTORES-10.2: conflict-check sub-claim INSERTs
**Files:** `lib/runtime/runner_subclaim.go`
**Steps:**
1. In `AcquireSubClaims`, before INSERTing each sub-claim row, run the producer-aware conflict check
   (byte-equal fallback; producer `ScopesConflict` when advertised) against already-held / already-
   INSERTed sibling sub-claim scopes for the same producer; reject (abort the acquisition tx, so no
   sibling sub-claim rows commit) when a conflict is found. Keep atomicity per invariant 10 (the
   whole sub-claim wave is one tx).
2. Add the `@blessed-invariant 4b` annotation at the new sub-claim conflict site.
**Verification:** `go test ./lib/services/test/scenarios/scopes_conflict/ -run TestScopesConflict_OverlapHeldOff -count=1 && go test ./lib/runtime/... -race -count=1`

---

## Pass AUTHSTORES-11: fs-store explicit-sync admin route (RED)
**Goal:** Author the failing store-level proof that a `sync_strategy: explicit` policy, after the
queue drains (Open → Unavailable), re-admits a newly-dropped folder ONLY after a `POST
/admin/sync/{selector}` call, observed via a subsequent gRPC Open returning Available with the new
folder.
**Scope:** New store test in `lib/services/stores/filesystem/store`. **End state:** broken-intentional
(restored green by AUTHSTORES-12)
**Verification:** `! go test ./lib/services/stores/filesystem/store/ -run TestAdminSync_ExplicitReadmits -count=1`
**Proves:** red TODAY — `AdminHandler` registers no sync route, so the POST 404s and the post-sync
Open stays Unavailable (no operator path to re-admit a drained explicit queue).
**Red-when-absent:** without the route the dropped folder is never claimable. (No Docker — the store
package test drives the in-process `Store` + its `AdminHandler` directly via `httptest`.)

### Task AUTHSTORES-11.1: write TestAdminSync_ExplicitReadmits
**Files:** `lib/services/stores/filesystem/store/admin_sync_test.go`
**Steps:**
1. Build a `Store` with one pick policy at `sync_strategy: explicit`, `on_commit: pop`,
   `on_give_up: pop`, rooted at a temp dir seeded with one folder. Open once (claim the seeded
   folder), Open again → assert Available:false (drained, no auto-sync).
2. Drop a NEW folder into the policy root on disk. `POST` to the (not-yet-existing) admin sync route
   `/admin/sync/{selector}` via `httptest.NewServer(s.AdminHandler())`; assert a 2xx success.
3. Open again over the producer interface; assert Available:true and the returned address/scope
   corresponds to the new folder — the operator-triggered refresh made it claimable.
**Verification:** `! go test ./lib/services/stores/filesystem/store/ -run TestAdminSync_ExplicitReadmits -count=1`
(expect FAIL today)

---

## Pass AUTHSTORES-12: implement the admin sync route (GREEN)
**Goal:** Register `POST /admin/sync/{selector}` on the fs-store admin handler, invoking `runSync`
for the named policy on demand, so a drained explicit/never queue can be re-primed without redeploy.
**Scope:** `lib/services/stores/filesystem/store/admin.go` (+ expose runSync if needed).
**End state:** working
**Verification:** `go test ./lib/services/stores/filesystem/store/ -run TestAdminSync_ExplicitReadmits -count=1`
**Proves:** AUTHSTORES-11's named test PASSES — the sync POST 2xx, the post-sync gRPC Open returns
the new folder Available. **Red-when-absent:** remove the route and the test reverts to red (404 →
Unavailable). RUN this gate; report exit 0.

### Task AUTHSTORES-12.1: add the sync route + runSync invocation
**Files:** `lib/services/stores/filesystem/store/admin.go`,
`lib/services/stores/filesystem/store/pick_policy.go`
**Steps:**
1. Register a `POST /admin/sync/` handler in `AdminHandler`: unescape the selector path-param,
   resolve it against `s.pickPolicies` (400 on unknown selector), call `s.runSync(selector, pp)`
   (200/204 on success, 500 on error). Mirror the existing bump-to-head handler's selector decoding
   and method/encoding guards.
2. If a future cross-package caller needs it, leave `runSync` unexported (the admin handler is a
   method on `*Store`, so it can call the unexported helper directly).
**Verification:** `go test ./lib/services/stores/filesystem/store/ -run TestAdminSync_ExplicitReadmits -count=1`

---

## Pass AUTHSTORES-13: recover the fs atomic-staging reference producer + example (RED)
**Goal:** Author the failing example-suite proof that a copyable filesystem atomic-staging reference
producer (stage-at-Open, atomic-rename-on-Commit, drop-on-Abandon) builds and behaves per the
stage-then-swap contract.
**Scope:** New `examples/atomic-staging-fs-producer/` (recovered from git) + its behavioral test in
the `examples` module. **End state:** broken-intentional (restored green by AUTHSTORES-14)
**Verification:** `! (cd examples && go test ./atomic-staging-fs-producer/... -count=1)`
**Proves:** red TODAY — the directory does not exist in the current tree (deleted in `c1ce756`), so
the example test target is missing / fails to build.
**Red-when-absent:** `examples/` ships no atomic-staging reference producer. (No Docker — the
behavioral test exercises the producer in-process / over bufconn with POSIX rename on a temp dir.)

### Task AUTHSTORES-13.1: recover the producer source from git
**Files:** `examples/atomic-staging-fs-producer/` — recover EVERY file under that directory from `c1ce756^`.
**Steps:**
1. Recover EVERY file under `examples/atomic-staging-fs-producer/` from `c1ce756^` (do NOT cherry-pick a subset — the store package needs all of its files to build). Confirm the full set first with `git ls-tree -r --name-only c1ce756^ -- examples/atomic-staging-fs-producer/`; the verified complete set (9 files) is:
   - `cmd/main.go`
   - `server/server.go`
   - `store/store.go`
   - `store/fs_check_unix.go` — `//go:build unix`; defines `assertSameFilesystem` (compares `st_dev` to guard rename(2) atomicity), called by `store/store.go:67`.
   - `store/fs_check_other.go` — the non-unix build-tag sibling of `fs_check_unix.go` (same symbol, fallback impl).
   - `sweep/sweep.go`
   - `sweep/sweep_test.go`
   - `README.md`
   - `template.yaml`
   Recover each via `git show c1ce756^:examples/atomic-staging-fs-producer/<path> > examples/atomic-staging-fs-producer/<path>` (create the `cmd/`, `server/`, `store/`, `sweep/` subdirs first). Omitting `store/fs_check_unix.go` / `store/fs_check_other.go` leaves `assertSameFilesystem` undefined and the package fails to build.
2. Adapt imports + module wiring to the current `examples/go.mod` + `lib/...` import paths (the
   producer must speak the current `claimproducer` protocol — Open reserves a private staging dir,
   Commit renames staging into the canonical view, Abandon discards staging). Drop any doc paths that
   pointed at the removed `docs/` tree; keep the README as the copyable pattern doc co-located with
   the example.
**Verification:** `(cd examples && go build ./atomic-staging-fs-producer/...)`

### Task AUTHSTORES-13.2: behavioral test pinning the stage-then-swap contract
**Files:** `examples/atomic-staging-fs-producer/atomic_staging_test.go`
**Steps:**
1. In the shape of the other example behavioral tests (`examples/claimproducer/claimproducer_test.go`),
   exercise the producer in-process: Open reserves a staging dir (assert it exists, canonical path
   absent); write a file into staging; Commit → assert the file is now at the canonical path (atomic
   rename) and staging is gone; a fresh Open + Abandon → assert the canonical path is unchanged and
   staging discarded.
**Verification:** `! (cd examples && go test ./atomic-staging-fs-producer/... -count=1)`
(expect FAIL today — the package does not yet exist)

---

## Pass AUTHSTORES-14: example builds + passes under the workspace gate (GREEN)
**Goal:** Make the recovered example compile and its behavioral test pass under the `examples`
module gate.
**Scope:** `examples/atomic-staging-fs-producer/` finalization. **End state:** working
**Verification:** `(cd examples && go build ./... && go test ./atomic-staging-fs-producer/... -count=1)`
**Proves:** AUTHSTORES-13's named test PASSES — Commit performs a real POSIX rename into the
canonical view, Abandon leaves it unchanged; the example builds in-module. **Red-when-absent:**
introduce a bug in the rename (e.g. copy-without-remove) and the contract test fails. RUN this gate;
report exit 0. ALSO run `(cd examples && golangci-lint run ./atomic-staging-fs-producer/...)`.

### Task AUTHSTORES-14.1: resolve build/lint + green the contract test
**Files:** `examples/atomic-staging-fs-producer/store/store.go` (and siblings as needed)
**Steps:**
1. Fix any residual compile/lint issues from the recovery (unused imports, renamed protocol symbols,
   the current `OpenOutcome`/`ClaimResult` shape with `RealizedWriteSemantics: staged_async`). Ensure
   Open advertises `staged_async`, Commit does the atomic `os.Rename`, Abandon does `os.RemoveAll` of
   the staging dir.
2. Confirm the behavioral test asserts the swap is a real rename (staging gone, canonical present).
**Verification:** `(cd examples && go build ./... && go test ./atomic-staging-fs-producer/... -count=1)`

---

## Pass AUTHSTORES-15: pgstore atomic-staging substrate — staging schema + swap + swap_failed (RED)
**Goal:** Author the failing store-level proof (testcontainers Postgres) that the pg store, on a
staged-write claim, reserves a staging schema at Open, atomically swaps it into the canonical schema
at Commit, discards it at Abandon, and emits `pg/swap_failed` when the swap collides.
**Scope:** New testcontainers-driven test in `lib/services/stores/postgres/store` (and
`.../server` for the swap_failed emit). **End state:** broken-intentional (restored green by
AUTHSTORES-16)
**Verification:** `! go test ./lib/services/stores/postgres/store/ -run TestAtomicStaging_SchemaSwap -count=1`
**Proves:** red TODAY — `Open` returns the selector verbatim (no schema reservation),
`Commit`/`Abandon` are no-ops for scope-bytes claims, and `pg/swap_failed` has zero emit sites.
**Red-when-absent:** no staging schema is created, no swap occurs, no swap_failed signal. (Docker:
`harness.StartFreshPostgres` / the package's testcontainers pattern — Docker socket required.)

### Task AUTHSTORES-15.1: write TestAtomicStaging_SchemaSwap (commit + abandon + swap_failed)
**Files:** `lib/services/stores/postgres/store/atomic_staging_test.go`
**Steps:**
1. Boot a real Postgres (the package's testcontainers helper, mirroring
   `server/executor_test.go`). Configure the store for staged write-semantics on a scope-bytes
   claim. Open a claim; assert a staging schema is created/reserved (query
   `information_schema.schemata`).
2. Write rows into the staging schema (as a real executor would, via a direct INSERT through the
   pool). Drive Commit; assert the canonical schema atomically reflects the staged rows AND the
   staging schema is gone.
3. Open a second claim; Abandon; assert the canonical schema is unchanged and the staging schema is
   discarded.
4. Force a swap collision (e.g. a pre-existing object in the canonical target so the rename/replace
   fails); drive Commit; assert it surfaces a `pg/swap_failed` error class (assert on the error /
   emitted class the store returns). [The subscriber-delivery surface is asserted in AUTHSTORES-19's
   acceptance gate; here pin the class at the store boundary.]
**Verification:** `! go test ./lib/services/stores/postgres/store/ -run TestAtomicStaging_SchemaSwap -count=1`
(expect FAIL today)

---

## Pass AUTHSTORES-16: implement pgstore staging-schema lifecycle + swap (GREEN)
**Goal:** Implement Open-reserves-staging-schema, Commit-atomic-swap, Abandon-discard, and
`pg/swap_failed` on collision for staged-write scope-bytes claims.
**Scope:** `lib/services/stores/postgres/store/store.go` (+ a staging helper file),
`lib/services/stores/postgres/server/executor.go` for the swap_failed class. **End state:** working
**Verification:** `go test ./lib/services/stores/postgres/store/ -run TestAtomicStaging_SchemaSwap -count=1`
**Proves:** AUTHSTORES-15's named test PASSES — staging schema created at Open, canonical reflects
staged rows after Commit (staging gone), Abandon discards staging (canonical unchanged), collision
yields `pg/swap_failed`. **Red-when-absent:** revert the swap and Commit becomes a no-op (test red).
RUN this gate; report exit 0.

### Task AUTHSTORES-16.1: staging-schema reservation + swap + discard
**Files:** `lib/services/stores/postgres/store/store.go`,
`lib/services/stores/postgres/store/staging.go`
**Steps:**
1. For a staged-write scope-bytes claim, `Open` creates a per-claim staging schema
   (`CREATE SCHEMA <staging_name>`), returns its identity in the Address/ClaimScope so the executor
   writes into it; record the staging name keyed by claim_id.
2. `Commit` performs an atomic schema swap into the canonical schema in one tx (drop/rename per the
   substrate-atomic pattern documented in `concept:atomic-staging` — "Postgres schema swap: atomic
   via transaction"); on failure (collision) return a `pg/swap_failed`-classed error and leave
   staging intact. `Abandon` drops the staging schema (`DROP SCHEMA ... CASCADE`). `Release` cleans
   up any residual staging state.
3. Keep the existing pick-policy Commit/Abandon paths unchanged (this is the scope-bytes/staged
   branch only).
**Verification:** `go build ./lib/services/stores/postgres/... && go test ./lib/services/stores/postgres/store/ -run TestAtomicStaging_SchemaSwap -count=1`

### Task AUTHSTORES-16.2: emit pg/swap_failed from the store boundary
**Files:** `lib/services/stores/postgres/store/store.go` (Commit error class),
`lib/services/stores/postgres/server/executor.go` (if the swap is reachable through the executor role)
**Steps:**
1. Ensure the swap-collision path produces the `pg/swap_failed` class (already declared in
   `declaredErrorClasses`) at whatever wire boundary the supervisor routes claim-terminal errors
   through. Confirm `pg/swap_failed` is no longer a zero-emit-site class.
**Verification:** `go test ./lib/services/stores/postgres/store/ -run TestAtomicStaging_SchemaSwap -count=1`

---

## Pass AUTHSTORES-17: pgstore row_count_ratio check (RED)
**Goal:** Author the failing testcontainers proof that the SQL store substrate compiles + executes a
`row_count_ratio` check as an aggregate-only query — Success in-bounds, `pg/verifier_check_failed/
row_count_ratio` Error out-of-bounds with the computed ratio in the payload.
**Scope:** New cases in the pg executor testcontainers test. **End state:** broken-intentional
(restored green by AUTHSTORES-18)
**Verification:** `! go test ./lib/services/stores/postgres/server/ -run TestExecutor_RowCountRatio -count=1`
**Proves:** red TODAY — `sqlchecks.Compile` falls through to `unknown check kind "row_count_ratio"`,
mapped to `pg/attribute_invalid`, so the in-bounds dispatch errors instead of succeeding.
**Red-when-absent:** the SQL store cannot run a `row_count_ratio` check the in-process verifier
ships. (Docker: pg `executor_test.go` boots real Postgres via testcontainers.)

### Task AUTHSTORES-17.1: write TestExecutor_RowCountRatio (pass + fail)
**Files:** `lib/services/stores/postgres/server/executor_test.go`
**Steps:**
1. Seed a real table to a known row count; dispatch via `executeCore` with attributes declaring a
   `row_count_ratio` check (`config:{baseline:<N>, low:0.5, high:2.0}`) so the ratio is in-bounds;
   assert a Success StreamClose.
2. Seed/declare the baseline so the ratio is out-of-bounds; dispatch; assert an Error StreamClose
   with `error_class == "pg/verifier_check_failed/row_count_ratio"` and the computed ratio present in
   the failure payload.
**Verification:** `! go test ./lib/services/stores/postgres/server/ -run TestExecutor_RowCountRatio -count=1`
(expect FAIL today)

---

## Pass AUTHSTORES-18: compile row_count_ratio in the shared sql-checks (GREEN)
**Goal:** Add a `row_count_ratio` compiler to the shared SQL check vocabulary — one aggregate-only
`SELECT count(*)`, ratio computed in `Interpret` against the config baseline — matching the
in-process config keys (`baseline` required positive, `low` default 0.5, `high` default 2.0).
**Scope:** `lib/services/stores/shared/sql-checks/compile.go`. **End state:** working
**Verification:** `go test ./lib/services/stores/postgres/server/ -run TestExecutor_RowCountRatio -count=1`
**Proves:** AUTHSTORES-17's named test PASSES — in-bounds Success, out-of-bounds
`pg/verifier_check_failed/row_count_ratio` with the ratio. **Red-when-absent:** remove the case arm
and the compiler falls through to unknown-kind (test red). RUN this gate; report exit 0. ALSO run
`go test ./lib/services/stores/shared/sql-checks/... -count=1` (the SELECT-only enforcement test
must still pass on the new query).

### Task AUTHSTORES-18.1: add compileRowCountRatio
**Files:** `lib/services/stores/shared/sql-checks/compile.go`
**Steps:**
1. Add `case "row_count_ratio": out, err = compileRowCountRatio(spec.Config, schema, table)` to the
   `Compile` switch.
2. Implement `compileRowCountRatio`: require `config.baseline` (numeric, > 0; error otherwise); read
   `low` (default 0.5) and `high` (default 2.0) via `numericDefault`. SQL = `SELECT count(*) FROM
   s.t` (aggregate-only, SELECT-prefixed). `Interpret`: compute `ratio = count/baseline`; Pass iff
   `low <= ratio <= high`; put `row_count`, `baseline`, `low`, `high`, and the computed `ratio` into
   `Counts`; on fail set a descriptive `Message`. Match the in-process `runRowCountRatio` config keys
   exactly so SQL-side and shape-side vocabularies stay coherent.
**Verification:** `go test ./lib/services/stores/shared/sql-checks/... -count=1 && go test ./lib/services/stores/postgres/server/ -run TestExecutor_RowCountRatio -count=1`

---

## Pass AUTHSTORES-19: pgstore claim_unavailable + swap_failed reach a subscriber (RED)
**Goal:** Author the failing full-stack proof that `pg/claim_unavailable` (empty pick-policy items
table / unopenable claim) and `pg/swap_failed` (atomic-staging swap collision) actually fire as
signals an operator-declared `error_types` entry / subscriber receives.
**Scope:** New full-stack scenario under `lib/services/test/scenarios` driving the real pg store +
real rimsky stack + a subscriber. **End state:** broken-intentional (restored green by
AUTHSTORES-20)
**Verification:** `! go test ./lib/services/test/scenarios/pg_error_classes/ -run TestPGErrorClasses_Delivered -count=1`
**Proves:** red TODAY — empties/conflicts surface only via `OpenResponse_Unavailable` and
`pg/swap_failed` has no emit site, so neither class is delivered to a subscriber.
**Red-when-absent:** the two declared classes carry no real signals. (Docker: real rimsky +
real pg store via testcontainers; `make core-images` + `make service-images` first.)

### Task AUTHSTORES-19.1: write TestPGErrorClasses_Delivered (two sub-cases)
**Files:** `lib/services/test/scenarios/pg_error_classes/pg_error_classes_test.go`
**Steps:**
1. Stand up the real pg store + rimsky stack. Configure a pick policy whose items table is empty;
   declare a node `error_types` entry keyed on `pg/claim_unavailable` (e.g. → give_up) and a
   subscriber matching `terminal/error/pg/claim_unavailable`. Drive an Open; assert the
   `pg/claim_unavailable` signal is delivered (event-log row / subscriber callback observed).
2. Configure a staged-write claim and force a swap collision at Commit (per AUTHSTORES-16); declare
   an `error_types`/subscriber on `pg/swap_failed`; assert the `pg/swap_failed` signal is delivered.
**Verification:** `! go test ./lib/services/test/scenarios/pg_error_classes/ -run TestPGErrorClasses_Delivered -count=1`
(expect FAIL today)

---

## Pass AUTHSTORES-20: emit pg/claim_unavailable (and route swap_failed) as signals (GREEN)
**Goal:** Make the pg store emit `pg/claim_unavailable` on a producer-side empty/unopenable claim
(replacing the bare `OpenResponse_Unavailable` where the operator declared the class) and ensure
`pg/swap_failed` (from AUTHSTORES-16) routes through `error_types` to a subscriber.
**Scope:** `lib/services/stores/postgres/store/store.go` + the producer→rimsky acquisition-failure
path that feeds the `acquire/*` → `error_types` chain. **End state:** working
**Verification:** `go test ./lib/services/test/scenarios/pg_error_classes/ -run TestPGErrorClasses_Delivered -count=1`
**Proves:** AUTHSTORES-19's named test PASSES — both classes are delivered to the subscriber at a
real surface. **Red-when-absent:** revert the emit and the classes go silent again (test red). RUN
this gate; report exit 0.

### Task AUTHSTORES-20.1: surface pg/claim_unavailable as the acquisition-failure class
**Files:** `lib/services/stores/postgres/store/store.go`
**Steps:**
1. When `openPickPolicy` finds the items table empty (the `pgx.ErrNoRows` branch) — for a producer
   whose declared error vocabulary includes `pg/claim_unavailable` — surface the unavailability such
   that rimsky's acquisition-failure routing keys the operator's chain by `pg/claim_unavailable`
   rather than only the generic `acquire/unavailable` synthetic. Grounding (the runtime routing seam,
   2026-06-07): a producer `Unavailable`/`Open` failure is turned into an acquisition-failure class
   inside `lib/runtime` — `tryAcquire` returns the `errAcquireUnavailable` sentinel
   (`lib/runtime/runner_acquire.go:296`), which `lib/runtime/runner_lifecycle.go` resolves through the
   operator's `error_types` chain under the synthetic class `"acquire/unavailable"` (with a
   `producerAcquireErrorFallbackClass = "acquire/producer_error"` at `runner_lifecycle.go:94` for a
   producer-side gRPC error). To make the operator-declared `pg/claim_unavailable` policy match, thread
   the producer-declared class through that chain (so the chosen `ErrorClass` is the producer's declared
   leaf, not only the generic `acquire/unavailable`). Keep `OpenOutcome{Available:false}` as the wire
   shape; the class attaches on the routing side. The GREEN gate `TestPGErrorClasses_Delivered` remains
   the arbiter of whether the class actually reaches the subscriber.
2. Confirm `pg/swap_failed` (AUTHSTORES-16) flows through the same claim-terminal error path to the
   subscriber.
**Verification:** `go test ./lib/services/test/scenarios/pg_error_classes/ -run TestPGErrorClasses_Delivered -count=1`

---

## Pass TEMPLCASCADE-1 — Claim-scope directive: single canonical spelling end to end

Story: `S-template-validation-claim-scope-end-to-end`.
Outcome: a template using `{{claim.<alias>.claim_scope}}` passes registration AND resolves at runtime to the live claim's claim-scope bytes; the legacy `{{claim.<alias>.scope}}` spelling is rejected at registration.

working: true
gate-cmd: `go test ./lib/graph/node/... ./lib/graph/attribute/... -run 'TestValidateTemplate_ClaimScopeSpelling|TestSubstitute_ClaimScope' -count=1`
gate-baseline: RED — the new validator/resolver assertions do not exist yet; once authored against today's code they fail (validator rejects `claim_scope`, resolver rejects `scope`).
gate-after: GREEN.

### Task TEMPLCASCADE-1.1 — RED: validator unit test pinning the spelling flip
Author `TestValidateTemplate_ClaimScopeSpelling` in `lib/graph/node/template_validator_test.go`. Drive the REAL `node.ValidateTemplate` over a template whose node acquires a claim under alias `a` (stores: `rw`, selector `/scope-A`) and sets attribute `region: "{{claim.a.claim_scope}}"`. Assert: `res.Ok()` is true (the canonical spelling validates). Add a sub-assertion that the same template using `region: "{{claim.a.scope}}"` produces a `ValidationError` whose `Msg` names `claim_scope` (legacy `scope` rejected).
Check: `! go test ./lib/graph/node/... -run TestValidateTemplate_ClaimScopeSpelling -count=1` (RED today — validator at `template_validator.go#1349/#1369` admits `scope`, rejects `claim_scope`).

### Task TEMPLCASCADE-1.2 — GREEN: flip the validator to canonical `claim_scope`
In `lib/graph/node/template_validator.go` change the claim-directive second-segment switch (`#1348-1371`): replace `case "address", "scope":` with `case "address", "claim_scope":` and update the error message at `#1369` to read `address|claim_scope|payload`; update the in-line comment block at `#1335-1339` to spell `claim_scope`.
Check: `go test ./lib/graph/node/... -run TestValidateTemplate_ClaimScopeSpelling -count=1` (GREEN). Then `go build ./...`.

### Task TEMPLCASCADE-1.3 — RED: resolver unit test pinning rejection of legacy `scope`
Author/extend `TestSubstitute_ClaimScope` in `lib/graph/attribute/substitution_test.go`: with a `ResolveContext.Claim` carrying `claimproducer.ClaimResult{ClaimScope: []byte("\"/scope-A\"")}` under alias `a`, assert `Substitute("{{claim.a.claim_scope}}", ctx)` returns the stringified scope with nil error, AND `Substitute("{{claim.a.scope}}", ctx)` returns an `*ErrMissingSource` (legacy spelling not a recognized second segment).
Check: `! go test ./lib/graph/attribute/... -run TestSubstitute_ClaimScope -count=1` (RED today if the rejection sub-assertion is new; resolver already accepts `claim_scope` at `#629`, so the load-bearing new assertion is that `scope` is rejected with the right error class).

### Task TEMPLCASCADE-1.4 — GREEN: confirm resolver second-segment error names canonical set
In `lib/graph/attribute/substitution.go::resolveClaimValue` ensure the default-arm error at `#647` reads `claim directive second segment must be address|claim_scope|payload` (already canonical — no `scope` admitted). No behavior change expected; the task exists to confirm the resolver default arm rejects `scope` as `ErrMissingSource` and to align the module docstring claim-grammar bullets at `#600-602` to the single canonical spelling.
Check: `go test ./lib/graph/attribute/... -run TestSubstitute_ClaimScope -count=1` (GREEN).

---

## Pass TEMPLCASCADE-2 — Substitution source-kinds docstring accuracy

Story: `S-template-validation-source-kinds-docstring-accuracy` (doc-drift; automated accuracy check).
Outcome: the substitution module header's source-kind enumeration matches the set of kinds the resolver actually dispatches on (claim, params, nodes, trigger, child).

working: true
gate-cmd: `go test ./lib/graph/attribute/... -run TestSubstitutionDocstringMatchesResolver -count=1`
gate-baseline: RED — header says "Five recognized source kinds:" over six bullets omitting trigger/child while the resolver dispatches five live kinds; the parsing test fails on the count + membership mismatch.
gate-after: GREEN.

### Task TEMPLCASCADE-2.1 — RED: accuracy test parsing header vs live resolver arms
Author `TestSubstitutionDocstringMatchesResolver` in `lib/graph/attribute/substitution_test.go`. Read the source file `substitution.go` (via `os.ReadFile` on the package-relative path or `runtime.Caller`), parse the documented source-kind enumeration in the module header (the `Five recognized source kinds:` block and its bullets), and assert: (a) the declared count word matches the bullet count; (b) the bullet set's kind-prefixes equal the live resolver kind set `{claim, params, nodes, trigger, child}` — the same set `resolveDirectiveValueRaw` switches on (excluding the retired `deps` rejection arm). The test must FAIL on an undercount, a missing recognized kind (trigger/child), or a listed kind the resolver does not handle.
Check: `! go test ./lib/graph/attribute/... -run TestSubstitutionDocstringMatchesResolver -count=1` (RED today — header drift at `substitution.go#7-14`).

### Task TEMPLCASCADE-2.2 — GREEN: correct the substitution module header
In `lib/graph/attribute/substitution.go` rewrite the header block (`#7-14`): change "Five recognized source kinds:" wording to match the true count and list all live kinds with one canonical bullet each — `nodes`, `claim` (using `claim_scope`, per Pass 1), `params`, `trigger`, `child` — so the prose enumerates exactly the kinds `resolveDirectiveValueRaw#411-423` dispatches (the `deps` arm stays documented as the retired/rejected form, not counted as a live kind).
Check: `go test ./lib/graph/attribute/... -run TestSubstitutionDocstringMatchesResolver -count=1` (GREEN). Then `go build ./...`.

---

## Pass TEMPLCASCADE-3 — Registration-time reference-validation MODE (all / available / none)

Story: `S-template-validation-ref-validation-mode`.
Outcome: an operator-set startup config governs ALL registration-time reference validation — `all` (default, hard-fail any unvalidatable ref), `available` (validate provisioned refs, skip not-yet-provisioned ones), `none` (no registration-time ref validation). The previously-implicit always-on soft-fail heuristic is GONE; its behavior is now exactly mode `available`.

working: true
gate-cmd: `go test ./lib/graph/node/... ./lib/control/controlapi/... -run 'TestValidateTemplate_RefMode|TestRegisterTemplate_RefMode' -count=1`
gate-baseline: RED — no mode exists; `node.ValidateTemplate` strictness is fixed per-leg with no config knob.
gate-after: GREEN.

### Task TEMPLCASCADE-3.0 — TEST-INFRA: make the stub executor's advertised schema configurable (consumed by AG-3 / AG-4)
Grounded today: the scenario stub executor advertises a permissive schema. `test/support/executors/stub/observability.go::ObservabilityServer` is a fieldless struct; `NewObservabilityServer()` takes no args; `Capabilities` hard-codes `ExpectedAttributesSchema: []byte(\`{"type":"object"}\`)` (`#48`). `RegisterObservability(srv *grpc.Server)` (`#82`) constructs it; the harness chains `scenario.Start` → `stubtest.Listen(t, s)` (`test/support/executors/stub/stubtest/listen.go:32`) → `stub.RegisterObservability(srv)`. `HarnessOpts` (`test/support/scenario/harness.go:71`) exposes `ExtraExecutors map[string]executor.Endpoint` but NO schema knob, so AG-3 (genuinely-invalid provisioned ref still 400s) and AG-4 (`minimum:0` static-config gate) cannot stand up a constraint-advertising executor today.
Action:
- Add a configurable schema to the stub observability server: a `ExpectedAttributesSchema []byte` field on `ObservabilityServer`, a `NewObservabilityServerWithSchema(schema []byte) *ObservabilityServer` constructor (keep `NewObservabilityServer()` defaulting to `{"type":"object"}` for back-compat), and `Capabilities` returns the configured bytes (falling back to the permissive default when empty). Add `RegisterObservabilityWithSchema(srv *grpc.Server, schema []byte)` alongside the existing `RegisterObservability`.
- Thread it through the listen helper: add `stubtest.ListenWithSchema(t, s, schema []byte) (*grpc.Server, string)` (the existing `Listen` delegates with a nil/permissive schema).
- Surface it to scenario tests so a constraint-advertising executor can be wired via `ExtraExecutors`: the cleanest seam is a small helper a test can call to stand up a SECOND stub instance whose Capabilities advertise a constrained schema and register it as an `ExtraExecutors` entry (e.g. add `scenario` helper `StartStubExecutorWithSchema(t, schema []byte) executor.Endpoint` that does `stubexec.New()` + `stubtest.ListenWithSchema` and returns the `{Transport:"grpc", URL:addr}` endpoint). Tests then pass `HarnessOpts{ExtraExecutors: {"constrained": StartStubExecutorWithSchema(t, schemaWithMinimum0)}}`. (No new top-level `HarnessOpts` schema field is required when the constrained executor is a distinct `ExtraExecutors` entry; if a reviewer prefers a first-class `HarnessOpts.StubSchema []byte` that re-skins the default `"stub"` executor's schema, add that field and apply it where the harness builds `s` / calls `stubtest.Listen` — either form is acceptable, but the `ExtraExecutors` form is the minimal change.)
Check: `go build ./test/support/... && go vet ./test/support/...` succeed; `grep -n 'ExpectedAttributesSchema\|NewObservabilityServerWithSchema\|ListenWithSchema' test/support/executors/stub/observability.go test/support/executors/stub/stubtest/listen.go` shows the new knob.

### Task TEMPLCASCADE-3.1 — RED: validator-level test of the tri-state mode over the reference legs
Author `TestValidateTemplate_RefMode` in `lib/graph/node/template_validator_test.go`. Extend `node.ValidateTemplate` to take a reference-validation mode (a new field on `RegistryHooks`, e.g. `RefValidationMode node.RefValidationMode` with values `RefValidateAll`/`RefValidateAvailable`/`RefValidateNone`, default zero-value = `RefValidateAll`). Drive three cases over a template node referencing an executor whose `ExecutorDeclared` hook returns false (not provisioned) and `ExecutorExpectedAttributesSchema` returns `(nil,false)` (schema not visible): mode `all` → a missing-reference `ValidationError`; mode `available` → no error for the not-provisioned ref BUT a genuinely-invalid ref to a PROVISIONED executor (declared true, schema visible, attribute violates schema) still errors; mode `none` → no reference errors at all. Assert the readOnly-leg skip at `#945-977` collapses into mode `available` semantics (not a fourth hidden behavior).
Check: `! go test ./lib/graph/node/... -run TestValidateTemplate_RefMode -count=1` (RED today — no mode field; legs are fixed).

### Task TEMPLCASCADE-3.2 — GREEN: thread the mode through the validator reference legs
In `lib/graph/node/template_validator.go`: add the `RefValidationMode` type + a `RefValidationMode` field on `RegistryHooks` (zero-value = `all`). Make `validateExecutorDeclared#686`, `validateStores#705` (the `StoreDeclared` leg `#716-724`), `validateLocks#800` (the `NamedLockDeclared` leg `#820-828`), and the executor-schema legs honor the mode: `all` → hard-fail on any unvalidatable/missing ref (including schema-not-visible — the readOnly leg becomes a hard error instead of a silent skip); `available` → skip refs whose target is not provisioned (hook reports not-declared / schema-not-visible) but still validate provisioned refs (this is exactly today's readOnly soft-fail behavior, made explicit and uniform across legs); `none` → skip all four reference legs entirely. Remove the implicit always-on soft-fail at `#945-977` and route it through the mode switch (mode `available`/`all` decide skip-vs-hard-fail).
Check: `go test ./lib/graph/node/... -run TestValidateTemplate_RefMode -count=1` (GREEN). Then `go build ./...`.

### Task TEMPLCASCADE-3.3 — RED: control-api register test reading the mode from operator config
Author `TestRegisterTemplate_RefMode` in `lib/control/controlapi/templates_test.go`. Add an operator-set config field (a `RefValidationMode` on `AppDeps`, sourced from a `cfg:` / startup option + `env:RIMSKY_REF_VALIDATION_MODE`) and have `validatorHooksFor#114` stamp it onto `RegistryHooks.RefValidationMode`. Drive `POST /templates` through the real `handleDeployTemplate` for a template referencing a not-yet-provisioned executor: with mode `all` (default) → HTTP 400 with a missing-reference `validation_errors` entry; with mode `available` → 200/201 for that ref while a genuinely-invalid provisioned ref still 400s; with mode `none` → 200/201 with no registration-time reference validation.
Check: `! go test ./lib/control/controlapi/... -run TestRegisterTemplate_RefMode -count=1` (RED today — no mode wiring; register strictness is fixed).

### Task TEMPLCASCADE-3.4 — GREEN: wire the operator mode into AppDeps + validatorHooksFor + config
In `lib/control/controlapi`: add the `RefValidationMode` field to `AppDeps`; stamp it onto the hooks in `validatorHooksFor#114-166`; default to `all` when unset. Add the config/env plumbing (`cfg:templates.ref_validation_mode` + `env:RIMSKY_REF_VALIDATION_MODE`) in the control-api config loader (locate the existing control-api config struct via the `cfg:` loader package and add a parsed enum field, defaulting to `all`, rejecting unknown values at startup). Apply the same mode to the `POST /templates/validate` path (`handleValidateTemplate#378`).
Check: `go test ./lib/control/controlapi/... -run TestRegisterTemplate_RefMode -count=1` (GREEN). Then `go build ./... && make lint`.

### Task TEMPLCASCADE-3.5 — Design-change: record validation-timing model on concept:template / concept:instance (ref-mode half)
Edit `.ok-planner/design/concepts/template.md`:
- **Invariants** section (`#22-27`): add a bullet — "Reference and schema validation is **optional at registration** under an operator-set mode (`all` default / `available` / `none`); a relaxed mode skips refs whose target services are not yet provisioned (the previously-implicit always-on soft-fail heuristic IS mode `available`, now explicit and uniform across the executor / store / lock / schema legs)."
- **Open within this concept** (`#33-37`): leave the `compose:` item; this story adds no new open item.
- **Notes**: append `2026-06-06 — Registration-time reference validation becomes operator-mode-controlled (all/available/none, default all) per spec:2026-06-06-comprehensive-gap-closure-design (story S-template-validation-ref-validation-mode). The implicit always-on soft-fail heuristic retires; its behavior is exactly mode available. Mandatory validation moves to instantiation (see concept:instance).`
Check: `grep -n 'ref_validation_mode\|ref validation\|available' .ok-planner/design/concepts/template.md` returns the new Invariant + Notes lines.

---

## Pass TEMPLCASCADE-4 — Mandatory instantiation-time static-config validation gate

Story: `S-template-validation-instantiation-mandatory` (review-surfaced; no prior design).
Outcome: `POST /instances` validates the node attributes' statically-knowable config (value constraints included) against every referenced service's schema and REJECTS create with a clear error if anything is statically misconfigured — even when a relaxed registration mode skipped it. Substitution-sourced values stay validated at dispatch (`@blessed-invariant 12`).

working: true
gate-cmd: `go test ./lib/control/controlapi/... -run TestCreateInstance_StaticConfigValidationGate -count=1`
gate-baseline: RED — instance-create runs no node-attribute schema validation; a `minimum`-violating static default is accepted at create and only fails at dispatch.
gate-after: GREEN.

### Task TEMPLCASCADE-4.1 — RED: instance-create rejection on a static `minimum` violation
Author `TestCreateInstance_StaticConfigValidationGate` in `lib/control/controlapi/instances_test.go`. Register (under ref mode `none`, so registration skips it) + deploy a template whose node carries a static attribute default violating a referenced executor's schema constraint — model the executor capability schema with a property declaring `minimum: 0` and set the node default to `-1` (e.g. `cli.max_signoff_attempts: -1`, mirroring the spec's claude-agent example). Drive `POST /instances` through the real `handleCreateInstance`. Assert: HTTP 400 with a validation error naming the offending attribute AND citing the `minimum` violation (a genuine value-constraint check, not a missing/extra-attribute surface error); `GET /instances` shows no new instance row persisted. Add a companion sub-case: a well-formed instance of the same template returns 201 and persists.
Check: `! go test ./lib/control/controlapi/... -run TestCreateInstance_StaticConfigValidationGate -count=1` (RED today — `handleCreateInstance#286` runs no schema validation; only `validateAttributeOverrides#386`).

### Task TEMPLCASCADE-4.2 — GREEN: add the create-time static-config validation gate
In `lib/control/controlapi/instances.go::handleCreateInstance`, after `validateAttributeOverrides#386` (inside the FOR UPDATE tx, before the dry-run gate / inserts), compute the per-node static config bag (L1 defaults ∪ L2 node declaration — the non-substitution-sourced subset, i.e. attributes with a `default:` or static literal, excluding `source:`-bound and substitution-directive values) and validate it against each referenced executor's `expected_attributes_schema` via the existing `attributes.Validate(..., attributes.PhaseDispatch)` value-constraint path against the executor's schema (reuse the lookup `deps.ExecutorCapabilities`). On any violation, return a wrapped `shared.ErrTemplateValidation` carrying the offending attribute path + constraint, surfaced as HTTP 400. Do NOT validate substitution-sourced values here (those remain dispatch-only, `@blessed-invariant 12`). Honor dry-run: the gate runs before the `errDryRunOK` sentinel so a dry-run create also surfaces the rejection. Note the gate is mandatory regardless of registration ref-mode (all referenced services exist at instantiation, including the host-agent proxy as a present service).
Check: `go test ./lib/control/controlapi/... -run TestCreateInstance_StaticConfigValidationGate -count=1` (GREEN). Then `go build ./... && make lint`.

### Task TEMPLCASCADE-4.3 — Design-change: record the mandatory-instantiation gate on concept:instance / concept:template
Edit `.ok-planner/design/concepts/instance.md`:
- **Invariants** section (`#21-31`): add a bullet — "Instantiation is the mandatory static-config validation gate: `POST /instances` validates each node's statically-knowable attribute config (value constraints included) against every referenced service's schema and rejects create on any static misconfiguration. All referenced services exist at instantiation (the bound-on-demand host-agent proxy is itself a present service), so whatever a relaxed registration mode skipped is enforced here. Substitution-sourced values, knowable only once a node acquires its inputs, stay validated at dispatch (`@blessed-invariant 12`, validate-twice — that pass becomes defense-in-depth for the static part)."
- **Notes**: append `2026-06-06 — Mandatory instantiation-time static-config validation gate added per spec:2026-06-06-comprehensive-gap-closure-design (story S-template-validation-instantiation-mandatory). Create-time now rejects a static value-constraint violation (e.g. a default below an executor schema minimum) rather than deferring it to a mid-run dispatch error.`
Also append to `template.md` Notes: `2026-06-06 — Validation-timing model: optional-at-registration (mode-controlled) + mandatory-at-instantiation (see concept:instance). The dispatch value-validation (@blessed-invariant 12) stays for substitution-sourced values and is defense-in-depth for the static config part.`
Check: `grep -n 'instantiation.*mandatory\|static-config\|statically-knowable' .ok-planner/design/concepts/instance.md` returns the new Invariant + Notes lines.

---

## Pass TEMPLCASCADE-5 — Lenient `?` marker recovery, proven end to end

Story: `S-template-validation-lenient-marker-recovery-e2e`.
Outcome: a lenient `?`-marked directive whose source is genuinely absent at dispatch resolves to empty string through the full real stack and the run reaches terminal Complete; a companion non-`?` directive on the same absent source fails the dispatch with a missing-source error.

working: true
gate-cmd: `go test ./test/scenarios/attributes/... -run TestLenientMarkerRecoveryE2E -count=1`
gate-baseline: RED — no scenario test exercises the `?` marker through a real stack; the test is absent (the new file does not compile/exist), and once authored it drives behavior that has no prior e2e coverage.
gate-after: GREEN.

### Task TEMPLCASCADE-5.1 — RED: full-stack scenario for lenient-recovery + strict-failure
Author `TestLenientMarkerRecoveryE2E` in a new file `test/scenarios/attributes/lenient_marker_recovery_test.go` (package `attributes`). Boot the real stack via `scenario.Start`. Deploy a template with two worker nodes against the stub executor: node A sets attribute `note: "{{nodes.upstream.attribute.maybe?}}"` (lenient, source genuinely absent — `upstream` does not produce `maybe`); node B sets `note: "{{nodes.upstream.attribute.maybe}}"` (strict, same absent source). Drive an instance. Assert via the REAL surfaces: (a) node A dispatches and the stub records (`h.Stub.Observed()[].Attributes["note"]`) the resolved value as empty string, and node A reaches `fresh`/terminal Complete; (b) node B's dispatch fails with a missing-source diagnostic (the node does not reach a clean terminal — surfaced as `template_resolution_failed`/missing-source on its run). Use the existing harness wait helpers (`WaitForNodeState`, `WaitForEventKind`).
Check: `! go test ./test/scenarios/attributes/... -run TestLenientMarkerRecoveryE2E -count=1` (RED — new test; Docker required).

### Task TEMPLCASCADE-5.2 — GREEN: confirm the e2e lenient path resolves to empty and strict fails
Run the scenario green. The `?` lenient path already exists in the resolver (`resolveDirectiveValue` strips the trailing `?` and returns empty/null on `ErrMissingSource`); this pass proves it through the real dispatch path end to end and fixes any gap surfaced by the RED run (e.g. if the dispatch-time `substituteAttributesSchema` path does not honor the lenient flag for whole-directive `source:` strings, fix `code:lib/runtime/runner_dispatch.go::substituteAttributesSchema` / the resolver entry it calls so a lenient missing source resolves to empty rather than failing the dispatch). Do NOT weaken the test to pass.
Check: `go test ./test/scenarios/attributes/... -run TestLenientMarkerRecoveryE2E -count=1` (GREEN). Then `go build ./... && go test ./lib/graph/attribute/... -count=1`.

---

## Pass TEMPLCASCADE-6 — Operator `frame: in` joins the running cascade frame

Story: `S-cascade-operator-frame-in`.
Outcome: an operator invalidate with `frame: in` resolves the target instance's currently-running frame and joins it (target acquires the SAME frame_id, re-evaluated within that frame's drain), instead of silently downgrading to next-frame.

working: true
gate-cmd: `go test ./lib/control/controlapi/... -run TestInvalidateNode_FrameIn_JoinsRunningFrame -count=1`
gate-baseline: RED — the handler never sets SourceNodeID/SourceFrameID, so `invalidateInFrame` falls back to next-frame; an assertion that the target joins the running frame_id fails.
gate-after: GREEN.

### Task TEMPLCASCADE-6.1 — RED: handler test asserting in-frame join (same frame_id)
Author `TestInvalidateNode_FrameIn_JoinsRunningFrame` — create `lib/control/controlapi/nodes_test.go` (the file does not exist yet on the current tree; grounded 2026-06-07), or place it in a co-located in-package `_test.go` (`package controlapi`). NOTE: CLICTRL-6.1 also creates `nodes_test.go`; whichever pass lands first creates the file and the other appends its test function (do not double-create — both are `package controlapi`). Set up a real persistence-backed control-api with an instance whose cascade frame is currently OPEN — a source node settled in a running frame `F`, a dependent mid-drain (seed via the harness/dispatch path or the real frame engine so frame `F` is genuinely running, not hand-rolled state). Issue `POST /nodes/{target}/invalidate` with body `{"frame":"in"}` for a target node in that instance. Assert at the real persisted state + event log: the target transitions fresh→stale and acquires `frame_id == F` (the running frame), NOT a freshly-enqueued next-frame id; the `state_transition` event for the target carries `frame_id = F` with reason `in_frame_invalidate`.
Check: `! go test ./lib/control/controlapi/... -run TestInvalidateNode_FrameIn_JoinsRunningFrame -count=1` (RED today — `handleInvalidateNode#177-186` omits SourceFrameID, so `invalidateInFrame#238` falls back to next-frame).

### Task TEMPLCASCADE-6.2 — GREEN: operator path resolves the running frame and threads it
In `lib/control/controlapi/nodes.go::handleInvalidateNode`, when `body.Frame == "in"`: resolve the target instance's currently-running frame id (read the open/running `rimsky_frames` row for `row.InstanceID` via the persistence frame accessor — the same source-of-truth the frame engine advances) and pass it as `runtime.InvalidateArgs.SourceFrameID` (a `*shared.UUID`). When no frame is currently running, leave `SourceFrameID` nil so `invalidateInFrame` takes its documented next-frame fallback (the deterministic fallback the story requires). Leave `SourceNodeID` unset (operator-sourced invalidate has no source node — the existing `invalidateInFrame` resolution order at `cascade_invalidate.go#214-218,251-268` prefers `SourceFrameID` and skips the source-node re-read when the caller supplies the frame, so the supplied frame is authoritative). The dry-run echo at `nodes.go#154-158` already echoes `frame: in`; it is now truthful.
Check: `go test ./lib/control/controlapi/... -run TestInvalidateNode_FrameIn_JoinsRunningFrame -count=1` (GREEN). Then `go build ./... && go test ./lib/runtime/... -run Invalidate -count=1`.

### Task TEMPLCASCADE-6.3 — Design-change: define operator-originated in-frame invalidate on concept:cascade / concept:invalidate
Edit `.ok-planner/design/concepts/invalidate.md`:
- **Invariants** (`#21-25`): refine the `frame: in | next` bullet — add: "An operator `frame: in` request resolves the target instance's currently-running frame and joins it (marks the target stale with that frame_id); when no frame is currently running it falls back deterministically to next-frame. This is the documented exception to 'operator-API messages always create a new frame' (2026-05-15 Notes): an explicit `frame: in` join is permitted into an open running frame."
- **Notes**: append `2026-06-06 — Operator-originated in-frame invalidate defined per spec:2026-06-06-comprehensive-gap-closure-design (story S-cascade-operator-frame-in). The operator-API frame:in path now resolves the instance's running frame and joins it (was silently downgraded to next-frame); deterministic next-frame fallback only when no frame is running.`
Edit `.ok-planner/design/concepts/cascade.md`:
- **Notes**: append `2026-06-06 — The cascade walker's in-frame join now has an operator-sourced entry point (operator frame:in), alongside the existing cascade-sourced in-frame joins (post-success self-invalidate, hard-dep pull). The boundary: cascade-sourced joins resolve the frame from the source node's run row; the operator-sourced join resolves the instance's currently-running frame directly (no source node). Per spec:2026-06-06-comprehensive-gap-closure-design.`
Check: `grep -n 'operator.*frame:in\|currently-running frame\|operator-sourced' .ok-planner/design/concepts/invalidate.md .ok-planner/design/concepts/cascade.md` returns the new lines.

---

## Pass TEMPLCASCADE-7 — Wait-set topic_kind spans the full 5-value signal taxonomy

Story: `S-cascade-waitset-topic-taxonomy`.
Outcome: `rimsky_wait_set.topic_kind` records the actual signal class for transient-, terminal-, and message-class signals instead of collapsing three of them into a lossy `state` bucket; the DB CHECK accepts the full taxonomy, `waitSetTopicKindFor` maps each top-level kind to its own value, and drain/dedupe behavior is unchanged.

### Task TEMPLCASCADE-7.1 — MIGRATION GATE (non-test relation/CHECK flip): broaden the topic_kind CHECK on postgres + sqlite
Proves: the `rimsky_wait_set.topic_kind` CHECK constraint admits the full 5-value taxonomy (`'state','attribute','event','transient','message','terminal'`) on a freshly-migrated database, on BOTH backends.
Red-when-absent: a probe that inserts a `rimsky_wait_set` row with `topic_kind='transient'` (and separately `'message'`, `'terminal'`) against a freshly-migrated DB is REJECTED by the CHECK before this migration exists (today's CHECK is `('state','attribute','event')` at `postgres/migrations/001-schema.sql#278` / `sqlite/migrations/001-schema.sql#246`).
Action:
- Add `lib/foundation/persistence/postgres/migrations/006-waitset-topic-kind-taxonomy.sql`: `ALTER TABLE rimsky_wait_set DROP CONSTRAINT <topic_kind check name>; ALTER TABLE rimsky_wait_set ADD CONSTRAINT rimsky_wait_set_topic_kind_check CHECK (topic_kind IN ('state','attribute','event','transient','message','terminal'));` (resolve the inline-CHECK constraint's generated name first, or recreate via a named constraint — postgres can alter a column CHECK without touching the PRIMARY KEY since `topic_kind`'s PK membership is independent of its CHECK). Keep `'state'` admitted (back-compat for existing 'state' rows; the new mapping no longer emits it for transient/terminal/message, but legacy rows and the conformance fixtures use it).
- Add `lib/foundation/persistence/sqlite/migrations/006-waitset-topic-kind-taxonomy.sql`: SQLite cannot alter a CHECK in place, so rebuild the leaf `rimsky_wait_set` table — `CREATE TABLE rimsky_wait_set_new (...)` with the broadened CHECK and identical columns/PK/indexes, `INSERT INTO rimsky_wait_set_new SELECT * FROM rimsky_wait_set;`, `DROP TABLE rimsky_wait_set;`, `ALTER TABLE rimsky_wait_set_new RENAME TO rimsky_wait_set;`, recreate `idx_rimsky_wait_set_receiver` + `idx_rimsky_wait_set_sender`. Safe because no FK references `rimsky_wait_set` (verified — it is a leaf table); the rebuild runs inside the migration tx with the same indexes restored.
Check: `! <probe>` — author a focused persistence test named `TestWaitSetTopicKindCheckAdmitsBroadenedTaxonomy` (in a new `lib/foundation/persistence/postgres/wait_set_topic_kind_test.go` + a sqlite mirror `lib/foundation/persistence/sqlite/wait_set_topic_kind_test.go`) that inserts a `topic_kind='transient'` row (and separately `'message'`, `'terminal'`) and asserts success; run it red against HEAD before adding the migrations, green after. This NEW test name is what the Pass-7 overall gate keys on (see below) — do not rely on the bare `WaitSet` filter, which matches pre-existing passing tests. Then `go test ./lib/foundation/persistence/... -count=1` (Docker required for the postgres path).

### Task TEMPLCASCADE-7.2 — RED: unit test pinning waitSetTopicKindFor to the 5-value taxonomy
Author `TestWaitSetTopicKindFor_FullTaxonomy` in `lib/runtime/runner_terminal_test.go` (or a co-located `_test.go`). Assert `waitSetTopicKindFor` maps each top-level signal kind to its own value: `terminal/success` → `"terminal"`, `transient/await_async` → `"transient"`, `attribute/x/changed` → `"attribute"`, `event/foo` → `"event"`, `message/invalidate/operator/n` → `"message"` — with no two distinct signal classes collapsed onto the same bucket.
Check: `! go test ./lib/runtime/... -run TestWaitSetTopicKindFor_FullTaxonomy -count=1` (RED today — `runner_terminal.go#1218-1227` maps terminal/transient/message all to `"state"`).

### Task TEMPLCASCADE-7.3 — GREEN: map each top-level kind to its own topic_kind value
In `lib/runtime/runner_terminal.go::waitSetTopicKindFor#1218`, replace the 3-bucket collapse with a full mapping over `pattern.TopLevel()`: `KindTerminal`→`"terminal"`, `KindTransient`→`"transient"`, `KindAttribute`→`"attribute"`, `KindEvent`→`"event"`, `KindMessage`→`"message"`, default→`"state"` (the empty/unrecognized fallback only). Update the function docstring (`#1205-1217`) to drop the "until a follow-up migration broadens the enum" wording and state the taxonomy is now faithful. Update the `TopicKind` field doc-comments in `lib/foundation/persistence/wait_set.go#33` to list the broadened value set.
Check: `go test ./lib/runtime/... -run TestWaitSetTopicKindFor_FullTaxonomy -count=1` (GREEN). Then `go build ./...`.

### Task TEMPLCASCADE-7.4 — Design-change: align wait-set topic_kind discriminator with the 5-value taxonomy on concept:signal
Edit `.ok-planner/design/concepts/signal.md`:
- **Boundaries / Does NOT own** (`#87-91`): the wait-set ledger stays owned by `concept:wait-set`; add to **Invariants** (`#95-103`) a bullet — "The wait-set `topic_kind` discriminator is a faithful projection of the signal top-level kind: each of the five canonical kinds (terminal, transient, attribute, event, message) maps to its own `topic_kind` value; no two distinct signal classes collapse onto a shared bucket. (`state` remains admitted for legacy/unrecognized fallback rows.)"
- **Notes**: append `2026-06-06 — Wait-set topic_kind discriminator broadened to the full 5-value signal taxonomy per spec:2026-06-06-comprehensive-gap-closure-design (story S-cascade-waitset-topic-taxonomy); the deferred CHECK-broadening migration from spec:2026-05-23-signal-taxonomy-and-policy-decoupling lands here (postgres + sqlite). The lossy 3-bucket collapse (terminal/transient/message → state) retires. Drain/dedupe behavior unchanged.`
Check: `grep -n 'topic_kind\|faithful projection\|5-value' .ok-planner/design/concepts/signal.md` returns the new Invariant + Notes lines.

working: true (Pass 7 overall)
gate-cmd: `go test ./lib/foundation/persistence/... ./lib/runtime/... -run 'TestWaitSetTopicKindCheckAdmitsBroadenedTaxonomy|TestWaitSetTopicKindFor_FullTaxonomy' -count=1`
gate-baseline: RED — scoped to the two NEW test names this pass authors (the migration probe + the `waitSetTopicKindFor` taxonomy unit test). The broad `-run 'TopicKind|WaitSet'` filter was REJECTED here because it is green-from-birth: on the current tree it matches two pre-existing passing tests (`TestRecalculateNode_StaleWithPendingWaitSet_IsNoOp`, `TestRecalculateNode_StaleWithEmptyWaitSetAndExecutor_EnqueuesDispatch`) and no `TopicKind`-named test exists, so the gate would be green before any work. Scoped to the new names, the gate is genuinely RED→GREEN around this pass: pre-edit both named tests are absent (filter matches nothing) and after 7.1/7.2 author them the probe is rejected by the unbroadened CHECK / `waitSetTopicKindFor` collapses to 3 buckets — both red — flipping green once the migrations + mapping land.
gate-after: GREEN — both named tests pass.

---

## Pass HOSTAGENT-1 — Proto: thread run_scope_id onto the dispatch wire [proto-edit]

Type: proto-edit (followed by regen + Go work in HOSTAGENT-2). No GATE metadata
on a proto-edit-only pass — the build gate lives on the consuming pass.

### Task HOSTAGENT-1.1 — add run_scope_id to ExecuteRequest
Edit `lib/protocols/proto/v1/executor.proto`: add `string run_scope_id = 16;` to
`ExecuteRequest` (next free field number after `prior_dispatch_disposition = 15`),
with a comment: the RunScope this dispatch lives in; used by the host-agent-proxy
to key per-run-scope spawn isolation; opaque to in-process executors.
Check: `grep -n 'run_scope_id = 16' lib/protocols/proto/v1/executor.proto` returns the line.

### Task HOSTAGENT-1.2 — add run_scope_id to OpenRequest
Edit `lib/protocols/proto/v1/claim_producer.proto`: add `string run_scope_id = 8;`
to `OpenRequest` (next free field after `instance_id = 7`), same comment intent
(per-run-scope spawn keying on the claim-producer path).
Check: `grep -n 'run_scope_id = 8' lib/protocols/proto/v1/claim_producer.proto` returns the line.

### Task HOSTAGENT-1.3 — regenerate generated Go
Run `make proto-gen`.
Check: `grep -rn 'RunScopeId' lib/protocols/proto/v1/gen/executor*.pb.go lib/protocols/proto/v1/gen/claim_producer*.pb.go` shows the new accessors (`GetRunScopeId`).

---

## Pass HOSTAGENT-2 — Per-run-scope spawn isolation: key spawns/reap by run_scope_id [working]

Story: `S-hostagent-per-run-scope-isolation`. Make the run-scope-keyed-spawn
invariant real end to end: supervisor stamps `run_scope_id` on the dispatch;
proxy keys spawns/dedup/reap by `(run_scope_id, binding_name)` rather than by
instance id; reap matches on `run_scope_id`. Two concurrent run-scopes of one
instance get distinct child processes.

```
GATE: go test ./test/scenarios/ -run 'TestHostAgentPerRunScopeIsolation' -count=1
GATE-DIR: (repo root)
GATE-VERDICT (a bare `-run` exits 0 when the named test is absent, so judge by OUTPUT, not exit code — the EXECUTORS-cluster guarded form):
  RED  iff: output contains `FAIL` (or a real test failure) AND does NOT contain `no tests to run`
  GREEN iff: output contains `ok` (the package passed the named test) AND does NOT contain `no tests to run`
  filter-matches-nothing (`no tests to run`) is treated as NOT-satisfied — right-reason-RED is pinned to the pass's RED task (2.1)'s `! go test … -run TestHostAgentPerRunScopeIsolation`, which is the verdict the orchestrator runs after authoring the test.
EXPECT-RED-PRE: before 2.1, `-run` matches nothing → `no tests to run` (NOT-satisfied, not a green gate); after 2.1 authors the test, the run FAILs (the proxy hard-codes scopeID := instanceID at dispatch.go:131 so two run-scopes collapse onto one child) → right-reason RED
EXPECT-GREEN-POST: output contains `ok`, no `no tests to run`
RUN-NOW result: on the current tree the bare run exits 0 with `no tests to run` (test absent) → NOT a passing gate; the load-bearing RED is the 2.1 `! go test` verdict after authoring
```

### Task HOSTAGENT-2.1 — RED: scenario test asserting two run-scopes → two distinct child PIDs
Add `test/scenarios/host_agent_per_run_scope_isolation_test.go` with
`func TestHostAgentPerRunScopeIsolation(t *testing.T)`. Drive the REAL stack via
the existing `newHostAgentFixture(t, fixtureOpts{withAgent: true})` (real proxy
binary + in-process `hostagent.Run` agent + real exec'd `stubchild`).

- Extend the `stubchild` testdata binary (`lib/runtime/hostagent/testdata/stubchild/main.go`)
  with a new env knob `STUBCHILD_PID_LOG`: on each Execute the stub appends a line
  `<run_scope_id-from-ExecuteRequest> <os.Getpid()>` to that file. (The stub already
  reads the request; it now reads `req.GetRunScopeId()`.) Set the env in the test
  before fixture start so every spawned child inherits it (env is inherited per
  `spawn.go:76`).
- Deploy a template whose graph fans out into **two concurrent run-scopes** that
  both dispatch the same late-bound executor node. Use a fan-out template spec
  (mirror `lateBindTemplateSpec` but with a `fan-out:` parent node that splits
  into two partitions, each a worker bound to `codegen`) — model on the existing
  fan-out scenarios under `test/scenarios/` for the spec shape. Bind `codegen` to
  the PID-logging stub via `fx.createLateBindInstance`-style
  `CreateInstanceWithServiceBindings`.
- Assert OBSERVABLE outcome: the PID-log file ends up with **two distinct PIDs**,
  one per run_scope_id, and each run_scope_id line carries only its own PID (no
  shared child). Read the run_scope_ids from the persisted run-tree to bind the
  assertion to real scopes, not string-matching.
Check (must fail on current tree):
`! go test ./test/scenarios/ -run 'TestHostAgentPerRunScopeIsolation' -count=1`

### Task HOSTAGENT-2.2 — GREEN: stamp run_scope_id on the supervisor dispatch
In `lib/runtime/runner_dispatch.go::buildExecuteRequest` (the `ExecuteRequest{}`
literal at ~1045) set `RunScopeId: acq.RunScopeID.String()` from the
`acquisition.RunScopeID` field (`runner_acquire.go:142`). For the claim-producer
path, set `RunScopeId: spec.RunScopeID` in `lib/runtime/peer/client.go::Open`'s
`OpenRequest{}` literal (~47) — add a `RunScopeID string` field to
`claimproducer.ClaimSpec` (`lib/protocols/claimproducer/types.go:87`) and populate
it where the spec is built. RunScopeID-threading detail (grounded 2026-06-07):
`InstanceID` on the spec is set INSIDE `buildLockSpecs` (`lib/runtime/runner_locks.go:139`,
`InstanceID: instInstanceScope(inst)`), but the run-scope id is NOT in scope there —
it is computed at the ACQUIRE site (`runner_acquire.go:520-528`, `runScopeID = row.RunScopeID`
read from the run-tree row). So thread `runScopeID` (a `shared.UUID`/its string form) into
`buildLockSpecs` as a NEW parameter from the acquire site, then set `spec.RunScopeID` next to
the existing `InstanceID` assignment inside `buildLockSpecs`. (Do not assume `InstanceID` and
the run-scope id are populated at the same place — they are not.)
Check: `grep -n 'RunScopeId:' lib/runtime/runner_dispatch.go lib/runtime/peer/client.go` shows both stamped.

### Task HOSTAGENT-2.3 — GREEN: proxy keys spawns + reap by run_scope_id
In `cmd/rimsky-host-agent-proxy/dispatch.go::resolveAndSpawn`, replace the
hard-coded `scopeID := instanceID` (line 131) with the run_scope_id read from the
inbound request. Thread `runScopeID string` as a parameter to `resolveAndSpawn`
(both call sites supply it): the executor handler passes `req.GetRunScopeId()`
(`executor_handler.go::Execute`), the claim-producer handler passes
`req.GetRunScopeId()` (`claim_producer_handler.go::Open`). Fall back to
`instanceID` only when `run_scope_id` is empty (degenerate / pre-field caller),
keeping the existing single-graph happy path working. The `runScopeBindingKey`
(`state.go:228`) and `spawnState.scopeID` continue to hold the chosen scope key.
In `lifecycle_handler.go::OnRunScopeTerminal` reap by `req.GetRunScopeId()` first
(falling back to `instance_id` only when run_scope_id is empty) so a per-run-scope
terminal reaps only that scope's child.
Check: `grep -n 'GetRunScopeId\|runScopeID' cmd/rimsky-host-agent-proxy/dispatch.go cmd/rimsky-host-agent-proxy/executor_handler.go cmd/rimsky-host-agent-proxy/claim_producer_handler.go cmd/rimsky-host-agent-proxy/lifecycle_handler.go` shows the threaded id.

### Task HOSTAGENT-2.4 — GREEN: update existing proxy unit tests + reap scenario for run-scope keying
The existing proxy unit harness (`harness_test.go`) and tests that build
`ExecuteRequest`/`OpenRequest` must supply a `run_scope_id` (or the fallback keeps
them passing). Update `test/scenarios/host_agent_reap_test.go`'s
`TestHostAgentReapOnRunScopeTerminal` if it relied on instance-keyed reap (its
header comment already flags the prior instance-vs-run-scope divergence) so it
asserts run-scope-keyed reap against production's run_scope_id ≠ instance_id.
Check: `go test ./cmd/rimsky-host-agent-proxy/... -count=1` passes.

### Task HOSTAGENT-2.5 — verify the build + the gate flips green
Run `go build ./... && go test ./lib/runtime/... ./cmd/rimsky-host-agent-proxy/... -count=1 && make lint`,
then the pass gate
`go test ./test/scenarios/ -run 'TestHostAgentPerRunScopeIsolation' -count=1`.
Check: all green; the named test passes.

---

## Pass HOSTAGENT-3 — Proto: claim-producer-verb-style protocol routing for all fronted protocols [proto-edit]

Type: proto-edit (followed by regen + Go work in HOSTAGENT-4). The `DispatchFrame`
already carries a `protocol` string + a `claim_producer_verb` enum. The agent's
unary-forward path is verb-keyed (`forwardClaimProducerUnary`) because
Commit/Abandon/Release are byte-identical at claim_id. The other protocols
(publisher/validation/data-processing) have multiple unary RPCs whose request
messages are also distinct types — the agent must know which RPC to invoke. Add a
generic per-protocol verb discriminator so every fronted protocol routes
uniformly.

### Task HOSTAGENT-3.1 — add a generic rpc_method discriminator to DispatchFrame
Edit `lib/protocols/proto/v1/host_agent.proto::DispatchFrame`: add
`string rpc_method = 7;` (the fully-qualified-or-short RPC name the agent must
invoke on the child for non-executor, non-claim-producer protocols — e.g.
`Subscribe`, `Validate`, `BeginCandidate`). Keep `claim_producer_verb` for the
claim-producer path (byte-identical-request reason documented in the existing
comment). Document that `rpc_method` is authoritative for publisher / validation /
data-processing dispatch, mirroring why `claim_producer_verb` is authoritative for
claim-producer.
Check: `grep -n 'rpc_method = 7' lib/protocols/proto/v1/host_agent.proto` returns the line.

### Task HOSTAGENT-3.2 — regenerate generated Go
Run `make proto-gen`.
Check: `grep -n 'GetRpcMethod' lib/protocols/proto/v1/gen/host_agent.pb.go` shows the accessor.

---

## Pass HOSTAGENT-4 — Late-bind ALL protocols: transparent forwarding, no Unimplemented stubs [working]

Story: `S-hostagent-latebind-all-protocols`. Replace the three
registered-but-Unimplemented stubs (`publisher`/`validation`/`data_processing`)
with real handlers that forward through the same `resolveAndSpawn` + agent-tunnel
mechanism the executor and claim-producer handlers use. The agent gains the
Capabilities-handshake + dispatch wiring for those three protocols. No protocol
ships as a stub; each presents exactly the fronted service's protocol.

```
GATE: go test ./test/scenarios/ -run 'TestHostAgentLateBindAllProtocols' -count=1
GATE-DIR: (repo root)
GATE-VERDICT (judge by OUTPUT, not exit code — a bare `-run` exits 0 on an absent test; EXECUTORS-cluster guarded form):
  RED  iff: output contains `FAIL` (or a real failure) AND NOT `no tests to run`
  GREEN iff: output contains `ok` AND NOT `no tests to run`
  `no tests to run` → NOT-satisfied; right-reason-RED is pinned to 4.1's `! go test … -run TestHostAgentLateBindAllProtocols` after the test is authored.
EXPECT-RED-PRE: before 4.1, `-run` matches nothing → `no tests to run`; after 4.1 authors the test it FAILs because a publisher/validation/data-processing dispatch through the proxy returns gRPC Unimplemented (main.go:74-76 + unimplemented_handlers.go) → right-reason RED
EXPECT-GREEN-POST: output contains `ok`, no `no tests to run` — each of the three additional protocols served by a real spawned local binary, none returns Unimplemented
RUN-NOW result: current-tree bare run exits 0 with `no tests to run` (test absent) → NOT a passing gate; the proxy registers Unimplemented{Publisher,Validation,DataProcessing}Server and the agent only handshakes/dispatches executor + claim_producer today
```

### Task HOSTAGENT-4.1 — RED: scenario test driving a validation + publisher + data-processing dispatch through the proxy to a real spawned binary
Add `test/scenarios/host_agent_latebind_all_protocols_test.go` with
`func TestHostAgentLateBindAllProtocols(t *testing.T)`. Extend the `stubchild`
binary to ALSO implement `Publisher`, `Validation`, and `DataProcessing` servers
(register them in `stubchild/main.go::main` alongside the existing
Executor/ClaimProducer registrations) with deterministic responses:
- a `Validate` that returns a rejecting `ValidationFinding` when a sentinel
  selector/role is present (so a deliberately-rejecting validator is observable),
- a `Subscribe`/message-push that records a real publish,
- a `BeginCandidate`/`CommitCandidate` that performs a deterministic typed-data op.

Then drive each protocol's dispatch through the REAL proxy + agent. For the
protocols the supervisor does not natively route in v1, exercise the proxy's
supervisor-facing handler directly over gRPC (dial the running proxy with the
`x-rimsky-service-name` header, as the in-process unit harness does) and assert
each returns a REAL outcome from the spawned binary — NOT gRPC `Unimplemented`:
the validation dispatch returns the rejecting finding; the publisher dispatch
returns success and the stub records the message; the data-processing dispatch
returns the committed candidate. Assert via `status.Code(err) != codes.Unimplemented`
on each path and on the positive observable outcome.
Check (must fail on current tree):
`! go test ./test/scenarios/ -run 'TestHostAgentLateBindAllProtocols' -count=1`

### Task HOSTAGENT-4.2 — GREEN: real proxy handlers for publisher / validation / data-processing
Delete `cmd/rimsky-host-agent-proxy/unimplemented_handlers.go`. Add
`publisher_handler.go`, `validation_handler.go`, `data_processing_handler.go`
(one feature per file, ~per existing handler shape) that each:
- implement the protocol's `*Server` interface,
- call the shared `resolveAndSpawn` with `expectedProtocols: [<protocol>]`,
- serialize the inbound request, tunnel it via `DispatchFrame{protocol, rpc_method, payload}`,
- await the response frame and unmarshal it into the protocol's response type,
- surface proxy-side faults via the protocol's natural error channel (gRPC status
  with `google.rpc.ErrorInfo` reason, matching `claimProducerStatus`).
Update `main.go` to register the three real handlers instead of the
`newUnimplemented*` constructors.
Check: `grep -rn 'newUnimplemented\|UnimplementedPublisherServer{}' cmd/rimsky-host-agent-proxy/main.go` returns nothing; `ls cmd/rimsky-host-agent-proxy/{publisher,validation,data_processing}_handler.go` lists the three files.

### Task HOSTAGENT-4.3 — GREEN: agent-side Capabilities handshake + dispatch for the three protocols
In `lib/runtime/hostagent/spawn.go::capabilitiesForProtocol` add cases for
`publisher`, `validation`, `data_processing` (call each protocol's Capabilities
RPC on the child conn; `publisher.proto` and `data_processing.proto` BOTH declare a
`rpc Capabilities(google.protobuf.Empty)` — grounded 2026-06-07). VALIDATION CAVEAT
(grounded 2026-06-07): `validation.proto`'s `service Validation` has NO `Capabilities`
RPC at all — it declares only `rpc Validate(ValidateRequest) returns (ValidateResponse)`.
A binary that fronts validation advertises its protocol set through the MULTI-PROTOCOL
service's capability surface (the claim-producer `CapabilitiesResponse.protocols`
field), which is what the agent handshake reads; the test stub in HOSTAGENT-4.1 carries
a claim-producer `Capabilities` RPC for exactly this reason. So the `validation` case
must NOT try to call a non-existent `Validation.Capabilities`; it should treat
validation as protocol-present-by-handshake (resolve its capability surface from the
service's claim-producer/multi-protocol Capabilities) rather than per-RPC. Acknowledge
the standalone-validation-only binary case explicitly: a binary that implements ONLY
`Validation` (no claim-producer Capabilities RPC) has no capability surface to read —
treat it as a no-capability validation forwarder (forward `Validate` dispatches without
a Capabilities handshake) rather than failing the spawn. In
`lib/runtime/hostagent/dispatch.go::handleDispatchFrame` add a uniform
non-executor unary path that switches on `df.GetProtocol()` then on
`df.GetRpcMethod()` to invoke the right RPC on the child (mirroring
`forwardClaimProducerUnary`, but driven by `rpc_method`). Define the protocol name
constants alongside the existing `protocolExecutor`/`protocolClaimProducer`.
Check: `grep -n 'protocolPublisher\|protocolValidation\|protocolDataProcessing\|GetRpcMethod' lib/runtime/hostagent/dispatch.go lib/runtime/hostagent/spawn.go` shows the new routing.

### Task HOSTAGENT-4.4 — verify the build + the gate flips green
Run `go build ./... && go test ./cmd/rimsky-host-agent-proxy/... ./lib/runtime/hostagent/... -count=1 && make lint`,
then the pass gate
`go test ./test/scenarios/ -run 'TestHostAgentLateBindAllProtocols' -count=1`.
Check: all green; the named test passes; the `TestUnimplementedHandlers` proxy
unit test is removed/retargeted (no longer asserts Unimplemented).

---

## Pass HOSTAGENT-5 — Proto: per-binding exec overrides on Binding [proto-edit]

Type: proto-edit (followed by regen + Go work in HOSTAGENT-6).

### Task HOSTAGENT-5.1 — extend the Binding message
Edit `lib/protocols/proto/v1/host_agent.proto::Binding` (currently `string path = 1;`
only). Add:
- `repeated string args = 2;`
- `map<string, string> env = 3;`
- `string cwd = 4;` (per-binding cwd override; falls back to the instance-level cwd)
- `int32 ready_timeout_seconds = 5;` (per-binding spawn-readiness timeout override)
Comment each as additive/backward-compatible (absent → today's defaults).
Check: `grep -n 'repeated string args = 2\|map<string, string> env = 3\|string cwd = 4\|ready_timeout_seconds = 5' lib/protocols/proto/v1/host_agent.proto` shows all four.

### Task HOSTAGENT-5.2 — regenerate generated Go
Run `make proto-gen`.
Check: `grep -n 'GetArgs\|GetEnv\|GetCwd\|GetReadyTimeoutSeconds' lib/protocols/proto/v1/gen/host_agent.pb.go` shows the Binding accessors.

---

## Pass HOSTAGENT-6 — Per-binding env/args/cwd/timeout overrides applied at exec() [working]

Story: `S-hostagent-per-binding-exec-overrides`. The `service_bindings` JSON shape
gains optional `args`/`env`/`cwd`/`timeout` per binding; the proxy carries them on
the `Binding` wire message; the agent applies them when it `exec()`s the child. A
binding with no overrides still spawns with inherited env + the global cwd/timeout.

```
GATE: go test ./test/scenarios/ -run 'TestHostAgentPerBindingExecOverrides' -count=1
GATE-DIR: (repo root)
GATE-VERDICT (judge by OUTPUT, not exit code — a bare `-run` exits 0 on an absent test; EXECUTORS-cluster guarded form):
  RED  iff: output contains `FAIL` (or a real failure) AND NOT `no tests to run`
  GREEN iff: output contains `ok` AND NOT `no tests to run`
  `no tests to run` → NOT-satisfied; right-reason-RED is pinned to 6.1's `! go test … -run TestHostAgentPerBindingExecOverrides` after authoring.
EXPECT-RED-PRE: before 6.1, `-run` matches nothing → `no tests to run`; after 6.1 authors the test it FAILs because the wire Binding carries only path (host_agent.proto:76-79), bindingSpec parses only Path (state.go:246-248), and spawn.go builds exec.Command(path) with no args, env = os.Environ()+RIMSKY_AGENT_PORT, single global cwd/timeout → right-reason RED
EXPECT-GREEN-POST: output contains `ok`, no `no tests to run` — the spawned child echoes back the per-binding args/env/cwd and a binding-specified short timeout bounds the spawn wait
RUN-NOW result: current-tree bare run exits 0 with `no tests to run` (test absent) → NOT a passing gate; no per-binding override is parsed or applied today
```

### Task HOSTAGENT-6.1 — RED: scenario test asserting the child ran with per-binding args/env/cwd and the binding timeout bounds the wait
Add `test/scenarios/host_agent_per_binding_exec_overrides_test.go` with
`func TestHostAgentPerBindingExecOverrides(t *testing.T)`. Extend `stubchild` with
a knob to echo its `os.Args[1:]`, a chosen env var, and its `os.Getwd()` back
through the Execute response (e.g. as NamedEvent payload fields, or appended to a
log file path passed via env). Drive the REAL fixture, binding `codegen` with a
binding JSON that carries `args`, `env`, and a per-binding `cwd` (a temp dir the
test creates). Assert the echoed argv/env/cwd equal what was declared. Add a
second sub-case: a binding whose `timeout` is shorter than the global default,
pointed at a stub started with `STUBCHILD_NO_BIND` (never binds its port) →
assert the dispatch fails with `spawn_failed` within the SHORT binding timeout
(not the 30s global default), proving the per-binding timeout bounds the wait.
A third sub-case: a binding with no overrides still spawns and succeeds
(backward compatible).
Check (must fail on current tree):
`! go test ./test/scenarios/ -run 'TestHostAgentPerBindingExecOverrides' -count=1`

### Task HOSTAGENT-6.2 — GREEN: parse override fields on the proxy binding spec + carry them on Spawn
Extend `bindingSpec` (`cmd/rimsky-host-agent-proxy/state.go:246-248`) with
`Args []string` (`json:"args,omitempty"`), `Env map[string]string`
(`json:"env,omitempty"`), `Cwd string` (`json:"cwd,omitempty"`),
`TimeoutSeconds int` (`json:"timeout_seconds,omitempty"`). In
`dispatch.go::spawnChild` populate the wire `Binding{Path, Args, Env, Cwd}` from
the parsed spec, and choose `ReadyTimeoutSeconds` as the per-binding override when
present, else the proxy's configured spawn timeout. Per-binding `Cwd` overrides
the instance-level `params.cwd` only when set.
Check: `grep -n 'Args\|Env\|Cwd\|TimeoutSeconds' cmd/rimsky-host-agent-proxy/state.go | grep binding -i` and `grep -n 'Args:\|Env:\|Cwd:' cmd/rimsky-host-agent-proxy/dispatch.go` show the fields wired onto the Spawn.

### Task HOSTAGENT-6.3 — GREEN: agent applies the overrides at exec()
In `lib/runtime/hostagent/spawn.go::handleSpawn` build the child command with the
binding overrides: `exec.Command(path, sp.GetBinding().GetArgs()...)`; set
`cmd.Dir` from the per-binding `Binding.cwd` when non-empty, else from `Spawn.cwd`
(today's behavior); build `cmd.Env` from `os.Environ()` + each `Binding.env` entry
+ `RIMSKY_AGENT_PORT` (binding env overrides inherited env on key collision, with
`RIMSKY_AGENT_PORT` always last so it wins). Keep the readyTimeout resolution as
`Spawn.ready_timeout_seconds` (the proxy already folds the per-binding override
into that field in 6.2).
Check: `grep -n 'GetArgs()\|GetEnv()\|GetBinding().GetCwd()' lib/runtime/hostagent/spawn.go` shows the overrides applied.

### Task HOSTAGENT-6.4 — verify the build + the gate flips green
Run `go build ./... && go test ./lib/runtime/hostagent/... ./cmd/rimsky-host-agent-proxy/... -count=1 && make lint`,
then the pass gate
`go test ./test/scenarios/ -run 'TestHostAgentPerBindingExecOverrides' -count=1`.
Check: all green; the named test passes.

---

## Pass HOSTAGENT-7 — Anonymous-mode late-bind: resolve the serving agent without an owner api-key [working]

Story: `S-hostagent-anonymous-mode-latebind`. Resolve
`tension:anonymous-mode-locks-out-late-bind`: an instance created under
anonymous-mode (null owner key) can still dispatch to late-bound services. The
chosen mechanism (per the tension's resolution direction): the agent registers an
anonymous-mode routing identity, and the proxy routes a dispatch for an
owner-less instance to a default/anonymous agent registration rather than
short-circuiting with `host_agent_not_connected`.

```
GATE: go test ./test/scenarios/ -run 'TestHostAgentAnonymousModeLateBind' -count=1
GATE-DIR: (repo root)
GATE-VERDICT (judge by OUTPUT, not exit code — a bare `-run` exits 0 on an absent test; EXECUTORS-cluster guarded form):
  RED  iff: output contains `FAIL` (or a real failure) AND NOT `no tests to run`
  GREEN iff: output contains `ok` AND NOT `no tests to run`
  `no tests to run` → NOT-satisfied; right-reason-RED is pinned to 7.1's `! go test … -run TestHostAgentAnonymousModeLateBind` after authoring.
EXPECT-RED-PRE: before 7.1, `-run` matches nothing → `no tests to run`; after 7.1 authors the test it FAILs because the proxy short-circuits at dispatch.go:117-119 returning host_agent_not_connected when entry.ownerAPIKeyID == "" (anonymous instances persist a null owner key) → right-reason RED
EXPECT-GREEN-POST: output contains `ok`, no `no tests to run` — an anonymous-mode instance's late-bound dispatch resolves to the connected agent, the child runs, and a real dispatch outcome returns
RUN-NOW result: current-tree bare run exits 0 with `no tests to run` (test absent) → NOT a passing gate; the owner-empty short-circuit at dispatch.go:117-119 is the today-behavior
```

### Task HOSTAGENT-7.1 — RED: scenario test driving a late-bound dispatch from an anonymous-mode instance
Add `test/scenarios/host_agent_anonymous_mode_latebind_test.go` with
`func TestHostAgentAnonymousModeLateBind(t *testing.T)`. Stand up the REAL
fixture in anonymous mode (do NOT mint an owner key — the harness's
`MintAdminKey` flips out of anonymous mode, so this fixture path must create the
instance via the anonymous path with no api-key owner). Connect a host-agent
under the anonymous-mode routing identity. Create the instance via the anonymous
path (null owner) with a `codegen` binding to the stub, deploy the late-bind
template, and assert the OBSERVABLE outcome: the worker reaches
`cascade.NodeStateFresh` and a `terminal/success` event is emitted — i.e. the
dispatch resolved + ran rather than terminating with `host_agent_not_connected`.
(Add a `fixtureOpts.anonymous` toggle to the host-agent fixture so it skips owner
minting and registers the agent under the anonymous routing identity.)
Check (must fail on current tree):
`! go test ./test/scenarios/ -run 'TestHostAgentAnonymousModeLateBind' -count=1`

### Task HOSTAGENT-7.2 — GREEN: anonymous routing identity on agent Register + proxy default route
Define a well-known anonymous-mode routing identity (a sentinel api-key-id value,
e.g. the constant the proxy treats as "anonymous agent"). The host-agent, when its
configured api-key is empty (anonymous deployment), registers under that sentinel
(`lib/runtime/hostagent/run.go` Register frame build). REQUIRED first step (grounded
2026-06-07): `hostagent.Run` hard-fails on an empty api-key BEFORE it ever connects —
`run.go:139-141` returns `errors.New("hostagent: RIMSKY_API_KEY is required")` when
`cfg.APIKey == ""`, and `connectOnce` (`run.go:222`) sends `ApiKey: cfg.APIKey`
(`run.go:236`) on the Register frame. So an anonymous agent never reaches Register
today. The task must EITHER (a) set the anonymous sentinel value as the agent's
effective `cfg.APIKey` (so the existing guard passes and Register carries the sentinel
on the wire), OR (b) relax the `run.go:139-141` guard to permit the empty-key
anonymous path and substitute the sentinel into the Register frame's `ApiKey`. Pick one
and apply it explicitly. In `cmd/rimsky-host-agent-proxy/dispatch.go::resolveAndSpawn`,
when `entry.ownerAPIKeyID == ""` (anonymous instance) resolve the agent by the
anonymous sentinel instead of returning `host_agent_not_connected` immediately;
only return `host_agent_not_connected` when no anonymous agent is connected
either. Keep the owner-keyed path unchanged for authenticated instances.
Check: `grep -n 'anonymous' cmd/rimsky-host-agent-proxy/dispatch.go lib/runtime/hostagent/run.go` shows the sentinel route on both sides, and the empty-api-key guard at `run.go:139-141` no longer blocks the anonymous path.

### Task HOSTAGENT-7.3 — verify the build + the gate flips green
Run `go build ./... && go test ./cmd/rimsky-host-agent-proxy/... ./lib/runtime/hostagent/... -count=1 && make lint`,
then the pass gate
`go test ./test/scenarios/ -run 'TestHostAgentAnonymousModeLateBind' -count=1`.
Check: all green; the named test passes. The existing
`TestOpenOwnerEmpty`/`TestExecuteOwnerEmpty` proxy unit tests are retargeted: an
owner-empty instance with NO anonymous agent connected still returns
`host_agent_not_connected` (the guard now discriminates "no anonymous agent" from
"owner empty"), keeping the negative path covered.

---

## Pass HOSTAGENT-8 — Anonymous-mode late-bind: resolve the tension [design-change]

Type: design-change pass for `S-hostagent-anonymous-mode-latebind`. No code gate
(the behavior gate is HOSTAGENT-7); this pass moves the tension and updates the
two concept docs the spec names.

### Task HOSTAGENT-8.1 — move the tension to _resolved/
Move `.ok-planner/design/tensions/anonymous-mode-locks-out-late-bind.md` to
`.ok-planner/design/tensions/_resolved/anonymous-mode-locks-out-late-bind.md`,
flip `status: open` → `status: resolved`, and append a `## Resolution` section (no
file paths per the tension surface rule) recording the chosen resolution: an
anonymous-mode agent registers under a well-known anonymous routing identity, and
the proxy routes owner-less-instance dispatches to that anonymous agent rather
than hard-failing — removing the mutual exclusion between `concept:anonymous-mode`
and late-bound services. Cite `spec:2026-06-06-comprehensive-gap-closure-design`.
Check: `test -f .ok-planner/design/tensions/_resolved/anonymous-mode-locks-out-late-bind.md && ! test -f .ok-planner/design/tensions/anonymous-mode-locks-out-late-bind.md`.

### Task HOSTAGENT-8.2 — update concept:host-agent-proxy + concept:anonymous-mode
In `design/concepts/host-agent-proxy.md` add/adjust an invariant (path-free) that
routing resolves the serving agent by the instance owner api-key OR, for
owner-less (anonymous-mode) instances, by the anonymous routing identity; append a
dated Notes entry citing the spec. In `design/concepts/anonymous-mode.md` Boundaries
or Invariants, add (path-free) that anonymous-mode instances may dispatch to
late-bound services via the anonymous routing identity (removing the prior
lock-out); append a dated Notes entry citing the spec.
Check: `grep -n 'anonymous routing identity\|late-bound\|late-bind' design/concepts/host-agent-proxy.md design/concepts/anonymous-mode.md` shows the new prose in both.

---

## Pass HOSTAGENT-9 — Conformance claim-producer terminals + retried-terminal idempotency [working]

Story: `S-conformance-claimproducer-terminals`. The conformance claim-producer
suite must drive Open → Commit / Abandon / Release on real claims the suite
itself Open'd, plus a retried (re-issued) terminal idempotency check, reporting
each as its own pass/fail row; FAIL + non-zero exit when a producer's terminal
errors or a duplicate terminal errors.

```
GATE: go test ./lib/services/test/scenarios/atomic_staging/ -run 'TestPGFusedStore_ClaimProducerConformance' -count=1
GATE-DIR: (repo root)
GATE-VERDICT: unlike the other HOSTAGENT working gates, this names an EXISTING test (`TestPGFusedStore_ClaimProducerConformance`) that PASSES on the current tree — the filter matches a real test, so the gate is GREEN-FROM-BIRTH and is NOT red merely because a test is absent. The RED→GREEN flip is engineered INSIDE the existing test: task 9.1 adds new sub-assertions that the returned `[]CheckResult` CONTAINS passing `Commit`/`Abandon`/`Release`/`TerminalIdempotency` rows. Judge by OUTPUT:
  RED  iff: output contains `FAIL` (the new containment sub-assertions fail because runner.go::Run never drives the terminal verbs)
  GREEN iff: output contains `ok` AND the terminal-verb rows are present
EXPECT-RED-PRE: AFTER 9.1 adds the containment assertions (and BEFORE 9.2 extends the runner), the existing test FAILs — the conformance result rows lack Commit/Abandon/Release/idempotency lines today (runner.go::Run, lines 41-139, emits only Capabilities/EnvelopeNonEmpty/OpenFirst/OpenSecond/Uniformity[+optional]; no terminal-verb row). NOTE: on the bare current tree (before 9.1) this gate is GREEN — that is expected; the red-for-the-right-reason verdict is the run AFTER 9.1 authors the assertions, which is what the orchestrator runs.
EXPECT-GREEN-POST: the conformance run reports passing Commit/Abandon/Release + TerminalIdempotency rows against the real bundled producer
RUN-NOW result: current tree → GREEN (the named existing test passes without the new assertions); the load-bearing RED is the post-9.1 run.
```
(This gate uses the existing real-producer conformance test under
`lib/services/test/scenarios/atomic_staging/pg_verifier_conformance_test.go`,
which already invokes `claimproducer.Run` against the bundled postgres producer
over the wire — the real value-delivering producer. Requires Docker.)

### Task HOSTAGENT-9.1 — RED: assert the conformance result set contains terminal + idempotency rows (real producer)
In `lib/services/test/scenarios/atomic_staging/pg_verifier_conformance_test.go`
(`TestPGFusedStore_ClaimProducerConformance`, which runs `claimproducer.Run` against the real
bundled postgres producer), add assertions that the returned `[]CheckResult`
includes passing rows named `Commit`, `Abandon`, `Release`, and
`TerminalIdempotency` (each with `Err == nil`). Also add an in-package unit test
`lib/protocols/conformance/claimproducer/runner_terminals_test.go` with a fake
`ClaimProducer` whose Commit returns an error, asserting `Run` yields a `Commit`
row with non-nil Err (so the FAIL path is pinned); and a second fake whose
duplicate terminal call errors, asserting the `TerminalIdempotency` row fails.
Check (must fail on current tree):
`! go test ./lib/services/test/scenarios/atomic_staging/ -run 'TestPGFusedStore_ClaimProducerConformance' -count=1`
and `! go test ./lib/protocols/conformance/claimproducer/ -run 'TestRunTerminals' -count=1`.

### Task HOSTAGENT-9.2 — GREEN: drive Commit/Abandon/Release + retried-terminal in the runner
Extend `lib/protocols/conformance/claimproducer/runner.go::Run` after the
existing checks: for each terminal verb, Open a fresh claim against the producer
(synthetic selector + fresh `claim_id`), then drive the verb on the claim using
the Open'd claim's scope/address, appending a `CheckResult{Name: "Commit"|"Abandon"|"Release"}`
(Err set on failure). Add a `TerminalIdempotency` check: Open a claim, issue the
same terminal verb twice (same claimID + scope + address), asserting the second
call is accepted without error (idempotent retry). Each check is independent
(failures don't short-circuit, matching the file's existing pattern). Skip
gracefully (SKIP-marker row, like `SplitScopeSkipped`) only when the producer
returns Unavailable for the synthetic Open (a drained pick-policy queue) so the
suite stays runnable against queue-shaped producers — but the real bundled
producer under the gate must produce the passing rows.
Check: `grep -n '"Commit"\|"Abandon"\|"Release"\|"TerminalIdempotency"' lib/protocols/conformance/claimproducer/runner.go` shows the new checks.

### Task HOSTAGENT-9.3 — GREEN: correct the CLI docstring drift
In `cmd/rimsky/conformance.go::runConformanceClaimProducer` (docstring ~160-164)
correct the false "the four runtime verbs" claim to accurately describe what the
suite now drives (Capabilities + envelope/uniformity + the terminal verbs
Commit/Abandon/Release + terminal-idempotency-under-retry + optional
SplitScope/ScopesConflict).
Check: `grep -n 'TerminalIdempotency\|Commit/Abandon/Release\|four runtime verbs' cmd/rimsky/conformance.go` shows the corrected prose (and no surviving false claim).

### Task HOSTAGENT-9.4 — verify the build + both gates flip green
Run `go build ./... && go test ./lib/protocols/conformance/claimproducer/... -count=1 && make lint`,
then the pass gate
`go test ./lib/services/test/scenarios/atomic_staging/ -run 'TestPGFusedStore_ClaimProducerConformance' -count=1`
(Docker up; this needs the bundled images per CLAUDE.md — run `make service-images`
first if the harness reports a missing local image).
Check: all green; the conformance run reports passing terminal + idempotency rows.

---

## Pass HOSTAGENT-10 — Conformance terminals: CLI exit-code + FAIL-on-broken-producer end-to-end [working]

Story: `S-conformance-claimproducer-terminals` (CLI/acceptance leg). Prove the
`rimsky conformance claim-producer` SUBCOMMAND prints a passing terminal/idempotency
row against a real producer and FAILs with non-zero exit against a producer whose
Commit (or duplicate terminal) errors.

```
GATE: go test ./lib/services/test/scenarios/atomic_staging/ -run 'TestConformanceClaimProducerTerminalsCLI' -count=1
GATE-DIR: (repo root)
GATE-VERDICT (judge by OUTPUT, not exit code — a bare `-run` exits 0 on an absent test; EXECUTORS-cluster guarded form):
  RED  iff: output contains `FAIL` (or a real failure) AND NOT `no tests to run`
  GREEN iff: output contains `ok` AND NOT `no tests to run`
  `no tests to run` → NOT-satisfied; right-reason-RED is the post-10.1 run after the test is authored.
EXPECT-RED-PRE: before 10.1, `-run` matches nothing → `no tests to run`; even with the runner extended there is no end-to-end test driving the built CLI binary against a real producer + a broken producer asserting stdout rows + exit codes (10.1 is RED+GREEN — it authors the test against the runner change already landed in HOSTAGENT-9)
EXPECT-GREEN-POST: output contains `ok`, no `no tests to run` — CLI exits 0 with passing terminal rows vs a real producer, exits non-zero with FAIL rows vs a broken producer
RUN-NOW result: current-tree bare run exits 0 with `no tests to run` (test absent) → NOT a passing gate; the CLI path is unverified for terminal rows / exit codes today
```

### Task HOSTAGENT-10.1 — RED+GREEN: build the CLI, run it against a real producer and a broken producer, assert rows + exit codes
Add `lib/services/test/scenarios/atomic_staging/conformance_claimproducer_cli_test.go`
(co-located with the existing real-producer conformance harness) with
`func TestConformanceClaimProducerTerminalsCLI(t *testing.T)`. Build the `rimsky`
CLI binary (`go build ./cmd/rimsky`), stand up the REAL bundled postgres
claim-producer over gRPC (reuse the atomic_staging harness's producer launch),
run `rimsky conformance claim-producer --endpoint grpc://<addr>` as a subprocess,
and assert: exit code 0, stdout contains `ok    Commit`, `ok    Abandon`,
`ok    Release`, `ok    TerminalIdempotency`. Then stand up a deliberately-broken
producer (a tiny in-test gRPC server whose `Commit` returns an error — register
only the ClaimProducer service with a stub Open that returns Acquired and a
Commit that returns `status.Error`), run the same CLI against it, and assert:
non-zero exit and stdout contains `FAIL  Commit`. Because the runner change
landed in HOSTAGENT-9, the real-producer half is GREEN once 10.1 lands; the
broken-producer half is also driven by real CLI exit-code logic
(`conformance.go:200-203` already returns 1 on any failed row).
Check: `go test ./lib/services/test/scenarios/atomic_staging/ -run 'TestConformanceClaimProducerTerminalsCLI' -count=1` passes.

### Task HOSTAGENT-10.2 — verify the build + the gate flips green
Run `go build ./... && make lint`, then the pass gate
`go test ./lib/services/test/scenarios/atomic_staging/ -run 'TestConformanceClaimProducerTerminalsCLI' -count=1`
(Docker up; `make service-images` if the harness needs the bundled image).
Check: all green; the named test passes.

---

## Pass HOSTAGENT-11 — Design-doc updates for host-agent-proxy invariants [design-change]

Type: design-change pass. Carries the two `concept:host-agent-proxy` doc edits the
spec's "Design changes" section names for the per-run-scope-isolation and
all-protocols stories. (The anonymous-mode concept edits + tension move live in
HOSTAGENT-8; the per-binding-overrides story is `designChange: none`.) No code
gate — these ride alongside the code landed in HOSTAGENT-2 and HOSTAGENT-4.

### Task HOSTAGENT-11.1 — concept:host-agent-proxy — make the run-scope-keyed-spawn invariant real
In `design/concepts/host-agent-proxy.md`, the existing Invariant (b) already says
"one spawn per (run-scope, binding-name) … reaped on run-scope termination."
Append a dated Notes entry (path-free) recording that v1 now threads the run-scope
id onto the dispatch wire so the proxy keys spawns by the real run-scope rather
than collapsing all run-scopes of an instance onto one child — making the invariant
actually enforced, not aspirational. Cite `spec:2026-06-06-comprehensive-gap-closure-design`.
Check: `grep -n 'run-scope id onto the dispatch\|keys spawns by the real run-scope' design/concepts/host-agent-proxy.md` returns the new Notes line.

### Task HOSTAGENT-11.2 — concept:host-agent-proxy — protocol-transparency invariant
In `design/concepts/host-agent-proxy.md` Invariants, add a new invariant
(path-free): the proxy is a transparent forwarder of every rimsky service
protocol it fronts (executor, claim-producer, publisher, validation,
data-processing) by one uniform spawn/forward mechanism, each presenting exactly
the fronted service's protocol; no protocol ships as a registered-but-unimplemented
stub, and a service that conforms to its own protocol works behind the proxy by
construction — so the proxy adds no separate conformance surface (no host-agent /
proxy conformance suite). Update the "## Notes" entry that currently says
publisher/validation/data-processing "ship registered but unimplemented" with a
dated follow-on Notes entry recording they are now fully forwarded. Cite the spec.
Check: `grep -n 'transparent forwarder\|no separate conformance surface' design/concepts/host-agent-proxy.md` returns the new invariant prose.

### Task HOSTAGENT-11.3 — regenerate the concepts TOC
Run the concepts.md regeneration step so `design/concepts.md`'s one-line entry for
host-agent-proxy stays consistent (no slug added/removed here, so this is a
consistency pass).
Check: `grep -n 'host-agent-proxy' .ok-planner/design/concepts.md` still resolves to one entry.

---

## EXECUTORS — conventions used by this cluster

**Gate-run trap (READ FIRST).** Both runners under this cluster exit 0 when a
name filter matches nothing — verified against the current tree:

- `npx vitest run -t '<name>'` matching nothing → **exit 0**, output ends with
  `Tests <N> skipped` and `0 passed`.
- `go test -run '<name>' ./...` matching nothing → **exit 0**, prints
  `ok ... [no tests to run]`.

Therefore every RED/GREEN/acceptance gate below specifies its check as the
**explicit count assertion** the runner must satisfy, NEVER a bare exit code:

- vitest: gate passes only when stdout shows the named test ran with the
  intended verdict — RED gate requires `Tests` line to show `≥1 failed`
  (`npx vitest run -t '<name>' 2>&1 | grep -E 'Tests.*[1-9][0-9]* failed'`);
  GREEN gate requires `npx vitest run -t '<name>' 2>&1 | grep -E 'Tests.*[1-9][0-9]* passed'`
  AND `grep -vq 'no tests'`. Filter on the unique `it()` description string,
  which is globally unique across the suite.
- go: RED gate requires `go test -run '<name>' ./... 2>&1 | grep -E 'FAIL'`
  with no `[no tests to run]`; GREEN gate requires `ok` AND
  `grep -vq 'no tests to run'`.

**Test commands** (cwd is always absolute per agent rules):

- claude-agent vitest: `(cd lib/services/executors/claude-agent && npx vitest run -t '<desc>')`
- http-node go: `go test -run '<name>' ./lib/services/executors/http-node/...`
- verifier go: `go test -run '<name>' ./lib/services/executors/verifier-shape-checks/...`
- examples go: `go test -run '<name>' ./examples/...`

**Build gates** after every cluster of TS edits:
`(cd lib/services/executors/claude-agent && npm run build)`; after Go edits:
`go build ./...`; final `make lint`. The full final verification block lives
in the ACCEPTANCE GATES section.

---

## Pass EXECUTORS-1 — Sign-off gate binds the run's REAL effective bound output (both writeback paths)

**Story:** `S-executors-signoff-binds-real-output`
**Originating design:** `2026-06-04-claude-agent-signoff-gate-design` (Non-goals
§ "Incremental `attributes_set` writeback interacting with the gate" — this pass
un-defers it). **Design-change:** `concept:write-semantics`.

**Root cause (grounded):** `code:lib/services/executors/claude-agent/src/agent-run.ts#717-722`
calls `verifyRequiredSignoffs(required, attributesDelta, dispatchId, signoffs)`
with `attributesDelta` = the terminal-final `report_complete` delta, which is
`null` whenever the agent used the incremental `attributes_set` callback path
(the `report_complete` tool description at
`code:lib/services/executors/claude-agent/src/internal-mcp-tools.ts#99-101` tells
the agent to OMIT `attributes_delta` when it used incremental writeback). With
`attributesDelta=null`, `valueAtPath(null, path)` returns `undefined` and
`buildSignoffMessage` (`code:.../src/signoff.ts#33-36`) canonicalizes to the
literal string `"null"` — so the gate verifies a signature over `"null"`, and an
unattested run passes the security gate. The `attributes_set` writeback is wired
at `code:.../src/agent-run.ts#506-522` (`onAttributesSet`), which POSTs each
`delta` to the supervisor but keeps NO local copy.

**Fix shape:** accumulate the incremental writeback state inside the run.
`onAttributesSet` merges each accepted `delta` into a run-local
`accumulatedWriteback: Record<string, unknown>` (shallow merge, last-write-wins,
mirroring how the supervisor merges). At gate time, the value the gate binds is
the **effective bound bag** = `{ ...accumulatedWriteback, ...(attributesDelta ?? {}) }`
(terminal-final delta layered on top of accumulated writebacks). Pass that merged
bag — not the raw `attributesDelta` — to `verifyRequiredSignoffs`. This is
exactly the bag the supervisor will commit, so the executor binds the same value
the supervisor persists.

### Task EXECUTORS-1.1 — RED: gated dispatch with incremental writeback + null delta is rejected/accepted on the REAL bound value

- **Action:** Add `it("sign-off gate binds the accumulated incremental writeback when report_complete omits attributes_delta", …)`
  to `lib/services/executors/claude-agent/src/signoff-gate.e2e.test.ts`. Drive the
  **real** HTTP-bridge `/execute` entry point (same harness as the existing two
  cases in that file — `startHttpBridge`, a `makeReportingHandle`-style fake
  `CliRunner` that connects a REAL MCP client, the REAL Ed25519 signer from
  `signoff-test-signer.ts`, REAL `verifyRequiredSignoffs`). The fake CLI:
  (1) calls `attributes_set` with `{ delta: { endpoints: [{ url: "x" }] } }`,
  (2) then calls `report_complete` with `attributes_delta` OMITTED (null).
  Two sub-cases asserting OBSERVABLE outcome on the callback URL:
  - **Case A (stale/unsigned):** `signoffs` carries a signature produced over the
    literal `"null"` (i.e. over the old broken bytes). The bridge MUST POST
    `AsyncCallbackBody{ error: { error_class: "agent/signoff_unobtained" } }`.
  - **Case B (correctly signed):** `signoffs` carries a signature the test signer
    produced over the canonical bytes of the ACTUAL accumulated value at
    `path: endpoints` (`[{url:"x"}]`). The bridge MUST POST
    `AsyncCallbackBody{ success: { … } }` whose committed delta includes
    `endpoints`.
- **Check (RED):** `(cd lib/services/executors/claude-agent && npx vitest run -t 'sign-off gate binds the accumulated incremental writeback' 2>&1 | grep -E 'Tests.*[1-9][0-9]* failed')`
  returns a match (test ran and FAILED today: Case A passes spuriously because
  the gate verifies over `"null"`, and Case B fails because the real value is
  never reconstructed). Confirm `grep -vq 'no tests'`.

### Task EXECUTORS-1.2 — GREEN: accumulate writeback and bind the effective bag

- **Action:** In `lib/services/executors/claude-agent/src/agent-run.ts`: declare a
  run-local `const accumulatedWriteback: Record<string, unknown> = {}` before the
  `onAttributesSet` definition (#506); inside `onAttributesSet`, on a non-error
  POST result, `Object.assign(accumulatedWriteback, delta)`. In the `onComplete`
  sign-off block (#694-728), compute
  `const effectiveBag = { ...accumulatedWriteback, ...(attributesDelta ?? {}) }`
  and call `verifyRequiredSignoffs(required, effectiveBag, dispatchId, signoffs ?? [])`.
  Update the block comment at #686-693 to state the gate binds the effective
  bound bag (accumulated writeback merged with terminal-final delta), keeping the
  tamper-proof note (required/dispatchId still come from dispatch-time inputs, not
  from the bag).
- **Check (GREEN):** `(cd lib/services/executors/claude-agent && npx vitest run -t 'sign-off gate binds the accumulated incremental writeback' 2>&1 | grep -E 'Tests.*[1-9][0-9]* passed')`
  matches and `grep -vq 'no tests'`.

### Task EXECUTORS-1.3 — Regression guard: existing terminal-delta gate path still binds correctly

- **Action:** Run the pre-existing two `signoff-gate.e2e.test.ts` cases plus the
  `signoff.test.ts` unit suite to confirm the terminal-delta path (where
  `attributesDelta` is non-null and `accumulatedWriteback` is empty) is unchanged
  — the merge with an empty accumulator is identity.
- **Check:** `(cd lib/services/executors/claude-agent && npx vitest run src/signoff-gate.e2e.test.ts src/signoff.test.ts 2>&1 | grep -E 'Tests.*passed' )` shows all passing, no failures.

### Task EXECUTORS-1.4 — DESIGN-CHANGE: record write-semantics binds effective bound attributes

- **Action:** Append a dated Notes entry to
  `.ok-planner/design/concepts/write-semantics.md` (the only concept file the spec
  Design-changes section assigns to this story): state that the sign-off gate
  binds the run's **effective bound attributes** — the terminal-final delta merged
  with any incremental `attributes_set` writebacks — not whatever rides
  `report_complete`; on the incremental path the executor must reconstruct the
  accumulated writeback state the supervisor will commit. Path-free per the
  self-containment rule; cite `spec:2026-06-06-comprehensive-gap-closure-design`.
- **Check:** `grep -q 'effective bound attributes' .ok-planner/design/concepts/write-semantics.md`
  and `grep -q '2026-06-06-comprehensive-gap-closure-design' .ok-planner/design/concepts/write-semantics.md`.

### Task EXECUTORS-1.5 — build

- **Action:** none (verify).
- **Check:** `(cd lib/services/executors/claude-agent && npm run build)` exits 0.

**Pass EXECUTORS-1 — working gate (metadata):**
- **kind:** vitest behavioral e2e (real HTTP-bridge entry point + real Ed25519).
- **cmd:** `(cd lib/services/executors/claude-agent && npx vitest run -t 'sign-off gate binds the accumulated incremental writeback')`
- **pass condition:** stdout shows `Tests …[1-9]+ passed` and no `no tests`.
- **green-from-birth check:** RUN this cmd against the CURRENT tree before any
  edit — it MUST be RED (`…failed`) or report `no tests` (test not yet added).
  Recorded expectation: today the test does not exist → `no tests` (so 1.1 must
  add it AND demonstrate the red `failed` count once added).
- **filter-matches-nothing check:** the cmd above, run on the current tree,
  prints `0 passed` / `no tests` — gate logic treats that as NOT-satisfied.

---

## Pass EXECUTORS-2 — claude-agent MCP catalog + `{ref:}` + four transports + `allow_inline` policy

**Story:** `S-executors-mcp-catalog-transports`
**Originating design:** `2026-05-08-platform-extensions-for-agent-consumers-design`
§ Gap 1 (startup catalog, `{ref:}`, http/stdio/module/http-loopback,
`policy.allow_inline`). **Design-change:** none.

**Grounded today:** `code:lib/services/executors/claude-agent/src/cli-runner.ts#59-64`
`CliToolConfig` has only `kind: "mcp-http"` + `{name,url,headers}`; `mcpConfigJson`
(#315-327) emits only `type:"http"`. `code:.../src/server.ts::parseCliConfig`
(#709) + `parseMcpServers` (#761) parse only inline `{name,url,headers}` — no
catalog, no `{ref:}`, no transport field, no `allow_inline`.
`code:.../src/main.ts#33-87` reads no catalog config at startup.

**Fix shape:**
1. Startup catalog: `main.ts` reads a catalog from
   `env:RIMSKY_EXECUTOR_MCP_CATALOG` (path to a YAML/JSON file) and an
   `env:RIMSKY_EXECUTOR_MCP_ALLOW_INLINE` policy flag (default false), parsed once
   into a `McpCatalog` map keyed by name; each entry declares `transport`
   (`http | stdio | module | http-loopback`) plus transport-specific fields.
   The catalog + policy thread into `AgentRunOptions` (already the carrier for
   `cliConfig`).
2. Extend `CliToolConfig` to a discriminated union over the four transports
   (`mcp-http` kept as the http leaf). `mcpConfigJson` emits the correct
   `--mcp-config` entry per transport (`type: "http"` | `"stdio"` with
   `command`/`args`; `module`/`http-loopback` resolved to a loopback http URL the
   executor stands up per-dispatch, then emitted as `type:"http"`).
3. `parseMcpServers` accepts `{ref: <name>}` (resolves against the catalog) OR an
   inline `{name,url,headers}` (rejected with a config error when
   `allow_inline=false`). Resolution happens at dispatch in `agent-run.ts` at the
   SINGLE `hostServers` map-build site (`agent-run.ts:793`, `const hostServers = (cliConfig?.mcpServers ?? []).map(...)`)
   — grounded 2026-06-07: there are NOT "three spawn sites"; the `hostServers` array is
   built ONCE at #793 and then spread into the one `cliRunner.spawn` (the `...hostServers`
   spreads at #839/#858) and the one `cliRunner.resume` path (#1073). Do the catalog
   `{ref:}` / `${env:}` resolution at that single build site so both the spawn and resume
   paths inherit the resolved list.

### Task EXECUTORS-2.1 — RED: stdio-catalog ref resolves + inline rejected under allow_inline=false (real CLI spawn config)

- **Action:** Add `it("resolves a stdio catalog ref to a stdio --mcp-config entry and rejects inline servers when allow_inline is false", …)`
  to `lib/services/executors/claude-agent/src/mcp-servers-wiring.test.ts` (the
  existing wiring test that inspects the spawn `CliSpawnRequest.tools` / the
  emitted `--mcp-config`). Build a catalog `{ "shape-validator": { transport: "stdio", command: "shape-validator", args: [...] } }`, `allow_inline=false`.
  - Dispatch a node whose `cli.mcp_servers` is `[{ ref: "shape-validator" }]`
    through the real spawn-assembly path (the fake `CliRunner` captures the
    `CliSpawnRequest`); assert the emitted `--mcp-config` for `shape-validator`
    has `type: "stdio"` with the catalog's `command`/`args` (NOT `type:"http"`),
    AND its tools are folded into `--allowedTools`.
  - Dispatch a node whose `cli.mcp_servers` is `[{ name: "x", url: "http://x" }]`
    (inline, no ref); assert the dispatch is REJECTED with a config error citing
    `allow_inline`.
- **Check (RED):** vitest `-t 'resolves a stdio catalog ref to a stdio'` shows
  `…failed` (today: `CliToolConfig` has no stdio leaf, `parseMcpServers` has no
  `{ref:}` branch, no `allow_inline` policy). Confirm `grep -vq 'no tests'`.

### Task EXECUTORS-2.2 — GREEN: catalog + ref + stdio transport + allow_inline policy

- **Action:** Implement the startup catalog parse in `main.ts` (new
  `lib/services/executors/claude-agent/src/mcp-catalog.ts` holding `McpCatalog`
  type, `parseCatalog`, `parsePolicy`); extend `CliToolConfig` union + `mcpConfigJson`
  in `cli-runner.ts` for the stdio leaf; extend `parseMcpServers` in `server.ts`
  AND its `@source`-tracked mirror in `http-bridge.ts` to handle `{ref:}` +
  inline-policy rejection; resolve refs against the catalog at the SINGLE
  `hostServers` map-build site in `agent-run.ts` (`#793`) — NOT "three spawn sites"
  (grounded 2026-06-07: the `hostServers` array is built once at #793 and spread into
  the one `cliRunner.spawn` and the one `cliRunner.resume` path, so resolving at the
  build site covers both).
- **Check (GREEN):** vitest `-t 'resolves a stdio catalog ref to a stdio'` shows a
  positive `Tests …[1-9][0-9]* passed` count (vitest prints `0 passed`/`Tests …skipped`,
  never literal `no tests` — rely on the positive-count guard).

### Task EXECUTORS-2.3 — RED: http-loopback and module transports reach terminal success end-to-end

- **Action:** Add `it("dispatches successfully using a module-transport catalog server and an http-loopback-transport catalog server", …)`
  to the e2e suite (extend `signoff-gate.e2e.test.ts` harness or add to
  `lifecycle.e2e.test.ts`-style real-bridge harness). For each of `module` and
  `http-loopback` transports: catalog entry resolving to a tiny in-tree MCP module
  exposing ONE tool; dispatch a node referencing it by `{ref:}`; the fake CLI
  calls that tool then `report_complete`; assert the dispatch reaches terminal
  success (real `/execute` callback `AsyncCallbackBody.success`).
- **Check (RED):** vitest `-t 'dispatches successfully using a module-transport'`
  shows `…failed` today (transports unimplemented). `grep -vq 'no tests'`.

### Task EXECUTORS-2.4 — GREEN: module + http-loopback transports

- **Action:** Implement `module` (dynamic `import()` of the module at dispatch,
  per-dispatch lifetime) and `http-loopback` (import module, stand up a loopback
  http MCP listener on an ephemeral port, point `--mcp-config` at the URL,
  tear down at dispatch end) in `cli-runner.ts` + the single `hostServers` map-build
  site in `agent-run.ts` (`#793`, the one place that feeds both the `cliRunner.spawn`
  and `cliRunner.resume` paths — not "three spawn sites"). Per-dispatch teardown wired
  into the existing `teardownCli`.
- **Check (GREEN):** vitest `-t 'dispatches successfully using a module-transport'`
  shows a positive `Tests …[1-9][0-9]* passed` count (vitest never prints literal
  `no tests`; rely on the positive-count guard, treating `0 passed` as NOT-satisfied).

### Task EXECUTORS-2.5 — build

- **Action:** none.
- **Check:** `(cd lib/services/executors/claude-agent && npm run build)` exits 0.

**Pass EXECUTORS-2 — working gate (metadata):**
- **kind:** vitest (wiring config-inspection + real-bridge e2e).
- **cmd (single runnable command, one `-t` alternation covering both the wiring test
  from 2.1 and the e2e test from 2.3):**
  `(cd lib/services/executors/claude-agent && npx vitest run -t 'resolves a stdio catalog ref to a stdio|dispatches successfully using a module-transport')`
  (do NOT pass two `-t` flags — vitest errors; use the regex alternation).
- **pass condition:** both named tests appear and report a positive `Tests …[1-9][0-9]* passed`
  count (vitest never prints literal `no tests`; rely on the positive-count guard,
  treating an empty / `0 passed` run as NOT-satisfied).
- **green-from-birth check:** RUN on the current tree pre-edit — MUST be empty/`0 passed`
  (tests do not exist yet); after 2.1–2.4 both pass.
- **filter-matches-nothing check:** as in EXECUTORS-1 (empty / `0 passed` → NOT-satisfied).

---

## Pass EXECUTORS-3 — claude-agent emits the four declared error classes on the wire

**Story:** `S-executors-claude-agent-error-classes`
**Originating design:** `2026-05-23-signal-taxonomy-and-policy-decoupling-design`
§ claude-agent vocabulary (`agent/rate_limited`, `agent/context_exceeded`,
`agent/refused`, `agent/tool_use_failed/<tool>`). **Design-change:** none.

**Grounded today:** the four classes appear ONLY in the declaration list at
`code:.../src/expected-attributes-schema.ts#180-183` with ZERO emit sites. Rate
limits divert to `park_requested` (`code:.../src/agent-run.ts#997-1038` via
`rate-limit.ts`) so `agent/rate_limited` is never an Error; the other three
collapse into the generic `onError` path (#751-756). Subprocess stderr is the
classification source (`detectRateLimit` is the existing precedent for
stderr-grep classification).

**Fix shape:** add a `classifyAgentError(stderr, exitCode, toolName?)` helper
(new `lib/services/executors/claude-agent/src/error-classify.ts`) that maps
subprocess output to the hierarchical leaves:
- context-window-exceeded stderr → `agent/context_exceeded`
- model-refusal stderr → `agent/refused`
- tool-invocation failure (the MCP tool surface reports a failed tool) →
  `agent/tool_use_failed/<tool>` (hierarchical leaf with the offending tool name)
- rate-limit stderr WITH `cli.handle_rate_limits=false` → `agent/rate_limited`
  (when `handle_rate_limits` is true the existing auto-park path still wins; when
  false the rate-limit is surfaced as this Error class instead of parking).
The exit-recovery path (#997+) and the `onError` path (#751) route through the
classifier so the emitted `Error.error_class` is the precise leaf.

### Task EXECUTORS-3.1 — RED: four real dispatches resolve to the exact error_class leaves

- **Action:** Add `it("emits agent/context_exceeded, agent/refused, agent/tool_use_failed/<tool>, and agent/rate_limited (handle_rate_limits=false) as terminal Error.error_class", …)`
  driving the real HTTP-bridge `/execute` entry point with four fake-CLI handles,
  each surfacing (respectively) a context-exceeded stderr, a refusal stderr, a
  tool-use failure, and (with `cli.handle_rate_limits=false`) a rate-limit stderr.
  Assert each callback `AsyncCallbackBody.error.error_class` equals
  `agent/context_exceeded`, `agent/refused`, `agent/tool_use_failed/<tool>`
  (assert hierarchical leaf has the tool name), `agent/rate_limited` respectively.
  Add a sub-assertion that each emitted class is a member of `declaredErrorClasses`
  (the `agent/tool_use_failed/*` wildcard covers the leaf).
- **Check (RED):** vitest `-t 'emits agent/context_exceeded'` shows `…failed`
  today (all four collapse to the generic class / park). `grep -vq 'no tests'`.

### Task EXECUTORS-3.2 — GREEN: classify and emit

- **Action:** Add `error-classify.ts`; wire it into `agent-run.ts` `onError`
  (#751) and the exit-recovery branch (#997-1038) so `cli.handle_rate_limits=false`
  routes a detected rate-limit to `safeResolve({kind:"errored", errorClass:"agent/rate_limited"})`
  instead of park; the other three leaves emit from the classifier. Keep the
  default `handle_rate_limits=true` auto-park behavior intact.
- **Check (GREEN):** vitest `-t 'emits agent/context_exceeded'` shows `…passed`,
  no `no tests`.

### Task EXECUTORS-3.3 — build

- **Action:** none.
- **Check:** `(cd lib/services/executors/claude-agent && npm run build)` exits 0.

**Pass EXECUTORS-3 — working gate (metadata):**
- **kind:** vitest behavioral e2e (real bridge, real subprocess classification of fake stderr).
- **cmd:** `(cd lib/services/executors/claude-agent && npx vitest run -t 'emits agent/context_exceeded')`
- **pass condition:** named test `…passed`, no `no tests`.
- **green-from-birth check:** RUN on current tree pre-edit — `no tests` (test not yet added); after 3.1, `…failed`.
- **filter-matches-nothing check:** as above.

---

## Pass EXECUTORS-4 — http-node parks on 429 with computed resume_at, supervisor auto-wakes

**Story:** `S-executors-http-node-429-park-resume`
**Originating design:** `2026-05-14-subscription-cascade-and-quality-of-life-design`
(§ deferred "Rate-limit park behavior in http-node" — un-deferred here, REUSING
the existing Park/`resume_at` auto-wake mechanism). **Design-change:** none.

**Grounded today:** `code:lib/services/executors/http-node/server.go#225-227`
sends ANY unexpected status (including 429) to `sendErrored` via
`classifyUnexpectedStatus` (#383-396), which maps a 429-with-no-error_class-body
to `http/expectation_mismatch` — a hard Error terminal. http-node never emits
Park, though `&genv1.StreamClose_Park{Park: &genv1.Park{Reason: genv1.ParkReason_PARK_REASON_SNOOZE, ResumeAt: …}}`
is the exact shape already used elsewhere (reference:
`code:lib/protocols/conformance/executor/callback_receiver.go#249-262`; the
claude-agent rate-limit path emits the same `PARK_REASON_SNOOZE` + `resume_at`).

**Fix shape:** in `executeCore`, BEFORE the generic `statusOK` error branch,
special-case `resp.StatusCode == 429`: compute `resumeAt` from the `Retry-After`
header (HTTP-date OR delta-seconds, per RFC 9110) and emit a StreamClose Park with
`Reason: PARK_REASON_SNOOZE` and `ResumeAt: timestamppb.New(resumeAt)` via a new
`sendParked(send, resumeAt, reasonNote)` helper. No new park machinery — the
supervisor's existing `SweepParkedNodes` wakes the node at `resume_at`.

### Task EXECUTORS-4.1 — RED: 429 → Park{SNOOZE, resume_at}; auto-wake → 200 → success

- **Action:** Add `TestHttpNode_429ParksWithResumeAtAndAutoWakes` to
  `lib/services/executors/http-node/server_test.go`. Stand up an `httptest`
  upstream that returns 429 with `Retry-After: <n seconds>` on the first call and
  200 with a JSON object body on a subsequent call. Drive the REAL `executeCore`
  (the same call shape existing tests use) and assert the terminal is a
  `StreamClose_Park` whose `Park.Reason == PARK_REASON_SNOOZE` and whose
  `Park.ResumeAt` is ≈ now + Retry-After seconds (within tolerance), NOT a
  `StreamClose_Error`. Then drive a second dispatch (simulating the supervisor's
  re-dispatch at `resume_at`) and assert it reaches `StreamClose_Success`. (The
  supervisor wake path itself is the real `SweepParkedNodes` exercised in the
  full-stack ACCEPTANCE gate below; this RED test pins the executor's Park-emit +
  resume-success contract.)
- **Check (RED):** `go test -run 'TestHttpNode_429ParksWithResumeAtAndAutoWakes' ./lib/services/executors/http-node/... 2>&1 | grep -E 'FAIL'`
  matches and no `no tests to run` (today 429 → Error, so the Park assertion fails).

### Task EXECUTORS-4.2 — GREEN: emit Park on 429

- **Action:** In `server.go` add `parseRetryAfter(string, time.Now) time.Time`
  (delta-seconds and HTTP-date forms) and `sendParked(send, resumeAt, note)`;
  in `executeCore`, before the `!statusOK` branch (#225), handle `429` →
  `sendParked`. Update the `@agent-contract: executeCore` block's "does not"
  line which currently says "does not: retry…" to note the 429→Park behavior.
- **Check (GREEN):** `go test -run 'TestHttpNode_429ParksWithResumeAtAndAutoWakes' ./lib/services/executors/http-node/... 2>&1 | grep -E 'ok'` and `grep -vq 'no tests to run'`.

### Task EXECUTORS-4.3 — build

- **Action:** none.
- **Check:** `go build ./...` exits 0.

**Pass EXECUTORS-4 — working gate (metadata):**
- **kind:** go behavioral (real `executeCore` against `httptest` upstream).
- **cmd:** `go test -run 'TestHttpNode_429ParksWithResumeAtAndAutoWakes' ./lib/services/executors/http-node/...`
- **pass condition:** `ok`, no `no tests to run`.
- **green-from-birth check:** RUN on current tree pre-edit — `no tests to run`
  (test not added) then `FAIL` once 4.1 lands.
- **filter-matches-nothing check:** the `[no tests to run]` string is treated as NOT-satisfied.

---

## Pass EXECUTORS-5 — validator MCP header `${env:…}` resolution at spawn; secret never persists

**Story:** `S-executors-validator-header-secret-refs`
**Originating design:** `2026-06-04-claude-agent-signoff-gate-design`
(Non-goals § "Secrets / env-refs for validator connection headers" — un-deferred
here). **Design-change:** none.

**Grounded today:** header maps are copied verbatim with no resolution:
`code:.../src/cli-runner.ts#319-323` emits `headers: t.headers ?? {}` directly into
the `--mcp-config`; the inbound parse `parseStringRecord` at
`code:.../src/server.ts#849-858` copies string values unchanged.

**Fix shape:** at SPAWN time (when assembling `CliToolConfig` headers for the
`--mcp-config`, in `cli-runner.ts::mcpConfigJson` or in `agent-run.ts` spawn
assembly), resolve `${env:VAR}` tokens in header VALUES against the executor
process environment. Resolution happens only on the spawn boundary — the parsed
`cli.mcp_servers` headers (the form persisted/trace-logged via attributes) keep
the `${env:…}` reference form; the resolved token exists only in the transient
`--mcp-config` file the CLI reads.

### Task EXECUTORS-5.1 — RED: validator reached with resolved bearer; persisted attrs show only ${env:}

- **Action:** Add `it("resolves ${env:VAR} in validator mcp_servers headers at spawn so a 401-gated validator is reached, while persisted attributes keep only the reference form", …)`
  to the e2e suite. Set `VALIDATOR_TOKEN` in the executor process env. Configure a
  node `cli.mcp_servers` with header `Authorization: "Bearer ${env:VALIDATOR_TOKEN}"`.
  Stand up a REAL local HTTP MCP validator that returns 401 unless it receives the
  exact resolved bearer token. Drive the real `/execute`; assert the dispatch
  reaches terminal success (validator was reached → header resolved on the wire).
  Separately assert the parsed `cli.mcp_servers` shape (the attributes-derived form
  the supervisor persists/traces) contains the literal `${env:VALIDATOR_TOKEN}`
  reference and NOT the plaintext token.
- **Check (RED):** vitest `-t 'resolves \${env:VAR} in validator mcp_servers headers'`
  shows `…failed` today (header copied verbatim → validator returns 401 →
  dispatch fails). `grep -vq 'no tests'`.

### Task EXECUTORS-5.2 — GREEN: resolve at spawn, keep reference in persisted form

- **Action:** Add `resolveEnvRefs(value: string): string` (new
  `lib/services/executors/claude-agent/src/env-refs.ts`) replacing `${env:VAR}` with
  `process.env.VAR ?? ""`. Apply it in `mcpConfigJson` (and the resume path's
  config builder) to header values ONLY when serializing the `--mcp-config`; do
  NOT mutate the parsed `mcpServers` headers stored on `cliConfig`. The cleanest single
  place to resolve is the `hostServers` map-build site in `agent-run.ts` (`#793`) —
  grounded 2026-06-07: that map is built ONCE and spread into the one `cliRunner.spawn`
  and the one `cliRunner.resume` path, so resolving there (not at "three spawn sites,"
  which do not exist) covers both. Keep `cliConfig.mcpServers` carrying the unresolved
  `${env:}` reference for the persisted/traced form.
- **Check (GREEN):** vitest `-t 'resolves \${env:VAR} in validator mcp_servers headers'`
  shows a positive `Tests …[1-9][0-9]* passed` count (vitest never prints literal
  `no tests`; rely on the positive-count guard).

### Task EXECUTORS-5.3 — build

- **Action:** none.
- **Check:** `(cd lib/services/executors/claude-agent && npm run build)` exits 0.

**Pass EXECUTORS-5 — working gate (metadata):**
- **kind:** vitest behavioral e2e (real bridge + real 401-gating validator).
- **cmd:** `(cd lib/services/executors/claude-agent && npx vitest run -t 'resolves \${env:VAR} in validator mcp_servers headers')`
- **pass condition:** named test `…passed`, no `no tests`.
- **green-from-birth check:** RUN on current tree pre-edit — `no tests`; after 5.1, `…failed`.
- **filter-matches-nothing check:** as above.

---

## Pass EXECUTORS-6 — tested reference sign-off validator in the examples/ module

**Story:** `S-examples-reference-signoff-validator`
**Originating design:** `2026-06-04-claude-agent-signoff-gate-design`
(§ Non-goals notes a reference validator is future work; un-deferred here as the
copy-and-modify reference). **Design-change:** none.

**Grounded today:** only the dist-excluded test-only signer exists at
`code:.../src/signoff-test-signer.ts#5-26` (excluded via `tsconfig.json` `exclude`);
the Apache `examples/` module ships NO sign-off validator. The signing contract:
the validator signs `SIGNOFF_DOMAIN ‖ "\n" ‖ dispatch_id ‖ "\n" ‖ canonical_json(value)`
with Ed25519 — `SIGNOFF_DOMAIN = "rimsky/claude-agent/signoff/v1"`
(`code:.../src/signoff.ts#20,33-36`), canonicalization JCS/RFC-8785.

**Fix shape:** the executor is TypeScript and the gate's bytes-contract is
TS-defined; the README frames `examples/` as "one per protocol a consumer
implements" (Go gRPC servers). A sign-off validator is a validator-MCP server an
agent consults, NOT one of the six core gRPC protocols. To keep the proof in the
REPO GATE (not a dist-excluded fixture) the reference validator ships as an
Apache-licensed, copy-and-modify validator under the claude-agent package's
non-dist source tree referenced by the examples README — OR (preferred, to honor
"in the examples/ module") a self-contained TS reference validator file plus a
behavioral test that the executor's REAL `verifyRequiredSignoffs` accepts its
signature. Concretely: add
`lib/services/executors/claude-agent/examples/signoff-validator/reference-validator.ts`
(Apache header) exporting `signSignoff(privateKeyPem, dispatchId, value): string`
and a documented public-key emitter, and a behavioral test
`reference-validator.test.ts` proving the executor accepts it. Update
`examples/README.md` to point host operators at this reference and contrast it
with the dist-excluded `signoff-test-signer.ts`. (Plan-writer note / FLAG: the
exact home — `lib/services/executors/claude-agent/examples/` vs the Go `examples/`
module — is the one judgment call here; the binding constraint is "Apache,
copy-and-modify, correctness proven by a repo-gate test, not a dist-excluded
fixture.")

### Task EXECUTORS-6.1 — RED: executor's real verifier accepts the reference validator's signature

- **Action:** Add `reference-validator.ts` (Apache header) and
  `reference-validator.test.ts` with
  `it("the reference sign-off validator produces an Ed25519 signature the executor's verifyRequiredSignoffs accepts", …)`:
  the test generates a keypair, has the reference validator sign
  `domain ‖ dispatch_id ‖ canonical(value)` for a sample value+path, and calls the
  executor's REAL `verifyRequiredSignoffs([{publicKey, path}], { <path>: value }, dispatchId, [sig])`,
  asserting `ok === true`; AND a negative sub-case where a signature over a
  DIFFERENT value yields `ok === false`. (The reference validator is the artifact
  under test; `verifyRequiredSignoffs` is the real value-delivering verifier.)
- **Check (RED):** vitest `-t 'the reference sign-off validator produces an Ed25519 signature'`
  shows `…failed` / `no tests` today (file does not exist). After adding the test
  but before the validator is correct, `…failed`.

### Task EXECUTORS-6.2 — GREEN: reference validator signs per the contract

- **Action:** Implement `signSignoff` using `node:crypto` Ed25519 + the same
  `canonicalize` dependency and `SIGNOFF_DOMAIN`/message layout as `signoff.ts`
  (cite it with `@source: src/signoff.ts::buildSignoffMessage` to track the
  duplication). Implement the public-key (PEM SPKI) emitter for `cli.required_signoffs`.
- **Check (GREEN):** vitest `-t 'the reference sign-off validator produces an Ed25519 signature'`
  shows `…passed`, no `no tests`.

### Task EXECUTORS-6.3 — README points at the reference validator

- **Action:** Update `examples/README.md` to reference the copy-and-modify
  sign-off validator and contrast it with the dist-excluded `signoff-test-signer.ts`.
- **Check:** `grep -q 'sign-off validator' examples/README.md`.

### Task EXECUTORS-6.4 — build

- **Action:** none.
- **Check:** `(cd lib/services/executors/claude-agent && npm run build)` exits 0
  (confirm the new files do not leak into dist if placed in a `tsconfig`-excluded
  examples tree).

**Pass EXECUTORS-6 — working gate (metadata):**
- **kind:** vitest behavioral (reference validator signature accepted by the real executor verifier).
- **cmd:** `(cd lib/services/executors/claude-agent && npx vitest run -t 'the reference sign-off validator produces an Ed25519 signature')`
- **pass condition:** named test `…passed`, no `no tests`.
- **green-from-birth check:** RUN on current tree pre-edit — `no tests`; after 6.1, `…failed`.
- **filter-matches-nothing check:** as above.

---

## Pass EXECUTORS-7 — examples/validation + examples/data-processing (one-per-protocol completeness)

**Story:** `S-examples-validation-and-data-processing` (REVIEW-SURFACED — no prior design).
**Design-change:** none.

**Grounded today:** `examples/` ships four protocol examples — `executor/`,
`claimproducer/`, `lifecyclesubscriber/`, `publisher/` — each a minimal gRPC
server + behavioral `*_test.go`. Missing: `validation/` and `data-processing/`,
the two remaining consumer-implementable protocols (`service Validation` in
`code:lib/protocols/proto/v1/validation.proto`; `service DataProcessing` in
`code:lib/protocols/proto/v1/data_processing.proto`). The module is in `go.work`
and Apache-licensed; existing example shape = embed `Unimplemented*Server`,
implement the RPCs minimally, in-process gRPC behavioral test (reference:
`code:examples/executor/executor.go` + `code:examples/executor/executor_test.go`).

### Task EXECUTORS-7.1 — RED: behavioral test for examples/validation

- **Action:** Create `examples/validation/validation.go` (Apache header, embeds
  `genv1.UnimplementedValidationServer`, implements `Validate` routing on the
  `ValidateRequest.context` oneof — minimal: return `valid=true` for a well-formed
  executor context, and `valid=false` with one `ValidationFinding{class,message,path}`
  for a deliberately-bad context) and `examples/validation/main.go` (the listen +
  register shape mirroring `examples/executor/main.go`). Add
  `examples/validation/validation_test.go` with
  `TestValidate_AcceptsWellFormedAndRejectsBadContext` starting the server
  in-process over gRPC (mirrors `executor_test.go` listener+dial), asserting the
  accept case returns `valid=true` and the reject case returns `valid=false` with
  the finding.
- **Check (RED):** `go test -run 'TestValidate_AcceptsWellFormedAndRejectsBadContext' ./examples/... 2>&1`
  → compile failure / `no test files` today (package does not exist). Once the
  test file exists but the impl is stubbed, `FAIL`. (Treat a build/compile error
  as RED.)

### Task EXECUTORS-7.2 — GREEN: examples/validation implemented

- **Action:** Complete the minimal `Validate` implementation so the behavioral
  test passes.
- **Check (GREEN):** `go test -run 'TestValidate_AcceptsWellFormedAndRejectsBadContext' ./examples/... 2>&1 | grep -E 'ok'` and `grep -vq 'no test files'`.

### Task EXECUTORS-7.3 — RED: behavioral test for examples/data-processing

- **Action:** Create `examples/data-processing/dataprocessing.go` (Apache header,
  embeds `genv1.UnimplementedDataProcessingServer`, implements `Capabilities`
  advertising a sample `data_shapes`/`materializations`/`partition_kinds`/`aggregators`,
  and the candidate lifecycle `BeginCandidate` → `CommitCandidate` against an
  in-memory candidate map) + `examples/data-processing/main.go`. Add
  `examples/data-processing/dataprocessing_test.go` with
  `TestBeginThenCommitCandidate_RoundTrips` starting the server in-process over
  gRPC, calling `BeginCandidate` (asserting a non-empty `candidate_handle`) then
  `CommitCandidate` on that handle (asserting it succeeds and returns metadata),
  plus a `Capabilities` non-empty assertion.
- **Check (RED):** `go test -run 'TestBeginThenCommitCandidate_RoundTrips' ./examples/... 2>&1`
  → compile failure / `no test files` today. Treat compile error as RED.

### Task EXECUTORS-7.4 — GREEN: examples/data-processing implemented

- **Action:** Complete the candidate-lifecycle implementation so the test passes.
- **Check (GREEN):** `go test -run 'TestBeginThenCommitCandidate_RoundTrips' ./examples/... 2>&1 | grep -E 'ok'` and `grep -vq 'no test files'`.

### Task EXECUTORS-7.5 — README protocol table lists all six

- **Action:** Add `validation/` and `data-processing/` rows to the
  `examples/README.md` protocol table (the "one per rimsky protocol a consumer
  implements" table) so it lists all six consumer-implementable protocols.
- **Check:** `grep -q 'validation/' examples/README.md && grep -q 'data-processing/' examples/README.md`.

### Task EXECUTORS-7.6 — module gate (build + lint)

- **Action:** none.
- **Check:** `go build ./examples/...` exits 0; `(cd examples && golangci-lint run)` exits 0.

**Pass EXECUTORS-7 — working gate (metadata):**
- **kind:** go behavioral (two in-process gRPC servers, one per protocol).
- **cmd:** `go test -run 'TestValidate_AcceptsWellFormedAndRejectsBadContext|TestBeginThenCommitCandidate_RoundTrips' ./examples/...`
- **pass condition:** `ok`, no `no test files` / `no tests to run`.
- **green-from-birth check:** RUN on current tree pre-edit — packages do not exist
  → compile error / `no test files`; recorded as RED-equivalent.
- **filter-matches-nothing check:** `no test files` / `no tests to run` treated as NOT-satisfied.

---

## Pass EXECUTORS-8 — verifier-shape-checks honors warning vs error severity at run time

**Story:** `S-executors-verifier-severity-partition`
**Originating design:** `2026-05-28-quality-of-life-features-design`
(severity partition honored at run time). **Design-change:** none.

**Grounded today:** the typed enum `spec.Severity {SeverityError, SeverityWarning}`
exists at `code:lib/foundation/spec/enums.go#11-16` (values `"error"` / `"warning"`)
with ZERO consumers — BUT it lives in `lib/foundation`, which the lib/services
module CANNOT import: the `consumption-side-isolation` depguard block in
`.golangci.yml` (files `**/lib/services/**`) DENIES
`github.com/rimsky-ai/rimsky-core/lib/foundation`, and the lib/services `go.mod`
requires only `lib/protocols`, so the import would fail both lint AND the module
graph. `code:.../verifier-shape-checks/checks/checks.go::CheckSpec` (#55-58) carries
only `Kind`+`Config` (no `Severity`); `Result.Pass` is a plain bool. The server
aggregator (`code:.../verifier-shape-checks/server.go#79-118`) treats EVERY failed
check as blocking — `failed > 0` → Error terminal.

**Fix shape:** do NOT import `lib/foundation/spec`. Define a SERVICES-LOCAL severity
type in the verifier's own `checks` package — a tiny string enum
`type Severity string` with `const (SeverityError Severity = "error"; SeverityWarning Severity = "warning")`
(mirroring the foundation values on the wire so operator `checks[].severity:
"warning"|"error"` strings parse identically, but with no cross-module import). Add
a `Severity Severity` field to `CheckSpec` (parsed from `checks[].severity`, default
`error`); in `server.go`, partition failures by severity — a failed
`warning`-severity check is recorded in the findings/observability as non-blocking
and does NOT contribute to the blocking-failure count; only a failed
`error`-severity check drives the `verifier/check_failed/<kind>` Error terminal. The
dispatch resolves to Success when all failures are warning-severity. (If the wire
genuinely needs a shared type across services, the home is `lib/protocols`, which
lib/services may import — but the in-package enum is sufficient here and is the
minimal change.)

### Task EXECUTORS-8.1 — RED: warning-fail still succeeds; error-fail blocks

- **Action:** Add `TestVerifier_WarningSeverityFailIsNonBlocking_ErrorSeverityFailBlocks`
  to `lib/services/executors/verifier-shape-checks/server_test.go`. Drive the REAL
  `executeCore`/dispatch path (the shape existing server tests use) over a real
  rows payload:
  - Dispatch A: checks array = one `severity:warning` check that FAILS + one
    `severity:error` check that PASSES. Assert terminal is `StreamClose_Success`
    AND the success/observability payload reports the warning failure as a
    non-blocking warning (assert the warning is surfaced in the delta/summary).
  - Dispatch B: an `severity:error` check that FAILS. Assert terminal is
    `StreamClose_Error` with `error_class` = `verifier/check_failed/<kind>`.
- **Check (RED):** `go test -run 'TestVerifier_WarningSeverityFailIsNonBlocking_ErrorSeverityFailBlocks' ./lib/services/executors/verifier-shape-checks/... 2>&1 | grep -E 'FAIL'`
  matches, no `no tests to run` (today a failing warning check blocks → Dispatch A is Error).

### Task EXECUTORS-8.2 — GREEN: consume Severity in aggregation

- **Action:** Define the services-local `type Severity string` + `SeverityError`/
  `SeverityWarning` consts in the verifier's `checks` package (`checks/checks.go`) —
  do NOT import `lib/foundation/spec`. Add a `Severity Severity` field to `CheckSpec`
  and parse `checks[].severity` in `parseChecks` (`server.go#123-142`, default
  `checks.SeverityError`); change the aggregation loop (#79-118) to count blocking
  failures only for `error`-severity checks, collect warning-severity failures into a
  `warnings` list surfaced in the Success delta / `summarize`, and emit the Error
  terminal only when a blocking (error-severity) failure exists.
- **Check (GREEN):** `go test -run 'TestVerifier_WarningSeverityFailIsNonBlocking_ErrorSeverityFailBlocks' ./lib/services/executors/verifier-shape-checks/... 2>&1 | grep -E 'ok'` and `grep -vq 'no tests to run'`; AND `(cd lib/services && golangci-lint run ./executors/verifier-shape-checks/...)` passes (no `consumption-side-isolation` violation — no `lib/foundation` import).

### Task EXECUTORS-8.3 — build

- **Action:** none.
- **Check:** `go build ./...` exits 0 AND `cd lib/services && golangci-lint run` passes (confirms the severity type stayed services-local — no forbidden `lib/foundation` import under `consumption-side-isolation`).

**Pass EXECUTORS-8 — working gate (metadata):**
- **kind:** go behavioral (real verifier dispatch over real check runs).
- **cmd:** `go test -run 'TestVerifier_WarningSeverityFailIsNonBlocking_ErrorSeverityFailBlocks' ./lib/services/executors/verifier-shape-checks/...`
- **pass condition:** `ok`, no `no tests to run`.
- **green-from-birth check:** RUN on current tree pre-edit — `no tests to run`; after 8.1, `FAIL`.
- **filter-matches-nothing check:** `[no tests to run]` treated as NOT-satisfied.

---

## Pass EXECUTORS-9 — http-node configurable upstream error-class field + `_unspecified` fallback leaf

**Story:** `S-executors-http-node-error-class-field`
**Originating design:** `2026-05-23-signal-taxonomy-and-policy-decoupling-design`
(§ http-node `http/request_invalid/<body_class>` — "configurable field name;
default `error_class`. Falls back to `http/request_invalid/_unspecified` if the
field is absent"). **Design-change:** none.

**Grounded today:** `code:.../http-node/server.go#390` reads
`decoded["error_class"]` with a HARDCODED field name; `#395` returns
`http/expectation_mismatch` when the field is absent (NOT an `/_unspecified`
leaf). `Config` (`code:.../http-node/config.go#14-20`) has no error-class-field
setting.

**Fix shape:** add a config field `ErrorClassField string` (default `error_class`)
read from `env:RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD` AND/OR from a node
attribute key (`attributes.error_class_field`) — choose the node-attribute form so
template authors can set it per-node (subtract it in `configAttributeKeys`). Pass
the field name into `classifyUnexpectedStatus`; read `decoded[field]`; when a 4xx
body parses but the configured field is absent/empty, return
`http/request_invalid/_unspecified` (a stable subscribable leaf), NOT
`http/expectation_mismatch`.

### Task EXECUTORS-9.1 — RED: configured field read; absent field → _unspecified leaf

- **Action:** Add `TestHttpNode_ConfigurableErrorClassFieldAndUnspecifiedFallback`
  to `server_test.go`. Two `httptest` cases driving REAL `executeCore`:
  - Configure error-class field = `code`; upstream returns 4xx body
    `{"code":"quota_exhausted"}`; assert terminal Error `error_class ==
    "http/request_invalid/quota_exhausted"`.
  - Upstream returns a 4xx body with NO error-class field; assert terminal Error
    `error_class` ends with `/_unspecified` (i.e.
    `http/request_invalid/_unspecified`), NOT `http/expectation_mismatch`.
- **Check (RED):** `go test -run 'TestHttpNode_ConfigurableErrorClassFieldAndUnspecifiedFallback' ./lib/services/executors/http-node/... 2>&1 | grep -E 'FAIL'`
  matches, no `no tests to run` (today: field hardcoded; absent → `expectation_mismatch`).

### Task EXECUTORS-9.2 — GREEN: thread configurable field + _unspecified

- **Action:** Add `ErrorClassField` to `Config` (`config.go`, default `error_class`,
  env-readable) and the `error_class_field` node-attribute path (add to
  `configAttributeKeys`); change `classifyUnexpectedStatus` to take the field name,
  read `decoded[field]`, and return `http/request_invalid/_unspecified` for a
  parseable 4xx body with the field absent. Update the doc comment at #376-382 and
  the `errorclasses` package's declared list if it enumerates leaves.
- **Check (GREEN):** `go test -run 'TestHttpNode_ConfigurableErrorClassFieldAndUnspecifiedFallback' ./lib/services/executors/http-node/... 2>&1 | grep -E 'ok'` and `grep -vq 'no tests to run'`.

### Task EXECUTORS-9.3 — build

- **Action:** none.
- **Check:** `go build ./...` exits 0.

**Pass EXECUTORS-9 — working gate (metadata):**
- **kind:** go behavioral (real `executeCore` against `httptest` 4xx upstreams).
- **cmd:** `go test -run 'TestHttpNode_ConfigurableErrorClassFieldAndUnspecifiedFallback' ./lib/services/executors/http-node/...`
- **pass condition:** `ok`, no `no tests to run`.
- **green-from-birth check:** RUN on current tree pre-edit — `no tests to run`; after 9.1, `FAIL`.
- **filter-matches-nothing check:** `[no tests to run]` treated as NOT-satisfied.

---

## Pass SENSLIFEOBS-1 — sensor-cron durable state DB (RED)

`status: working`
`gate: ! go test ./lib/services/sensors/sensor-cron/ -run 'TestSensorCronStateDSN' -count=1`
`gate-meta: docker-required (testcontainers Postgres via test/harness.StartFreshPostgres); package main; expected RED today — neither the test nor state_db.go exists.`

### Task SENSLIFEOBS-1.1 — author the RED durability test driving the real Publisher service
- **Action:** Create `file:lib/services/sensors/sensor-cron/state_db_test.go`
  (`package main`) with `TestSensorCronStateDSN_SurvivesRestartAndFiresOnScheduledWindow`.
  The test, modeled on `code:lib/services/sensors/sensor-http/state_db_test.go`:
  1. `dsn := harness.StartFreshPostgres(ctx, t)` (testcontainers) and
     `t.Setenv("RIMSKY_SENSOR_CRON_STATE_DSN", dsn)`.
  2. Stand up an `httptest.Server` recording POSTed envelopes (mirror
     `code:lib/services/sensors/sensor-cron/multi_replica_test.go#47-57`).
  3. Construct a first `SensorService` (`NewSensorService(srv.URL, …)` with
     `AttachStateDB(openStateDB(ctx))`), pin `s.clock` to a fixed `registerTime`,
     `Subscribe` a cron sub `{"cron":"*/5 * * * *"}` whose computed
     `next_fire_at` is in the FUTURE relative to `registerTime`.
  4. Drop the first service (simulated process death — do NOT call Unsubscribe).
  5. Construct a SECOND `SensorService` against the SAME DSN with a fresh
     `openStateDB`; `AttachStateDB` must rebuild watches from `ListAll`.
     Assert the recovered watermark is the SAME persisted `next_fire_at`
     (proving it was recovered, not recomputed from a new `sched.Next(now)`) by
     reading it through `state.ListAll(ctx)` (the persisted `SubscriptionState.NextFireAt`)
     OR the internal `Watch.NextFireAt` on the rebuilt watch map (the test is
     `package main`, so it may read the internal `Watch` field directly). Do NOT
     assert via `ListSubscriptions` — grounded 2026-06-07, the gRPC
     `PublisherSubscriptionDescriptor` (publisher.proto) carries no `next_fire_at`
     field (only `started_at`/`target_node`/`message_kind`/…), so the watermark is
     not observable on that surface and no proto-gen task in this cluster adds it.
     The firing-on-the-original-window proof in step 6 is the load-bearing observable.
  6. Advance the second service's clock just past the persisted `next_fire_at`,
     call `Tick(ctx)`, and assert exactly one envelope POSTed with
     `sender_kind=="publisher"` on the originally-scheduled window —
     WITHOUT any re-Subscribe.
  7. Second sub-case: with `RIMSKY_SENSOR_CRON_STATE_DSN` UNSET, a restart
     loses the subscription (`ListSubscriptions` empty after reconstruct),
     confirming the in-memory default path is unchanged.
- **Check:** `! go test ./lib/services/sensors/sensor-cron/ -run 'TestSensorCronStateDSN' -count=1`
  fails to COMPILE/run today (no `openStateDB`, no `AttachStateDB`, no
  `RIMSKY_SENSOR_CRON_STATE_DSN` wiring). Red.

## Pass SENSLIFEOBS-2 — sensor-cron durable state DB (GREEN)

`status: working`
`gate: go test ./lib/services/sensors/sensor-cron/ -run 'TestSensorCronStateDSN' -count=1`
`gate-meta: docker-required; same test as Pass 1, now PASSING.`

### Task SENSLIFEOBS-2.1 — add the state-DB module
- **Action:** Create `file:lib/services/sensors/sensor-cron/state_db.go`
  (`package main`), modeled on the sensor-http peer but with a cron-specific
  schema: `CREATE TABLE IF NOT EXISTS sensor_cron_state (publisher_subscription_id TEXT PRIMARY KEY, instance_id TEXT NOT NULL, cron_expr TEXT NOT NULL, target_node TEXT NOT NULL, message_kind TEXT NOT NULL, next_fire_at TIMESTAMPTZ NOT NULL, started_at TIMESTAMPTZ NOT NULL DEFAULT now(), last_fire_at TIMESTAMPTZ, missed_fires BOOLEAN NOT NULL DEFAULT FALSE)`.
  Implement `openStateDB(ctx)` (reads `RIMSKY_SENSOR_CRON_STATE_DSN`, `(nil,nil)`
  on empty), `bootstrap`, `Close`, `UpsertSubscription(ctx, *Watch)`,
  `DeleteSubscription(ctx, id)`, `UpdateNextFire(ctx, id, nextFireAt, lastFireAt)`,
  and `ListAll(ctx) ([]SubscriptionState, error)` where `SubscriptionState`
  carries the columns needed to rebuild a `Watch` (incl. `NextFireAt`,
  `CronExpr`, `MissedFires`). pgx stdlib driver import (allow-listed).
- **Check:** `go build ./lib/services/sensors/sensor-cron/` succeeds.

### Task SENSLIFEOBS-2.2 — wire the state DB into the service + rebuild on startup
- **Action:** In `file:lib/services/sensors/sensor-cron/sensor.go`:
  add a `state *stateDB` field on `SensorService` and an `AttachStateDB(state *stateDB)`
  that, when non-nil, also rebuilds `s.watches` from `state.ListAll` (each
  recovered row → a `Watch` with its persisted `NextFireAt`/`CronExpr`/etc.).
  In `Subscribe`, after publishing the watch, `state.UpsertSubscription`
  (skip on the idempotent already-active path, mirroring the peer). In
  `Unsubscribe`, `state.DeleteSubscription`. In `fireOne`, after advancing
  `cur.NextFireAt`, `state.UpdateNextFire(id, cur.NextFireAt, cur.LastFireAt)`
  so the durable watermark advances with each fire. Rewrite the package doc
  comment (`#16-34`) to state that persistence is now DSN-gated (empty → the
  in-memory default), removing the "deliberate divergence" prose.
- **Check:** `go build ./lib/services/sensors/sensor-cron/` succeeds.

### Task SENSLIFEOBS-2.3 — read the DSN in main and attach
- **Action:** In `file:lib/services/sensors/sensor-cron/main.go`, mirror
  `code:lib/services/sensors/sensor-http/main.go#49-59`: call `openStateDB(ctx)`,
  `os.Exit(1)` on error, and `svc.AttachStateDB(state)` + `defer state.Close()`
  when non-nil (log "sensor-cron state db attached").
- **Check:** `go build ./lib/services/sensors/sensor-cron/` succeeds and
  `go test ./lib/services/sensors/sensor-cron/ -run 'TestSensorCronStateDSN' -count=1`
  PASSES (gate flips green).

### Task SENSLIFEOBS-2.4 — full verification
- **Action:** Run the broad Go checks plus the sensor-cron package race-suite
  (Tick/fire is mutex-guarded; the new state writes share the lock).
- **Check:** `go build ./... && go test ./lib/services/sensors/sensor-cron/... -race -count=1 && make lint` all pass.

---

## Pass SENSLIFEOBS-3 — sensor-cron replica-posture accuracy (RED)

`status: working`
`gate: ! go test ./lib/services/sensors/sensor-cron/ -run 'TestSensorCronReplicaPostureAccuracy' -count=1`
`gate-meta: no-docker (httptest + source-scan only); package main; expected RED — the no-advisory-lock source-accuracy assertion + the real-binary dual-replica envelope-count check at this name do not exist today.`

### Task SENSLIFEOBS-3.1 — author the RED accuracy test
- **Action:** Create `file:lib/services/sensors/sensor-cron/replica_posture_test.go`
  (`package main`) with `TestSensorCronReplicaPostureAccuracy` asserting the
  three acceptance facets against the real Publisher service:
  1. **Single replica fires once per window:** one `SensorService`, a due
     cron sub, one `Tick` → exactly ONE envelope POSTed with
     `sender_kind=="publisher"` (assert at the message-POST altitude, not a
     counter only).
  2. **Two replicas fan-out N×:** two independent `SensorService` instances
     sharing one `publisher_subscription_id`, each `Tick`ed over the same
     window → exactly TWO envelopes (honest double-fire per `concept:replica`).
  3. **No coordination primitive in source:** walk every non-`_test.go` file
     under `lib/services/sensors/sensor-cron/` and assert NONE contains
     `pg_advisory`, `AdvisoryLock`, `GET_LOCK`, or `LeaderElect` (string
     scan via `os.ReadFile`/`filepath.WalkDir`), so the documented
     single-replica posture is provably the implemented behavior. Use
     `runtime.Caller`/a relative dir constant to locate the package source.
- **Check:** `! go test ./lib/services/sensors/sensor-cron/ -run 'TestSensorCronReplicaPostureAccuracy' -count=1`
  fails today (test absent → no-such-test is not a failure; so the test must
  be ADDED in this RED task and assert at least one currently-false property).
  Concretely: facet 3's source-scan must be written so it would FAIL if an
  advisory-lock primitive were present — and to make the RED real, the task
  ALSO adds (temporarily, in the same commit as the test) NOTHING; instead
  the RED is established by the test referencing a not-yet-existing helper
  `assertNoCoordinationPrimitive(t)` placed in a GREEN-pass file. See note.

> Gate-honesty note: facets 1 and 2 are already true of the running code
> (they duplicate `multi_replica_test.go` at message-POST altitude), so the
> RED MUST come from a genuinely-absent assertion. The two genuine red levers
> are: (a) facet 3's `assertNoCoordinationPrimitive(t)` helper, which does not
> exist yet — referencing it from this test is a COMPILE failure until Pass 4.1
> lands it; and (b) a package-doc accuracy assertion on `sensor.go` — the test
> asserts the `sensor.go` package doc comment no longer contains the stale
> "in-memory only — a deliberate divergence" sentence, which today it STILL
> contains (until Pass 2.2 rewrites it). DROP the `multi_replica_test.go`
> "advisory-lock implementation lands" docstring-accuracy facet: grounded
> 2026-06-07, that string was NEVER in `multi_replica_test.go` (its docstring
> describes the single-replica posture per `concept:replica` — no "advisory-lock
> implementation lands" wording exists to scrub), so it is not a valid red lever.
> Order Pass 3 AFTER Pass 2 so the only remaining red is the new
> `assertNoCoordinationPrimitive` compile-failure + the `sensor.go` package-doc
> accuracy assertion, established by this task and closed by Pass 4.

## Pass SENSLIFEOBS-4 — sensor-cron replica-posture accuracy (GREEN)

`status: working`
`gate: go test ./lib/services/sensors/sensor-cron/ -run 'TestSensorCronReplicaPostureAccuracy' -count=1`
`gate-meta: no-docker; same test as Pass 3, now PASSING.`

### Task SENSLIFEOBS-4.1 — land the source-scan helper + scrub stale prose
- **Action:** Add the `assertNoCoordinationPrimitive(t)` helper (the
  `filepath.WalkDir` + token scan) so facet 3 compiles and passes. (Do NOT add a
  "scrub `multi_replica_test.go` of 'advisory-lock implementation lands' wording"
  step — grounded 2026-06-07, that string is not present in `multi_replica_test.go`;
  its docstring already states the single-replica `concept:replica` contract, so
  there is nothing to scrub.) The `sensor.go` package-doc rewrite (removing the
  "in-memory only — a deliberate divergence" prose) already landed in Pass 2.2;
  this task only ensures the `sensor.go` package-doc accuracy assertion + the
  `assertNoCoordinationPrimitive` source scan are satisfied.
- **Check:** `go test ./lib/services/sensors/sensor-cron/ -run 'TestSensorCronReplicaPostureAccuracy' -count=1`
  PASSES (gate flips green).

### Task SENSLIFEOBS-4.2 — full verification
- **Action:** Run the broad checks plus the full sensor-cron suite.
- **Check:** `go build ./... && go test ./lib/services/sensors/sensor-cron/... -count=1 && make lint` pass.

---

## Pass SENSLIFEOBS-5 — full-stack force-terminate of an await-async-stuck instance (RED)

`status: working`
`gate: ! go test ./test/scenarios/ -run 'TestForceTerminateAwaitAsyncStuckFullStack' -count=1`
`gate-meta: docker-required (scenario harness boots real scheduler+supervisor on testcontainers Postgres); expected RED — test does not exist.`

### Task SENSLIFEOBS-5.1 — author the RED full-stack terminate test
- **Action:** Create `file:test/scenarios/lifecycle_force_terminate_fullstack_test.go`
  (`package scenarios`) with `TestForceTerminateAwaitAsyncStuckFullStack`,
  modeled on `code:test/scenarios/agentic_executor_async_handoff_test.go`:
  1. `h := scenario.Start(t, scenario.HarnessOpts{})` (real scheduler +
     supervisor).
  2. `h.Stub.WhenType("agent").AwaitAsyncCallback("ack-stuck", 60000)` and
     deploy a single-`agent`-node template; `CreateInstance`.
  3. `require.True(t, h.WaitForNodeState(n.ID, cascade.NodeStateRunning, 15s))`
     — the node reaches `running` through the REAL dispatch path and stays
     there (we NEVER POST the callback to `h.Supervisor.CallbackAddr()`).
  4. `http.Post(h.ControlBase+"/instances/"+iid+"/terminate", "application/json", nil)`
     → assert 200 (anonymous-mode gates pass, per existing scenarios e.g.
     `code:test/scenarios/cascade_invalidate_test.go#115`).
  5. Assert via the real persistence projection: the node's most-recent
     run-row transitions to `state=failed` with settling signal
     `terminal/error/instance_killed` (read the projected `NodeRow.State` +
     a `QueryRowSQL` over `rimsky_node_runs` for the failed run's
     `settling_signal_type`/reason); `terminated_at IS NOT NULL` on the
     instance; the main run-scope is closed
     (`rimsky_run_scopes.closed_at IS NOT NULL` for `MainRunScopeID`).
  6. A subsequent `http.NewRequest(DELETE, h.ControlBase+"/instances/"+iid, …)`
     returns 200 `{"deleted":true}` (the terminal guard now passes).
- **Check:** `! go test ./test/scenarios/ -run 'TestForceTerminateAwaitAsyncStuckFullStack' -count=1`
  — RED (no such test). After this task the test exists and FAILS only if a
  regression exists; since the production code is complete, run the gate and
  confirm: if it passes immediately it is GREEN-from-birth proof of the
  existing behavior — acceptable here because the spec's gap is PROOF, not
  code (see gate-honesty note below). Promote directly to the GREEN pass.

> Gate-honesty note: the spec story is a PROOF gap — `handleTerminateInstance`
> already works; the missing thing is a full-stack test that exercises the
> real await-async-stuck path instead of a hand-INSERTed running row. A
> proof-gap RED is legitimately satisfied by a test that is red-because-absent
> and green-once-written against correct code, PROVIDED the test genuinely
> boots the real stack and drives a real `running` row (it does). To make the
> red→green flip observable and guard against a silently-passing test, the
> implementer MUST run the new test against the current tree and report the
> result; if it is green-from-birth, additionally run it with a one-line
> local mutation (e.g. comment out the `MarkTerminated` call in
> `handleTerminateInstance`) to confirm the assertions fail, then revert. The
> revert leaves the gate green.

## Pass SENSLIFEOBS-6 — full-stack force-terminate (GREEN / confirm)

`status: working`
`gate: go test ./test/scenarios/ -run 'TestForceTerminateAwaitAsyncStuckFullStack' -count=1`
`gate-meta: docker-required; same test as Pass 5, asserted PASSING against the real stack.`

### Task SENSLIFEOBS-6.1 — confirm green + delete the superseded fake-altitude proof
- **Action:** Confirm the full-stack test passes. Then, per the spec's
  "FULL-STACK scenario tests, NOT handler-altitude unit tests with fakes"
  directive and the pre-v1 "delete superseded code" rule, remove the
  hand-INSERT-based `seedRunningNodeRun` proof in
  `code:lib/control/controlapi/instances_test.go` that the spec flags as the
  inadequate proof (`#38-61`) IF its only purpose was the await-async-stuck
  path now covered full-stack; keep any terminate assertions that cover
  distinct surfaces (e.g. idempotent re-terminate, dry-run) at handler
  altitude, since those are not the gap. (If after review the handler test
  covers orthogonal cases, narrow rather than delete — but the
  raw-SQL-seeded-running-row case is superseded.)
- **Check:** `go test ./test/scenarios/ -run 'TestForceTerminateAwaitAsyncStuckFullStack' -count=1 && go test ./lib/control/controlapi/... -count=1` pass.

### Task SENSLIFEOBS-6.2 — race + broad verification
- **Action:** Run the race suite on the terminate path's packages plus the
  broad build/test/lint.
- **Check:** `go build ./... && go test ./lib/runtime/... ./test/scenarios/ -run 'TestForceTerminateAwaitAsyncStuckFullStack' -race -count=1 && make lint` pass.

---

## Pass SENSLIFEOBS-7 — full-stack backfill partition-selector override (RED)

`status: working`
`gate: ! go test ./test/scenarios/ -run 'TestBackfillPartitionOverrideFullStack' -count=1`
`gate-meta: docker-required (real stack + remote stub claim-producer over gRPC on testcontainers Postgres); expected RED — test does not exist.`

### Task SENSLIFEOBS-7.1 — author the RED full-stack backfill-override test
- **Action:** Create `file:test/scenarios/backfill_partition_override_fullstack_test.go`
  (`package scenarios`), modeled on `code:test/scenarios/fanout_success_cascade_e2e_test.go`:
  1. Start the remote stub store fixture
     (`stubfixture.Start` with `SupportsSplitScope`, decoding
     `{"partition_keys":[...]}` into one SubScopeDescriptor per key) and
     wire it as a `config.RemoteStoresConfig` store.
  2. `h := scenario.Start(t, scenario.HarnessOpts{Stores: …})`.
  3. Deploy a fan-out template whose `FanOut.PartitionRequest` reads the
     trigger override with a SINGLE-key default, canonical form
     `{{trigger.message.payload.partition_request_override | {"partition_keys":["default-only"]}}}`
     (the authoring form documented at
     `code:lib/runtime/runner_acquire_helpers.go#88-99`); `Stub.WhenType("fan-parent").Success(...)`.
  4. `CreateInstance`; let the initial run settle (default → exactly ONE
     partition RunScope materializes — sanity baseline via `QueryRowSQL`
     count over `rimsky_run_scopes WHERE instance_id=$1 AND partition_key<>''`).
  5. `http.Post(h.ControlBase+"/instances/"+iid+"/backfills", …)` with body
     `{"target_node":"fan-parent","partition_request_override":{"partition_keys":["region-x","region-y"]},"reason":"scenario backfill"}`
     → assert 201/200.
  6. Assert through the REAL dispatch that the supervisor materializes runs
     against the OVERRIDDEN selector: exactly TWO new partition RunScopes
     keyed `region-x`/`region-y` appear (count rises to the override's 2,
     NOT the template default's 1), and two `fan-parent` child dispatches
     for those keys are `Observed()` via the stub, driving the child runs to
     `state=fresh` (pattern from the fanout cascade scenario's
     `QueryRowSQL`/`require.Eventually` blocks).
- **Check:** `! go test ./test/scenarios/ -run 'TestBackfillPartitionOverrideFullStack' -count=1`
  — RED (no such test).

> Gate-honesty note: the override-binding code already works
> (`runner_acquire_helpers.go` substitutes the override before SplitScope),
> so this is a PROOF-gap red identical in character to Pass 5. The implementer
> MUST run the new test against the current tree; if green-from-birth,
> additionally confirm the assertions fail under a one-line local mutation
> (e.g. force `substituteFanOutPartitionRequest` to return the literal
> template default), then revert. The end-state gate is green.

## Pass SENSLIFEOBS-8 — full-stack backfill override (GREEN / confirm)

`status: working`
`gate: go test ./test/scenarios/ -run 'TestBackfillPartitionOverrideFullStack' -count=1`
`gate-meta: docker-required; same test as Pass 7, asserted PASSING against the real stack + real scheduler/supervisor materialization.`

### Task SENSLIFEOBS-8.1 — confirm green + retire the fake-altitude payload proof
- **Action:** Confirm the full-stack test passes. The fake-based
  `code:test/scenarios/backfill/partition_selector_override_test.go`
  (`TestPartitionSelectorOverride_RoundTripsThroughPayload`) only proves the
  override round-trips through the message payload via `fakeMessages`; per
  the spec's "full-stack, not fakes" directive, mark it superseded —
  delete `TestPartitionSelectorOverride_RoundTripsThroughPayload` (the
  end-to-end materialization is now the real proof). Keep
  `TestPartitionSelectorOverride_ValidatesInput` (pure input-validation of
  `CreateBackfill`, orthogonal and cheap).
- **Check:** `go test ./test/scenarios/ -run 'TestBackfillPartitionOverrideFullStack' -count=1 && go test ./test/scenarios/backfill/... -count=1` pass.

### Task SENSLIFEOBS-8.2 — race + broad verification
- **Action:** Race the scheduler/runtime materialization path plus broad checks.
- **Check:** `go build ./... && go test ./lib/graph/scheduler/... ./lib/runtime/... -race -count=1 && make lint` pass.

---

## Pass SENSLIFEOBS-9 — node latest-attribute bag on the node-read surfaces (RED)

`status: working`
`gate: ! go test ./test/scenarios/ -run 'TestNodeLatestAttributeBagFullStack' -count=1`
`gate-meta: docker-required (real control-api + supervisor + executor on testcontainers Postgres); expected RED — neither surface returns the bag and the test does not exist.`

### Task SENSLIFEOBS-9.1 — author the RED latest-attribute test
- **Action:** Create `file:test/scenarios/observability_latest_attribute_fullstack_test.go`
  (`package scenarios`) with `TestNodeLatestAttributeBagFullStack`:
  1. `h := scenario.Start(...)`; deploy a single-`worker` node whose stub
     `Success` returns an `attributes_delta` the supervisor commits into
     `rimsky_node_attributes` (pattern: `code:test/scenarios/happy_path_executor_test.go#81`).
  2. Drive the node to `fresh` (so a per-run attribute bag is persisted) —
     and to satisfy the "most-recent of two runs" clause, invalidate and
     let it re-run with a DIFFERENT delta value, so the latest bag differs
     from the first.
  3. `GET h.ControlBase+"/nodes/"+nodeID` → assert the JSON response now
     carries a `latest_attributes` object equal to the SECOND run's
     resolved bag (the value `GetLatestByNode(nodeID, MainRunScopeID)` returns).
  4. `GET h.ControlBase+"/v1/observability/nodes/"+iid+"/worker"` → assert
     the response's node object (or a sibling `latest_attributes` key) carries
     the same most-recent bag (today it returns only `{node,events,holdings}`).
  5. Assert a node that has never executed returns an
     absent/empty `latest_attributes` (no panic on nil row).
- **Check:** `! go test ./test/scenarios/ -run 'TestNodeLatestAttributeBagFullStack' -count=1`
  — RED (neither surface emits the bag; assertions fail / key absent).

## Pass SENSLIFEOBS-10 — node latest-attribute bag (GREEN)

`status: working`
`gate: go test ./test/scenarios/ -run 'TestNodeLatestAttributeBagFullStack' -count=1`
`gate-meta: docker-required; same test as Pass 9, now PASSING via the real GetLatestByNode primitive end to end.`

### Task SENSLIFEOBS-10.1 — surface the bag on controlapi GET /nodes/{id}
- **Action:** In `code:lib/control/controlapi/nodes.go::handleGetNode#99`,
  after loading the `NodeRow`, resolve `MainRunScopeID` via
  `Instances().Get(row.InstanceID)` and call
  `NodeAttributes().GetLatestByNode(row.ID, mainScopeID, tx)` in the same
  read tx; attach the returned `Data` map as a new `latest_attributes`
  field on `nodeResponse` (`omitempty`, absent when the row is nil). Keep
  the existing fields unchanged.
- **Check:** `go build ./lib/control/controlapi/... && go test ./lib/control/controlapi/... -count=1` pass.

### Task SENSLIFEOBS-10.2 — surface the bag on the observability node read
- **Action:** In `code:lib/control/observability/handler.go::handleGetNode#423`,
  resolve the instance's `MainRunScopeID` (the handler already has the
  `instance_id` URL param → `Instances().Get`) and call
  `GetLatestByNode(match.ID, mainScopeID, tx)`; add a `latest_attributes`
  key alongside `node`/`events`/`holdings` in the response map (nil → omit
  or empty object).
- **Check:** `go build ./lib/control/observability/... && go test ./test/scenarios/ -run 'TestNodeLatestAttributeBagFullStack' -count=1` PASSES (gate flips green).

### Task SENSLIFEOBS-10.3 — full verification
- **Action:** Broad build/test/lint.
- **Check:** `go build ./... && go test ./lib/control/... -count=1 && make lint` pass.

---

## Pass SENSLIFEOBS-11 — breakpoint.hit event-log row on GET /events (RED)

`status: working`
`gate: ! go test ./test/scenarios/breakpoints/ -run 'TestBreakpointHitEmitsEvent' -count=1`
`gate-meta: docker-required (real scheduler+supervisor evaluate the checkpoint on testcontainers Postgres); expected RED — no breakpoint.* event kind exists in source.`

### Task SENSLIFEOBS-11.1 — author the RED breakpoint-event test
- **Action:** Create `file:test/scenarios/breakpoints/hit_emits_event_test.go`
  (`package breakpoints`) with `TestBreakpointHitEmitsEvent`, modeled on
  `code:test/scenarios/breakpoints/notify_only_mode_test.go` (notify_only so
  the dispatch doesn't block):
  1. Deploy a single-`worker` node; `createInstanceWithPause`;
     `breakpointCreate` a `notify_only` `before_dispatch` breakpoint matching
     `node_type: worker`; `instanceResume`.
  2. `waitForHitCount(bpID, 1, …)` → capture the single hit (its `ID`,
     `BreakpointID`, `NodeRunID`, checkpoint, mode).
  3. Poll `GET h.ControlBase+"/events?kind=breakpoint.hit&instance_id="+iid`
     (the route the acceptance names) and assert exactly one event row whose
     payload carries `instance_id`, `node_id`, `breakpoint_id`, `hit_id`,
     `checkpoint=="before_dispatch"`, `mode=="notify_only"`, and whose
     `hit_id` equals the ledger hit's `ID`.
  4. Txn-coupling assertion: also read via `h.Persist.Events().List(... Kind:"breakpoint.hit")`
     and assert the event row count equals the ledger hit count (1) — proving
     the event is written in the same tx that creates the hit (a recorded
     hit is always reflected on `/events`).
- **Check:** `! go test ./test/scenarios/breakpoints/ -run 'TestBreakpointHitEmitsEvent' -count=1`
  — RED (no `breakpoint.hit` rows are ever appended; `GET /events?kind=breakpoint.hit`
  returns empty).

## Pass SENSLIFEOBS-12 — breakpoint.hit event-log row (GREEN)

`status: working`
`gate: go test ./test/scenarios/breakpoints/ -run 'TestBreakpointHitEmitsEvent' -count=1`
`gate-meta: docker-required; same test as Pass 11, now PASSING.`

### Task SENSLIFEOBS-12.1 — append the breakpoint.hit event inside the hit-create tx
- **Action:** In `code:lib/runtime/breakpoint_eval.go#244-258`, inside the
  SAME `args.Persist.Transaction` closure that calls `BreakpointHits().Create`,
  after the hit id is known append an event via `args.Persist.Events().Append(ctx,
  persistence.EventAppendInput{InstanceID: &cc.InstanceID, NodeID: <node-id>,
  Kind: "breakpoint.hit", Payload: {instance_id, node_id, breakpoint_id:
  bp.ID, hit_id: hitID, checkpoint: string(cc.Checkpoint), mode: string(bp.Mode)}}, tx)`.
  Use the dispatch's node id — `CheckpointContext` carries `DispatchID`
  (the `rimsky_node_runs.id`); resolve the owning `node_id` for the event's
  `NodeID` (from `cc` if available, else via the node-run row; prefer adding
  the node id to `CheckpointContext` if not already present so no extra read
  is needed). On Append error, return a `*BreakpointInfraError{Phase:"create_hit"}`
  so the failure routes through the existing debugger-infra path (the whole
  tx rolls back together — hit and event are atomic). Document with an
  `@concept: event-log` reference that the hit and its event-log row are
  co-transactional.
- **Check:** `go build ./lib/runtime/... && go test ./lib/runtime/... -run 'Breakpoint' -count=1` pass.

### Task SENSLIFEOBS-12.2 — confirm the scenario gate flips green
- **Action:** Run the breakpoint scenario gate.
- **Check:** `go test ./test/scenarios/breakpoints/ -run 'TestBreakpointHitEmitsEvent' -count=1` PASSES.

### Task SENSLIFEOBS-12.3 — race + broad verification
- **Action:** The supervisor evaluates breakpoints off the dispatch path;
  race the runtime + scheduler packages, then broad checks.
- **Check:** `go build ./... && go test ./lib/runtime/... ./lib/graph/scheduler/... -race -count=3 && go test ./test/scenarios/breakpoints/... -count=1 && make lint` pass.

---

## Pass DOCS-1: lifecycle-subscriber concept-scope clarification
**Goal:** Apply the spec's `concept:lifecycle-subscriber` design-change (the one design-change with no code story).
**Scope:** Task DOCS-1.1
**End state:** working
**Verification:** grep -q "control-plane / instance lifecycle" .ok-planner/design/concepts/lifecycle-subscriber.md && ! grep -q "wants to apply per-template DDL" .ok-planner/design/concepts/lifecycle-subscriber.md
**Proves:** the concept doc states its control-plane/instance-lifecycle scope (not node-cascade) and the postgres-store DDL over-claim is corrected
**Red-when-absent:** before the edit the grep finds no scope statement (exit 1) — a content assertion, not an infra error
### Task DOCS-1.1: Edit `concept:lifecycle-subscriber`
**Files:** `.ok-planner/design/concepts/lifecycle-subscriber.md`
**Steps:**
1. In Definition/Boundaries, state the protocol relays the control-plane / instance lifecycle (template register/deploy/undeploy/deregister, instance created/terminated, run-scope terminal) and deliberately does NOT carry node-cascade events (e.g. parked), which live in concept:signal / concept:event-log.
2. Correct the over-claim that the bundled postgres store applies per-template DDL on the deployed callback — it ships a no-op skeleton (a documented fork-point), so DDL-on-deploy is the archetype the protocol enables, not a shipped behavior.
3. Append a dated Notes entry citing spec:2026-06-06-comprehensive-gap-closure-design. Keep the body path-free (concept self-containment rule).
**Verification:** (same as the pass)

---

## Acceptance

These are the plan's final acceptance passes — one end-to-end gate per user-outcome story (43 total); the run cannot close until every gate is green.

### Acceptance — CLICTRL

> All full-stack gates boot the real assembled product via the services harness (`lib/services/test/harness`: `BringUpRimsky(WithSQLite(), WithExistingNetwork(net), WithExecutor("stub","executor-stub:9300"))` + `StartExecutorStubOnNetwork`), modeled on `lib/services/test/scenarios/sqlite_all_in_one_test.go`. They REQUIRE locally-built images (`make core-images` for `rimsky-all-in-one:latest`; the stub executor is built in-tree by the harness) and a Docker socket. CLI verbs are driven in-process through the real CLI entrypoints (`cli.RunRun`, `cli.RunWatch`, `compose.RunComposeUp/RunComposeDown`) pointed at `RimskyEndpoint.BaseURL` via `--endpoint` — the real CLI → real control-api → real scheduler/supervisor/stub-executor. Tests live in `lib/services/test/scenarios/` (new files), one per story. Right-reason red = the named CLI verb / handler / wire field is absent or behaves source-grouped/keyless/prefix-permissive, surfacing as a failed assertion on the observable outcome, NOT a testcontainers/Docker error.

#### Gate CLICTRL-G1 — S-cli-onboarding-example-spec
**File:** `lib/services/test/scenarios/cli_example_spec_e2e_test.go` (new) — `TestCLIExampleSpec_RunReachesTerminal`.
**Steps:** Bring up the all-in-one stack + stub executor (the example spec's node uses `executor: stub`, which the harness wires). Point `cli.RunRun(ctx, []string{"--endpoint", ep.BaseURL, "<repo>/examples/compose/template-a.yml"})` at the SHIPPED example YAML (the one authored in CLICTRL-5.6, copied verbatim — a real on-disk file with a `nodes:` block). Capture stdout; assert it prints `instance_id=<uuid>` and `RunRun` returns exit 0. Extract the instance id, then drive `cli.RunInstanceGet`/poll the observability node read until the worker node emits `work_started` and settles to `fresh` (terminal). Second assertion: run the README's documented `rimsky compose up`/`rimsky run` invocation as written against the shipped file and assert exit 0.
**Verification:** `go test ./lib/services/test/scenarios/ -run TestCLIExampleSpec_RunReachesTerminal -count=1`
**Proves:** A never-before-written-by-the-operator shipped TemplateSpec runs end to end via `rimsky run` and reaches terminal on the real assembled stack.
**Red-when-absent:** With no shipped example YAML (today `examples/` has only Go programs), `RunRun` cannot consume a file → the test fails opening the path / asserting `instance_id=`. Right-reason red.
**Run-now result:** Not run (requires Docker + `make core-images`; the example file does not exist yet). Expected red: missing `examples/compose/template-a.yml` → file-open failure / no `instance_id=` line. (Authored by CLICTRL-5.6.)

#### Gate CLICTRL-G2 — S-cli-onboarding-compose-up-down
**File:** `lib/services/test/scenarios/cli_compose_up_down_e2e_test.go` (new) — `TestCLICompose_UpThenDown`.
**Steps:** Bring up the stack (already running — compose never starts it). Write a `rimsky-compose.yml` to a temp dir declaring two templates (single `executor: stub` node each) + one instance per member, project `project-alpha`. Run `compose.RunComposeUp(ctx, []string{"-f", manifestPath, "--endpoint", ep.BaseURL, "--yes"})`; assert exit 0. Assert via `cli.RunLs`/the control-api that each declared template is registered+deployed (`template list`) and one instance per member exists (`ls instances`), each carrying a `compose:project-alpha:`-prefixed tag/instance_key. Assert reconcile touched only compose-tagged resources (create a manual non-compose tag first; confirm `compose down` leaves it). Then `compose.RunComposeDown(ctx, …)` → exit 0; assert the compose instances + templates are gone and the stack is clean. Assert NO docker/infra side effect (the test asserts only HTTP writes happened — no new containers spawned beyond the harness; structurally guaranteed since compose only calls the cli.Client).
**Verification:** `go test ./lib/services/test/scenarios/ -run TestCLICompose_UpThenDown -count=1`
**Proves:** The `compose:` prefix is now PRODUCED by a working verb (up/down) against a live control-api; reconcile is project-scoped; no infra invocation.
**Red-when-absent:** No `compose` subpackage / no `main.go` case today → `compose.RunComposeUp` is undefined (compile failure) until CLICTRL-5 lands. Right-reason red (missing symbol → unbuildable test).
**Run-now result:** Not run (compose subpackage absent; would not compile). Expected red: undefined `compose.RunComposeUp`.

#### Gate CLICTRL-G3 — S-cli-onboarding-watch-chronological
**File:** `lib/services/test/scenarios/cli_watch_chronological_e2e_test.go` (new) — `TestCLIWatch_ChronologicalAcrossSources`.
**Steps:** Bring up the stack + stub executor. Create an instance and set a breakpoint on a node's `before_dispatch` checkpoint (via the real breakpoint route). Drive the instance so the real sequence is event → breakpoint hit → later event within one watch poll window (seed timing by configuring the breakpoint to stop, letting an event precede and follow the hit). Run `cli.RunWatch(ctx, []string{"--endpoint", ep.BaseURL, "--poll-interval","250ms", instanceID})` with stdout captured in a goroutine; resume the breakpoint to let the instance terminate so watch exits. Parse stdout lines; assert the breakpoint.hit line sits between the two event lines by printed order (timestamp-faithful), not after both.
**Verification:** `go test ./lib/services/test/scenarios/ -run TestCLIWatch_ChronologicalAcrossSources -count=1`
**Proves:** The advertised chronological feed is delivered across the real event log + breakpoint-hits route + terminal, driven by the real watch loop.
**Red-when-absent:** Current `RunWatch` is source-grouped (events, then hits) → the hit prints after both events → the between-events assertion fails. Right-reason red.
**Run-now result:** Not run (requires Docker/all-in-one). Expected red: source-grouped output orders the hit after both events.

#### Gate CLICTRL-G4 — S-cli-onboarding-rules-deploy-paths
**File:** `tools/rulesdoc/rulesdoc_test.go` (the accuracy check authored in CLICTRL-1.1) — `TestRulesDoc_CitedPathsExist`. (Doc-drift story → automated accuracy check is the acceptance, per spec; no full-stack boot.)
**Verification:** `go test ./tools/rulesdoc/... -run TestRulesDoc_CitedPathsExist -count=1`
**Proves:** Every filesystem path `rules.md` instructs a contributor to run resolves on disk; the dead `deploy/` refs are gone and `make core-images` is named.
**Red-when-absent:** Before CLICTRL-1.2, `deploy/build-images.sh` + `deploy/docker-compose.yml` are cited but absent → the test `t.Errorf`s listing them.
**Run-now result:** RAN (the test does not exist yet, so a literal run matches nothing). To avoid the green-from-birth trap the gate is asserted RED in CLICTRL-1.1 via `! go test -run TestRulesDoc_CitedPathsExist` AFTER the test is authored — at that point it fails on the two missing `deploy/` paths. Confirmed: `deploy/` directory does not exist on disk (`ls deploy` → no such file), so the authored test will be red-for-the-right-reason until CLICTRL-1.2.

#### Gate CLICTRL-G5 — S-control-api-mcp-idempotency-key-required
**File:** `lib/services/test/scenarios/control_api_idempotency_required_e2e_test.go` (new) — `TestControlAPIIdempotencyRequired_E2E`.
**Steps:** Bring up the all-in-one stack (control-api real). Register+deploy a template and create a live instance via the harness. POST `/instances/{id}/messages` with a valid invalidate body and NO `Idempotency-Key` over real HTTP (`ep` raw POST) → assert 400 + header-required diagnostic; assert `GET /instances/{id}/messages` shows zero messages (no envelope, no idempotency row). POST again WITH a key → 201; replay same key → 200 + identical `message_id` + still one envelope.
**Verification:** `go test ./lib/services/test/scenarios/ -run TestControlAPIIdempotencyRequired_E2E -count=1`
**Proves:** Replay-dedup is mandatory at the real route; a missing key can never bypass it.
**Red-when-absent:** The real handler returns 201 for a keyless POST today → the 400 assertion fails. Right-reason red.
**Run-now result:** Not run (Docker/all-in-one). Expected red: keyless POST → 201, fails the 400 assertion.

#### Gate CLICTRL-G6 — S-control-api-mcp-compose-prefix-server-guard
**File:** `lib/services/test/scenarios/control_api_compose_prefix_guard_e2e_test.go` (new) — `TestControlAPIComposePrefixGuard_E2E`.
**Steps:** Bring up the stack. Raw HTTP `POST /tags` `{"tag":"compose:project-alpha:v1","template":<hash>}` with NO compose-origin header (no CLI) → assert 400 + reserved-prefix diagnostic; `GET /tags` omits it. Raw `POST /instances` with `instance_key` `compose:project-alpha:i1` → 400; the instance is not created. Then drive the SAME writes through the real `compose.RunComposeUp` machinery (which sets the compose-origin marker) and assert they succeed (compose-prefixed tag + instance created).
**Verification:** `go test ./lib/services/test/scenarios/ -run TestControlAPIComposePrefixGuard_E2E -count=1`
**Proves:** The server guards the reserved prefix against foreign clients regardless of which client called, while compose-originated writes still succeed.
**Red-when-absent:** Today `validTag` accepts `compose:` and the instance-key path has no prefix check → the raw POSTs return 2xx, failing the 400 assertions. Right-reason red.
**Run-now result:** Not run (Docker/all-in-one). Expected red: raw `POST /tags compose:…` → 201.

#### Gate CLICTRL-G7 — S-control-api-mcp-node-detail-resolution-flavor
**File:** `lib/services/test/scenarios/control_api_node_signal_type_e2e_test.go` (new) — `TestControlAPINodeSettlingSignalType_E2E`.
**Steps:** Bring up the stack + stub executor. Create an instance and drive its node to a real settle (stub returns Success → the node settles with a known resolution and persists `settling_signal_type`). `GET /nodes/{nodeID}` over real HTTP → assert the body's `settling_signal_type` equals the canonical signal type the run-tree/lineage surface reports for that node (cross-check against `GET` of the lineage/backfill drill-down or the observability node read). For an unsettled node, assert the field is absent/empty.
**Verification:** `go test ./lib/services/test/scenarios/ -run TestControlAPINodeSettlingSignalType_E2E -count=1`
**Proves:** Node detail carries the settling signal type read from the real persisted `NodeRow`, matching the run-tree's value.
**Red-when-absent:** `nodeResponse` has no `settling_signal_type` today → the field is absent and the equality assertion fails. Right-reason red.
**Run-now result:** Not run (Docker/all-in-one). Expected red: response JSON has no `settling_signal_type` key.

#### Gate CLICTRL-G8 — S-control-api-mcp-idempotency-status-matrix
**File:** `lib/control/controlapi/idempotency_matrix_test.go` — `TestIdempotencyMatrix` (the per-status matrix authored in CLICTRL-3.1; it drives the REAL controlapi handler through the real httptest server + real Postgres, the value-delivering component). The spec's acceptance for this story is exactly "running `go test ./lib/control/controlapi/...` exercises every status path green; flipping any one status turns exactly its test red" — a handler-level matrix against the real handler IS the stated acceptance surface (no separate full-stack boot demanded for this story).
**Verification:** `go test ./lib/control/controlapi/ -run TestIdempotencyMatrix -count=1`
**Proves:** Every idempotency/publisher-capability status (201/200/400/403/no-collision) is pinned by a named sub-test against the real handler.
**Red-when-absent:** The matrix's `missing_key_400` sub-case fails against the pre-CLICTRL-2 handler (returns 201); after CLICTRL-2 the full matrix is green and any single status flip reddens exactly one sub-case.
**Run-now result:** RAN — `go test ./lib/control/controlapi/ -run TestIdempotencyMatrix` currently exits 0 with "no tests to run" (test not yet authored — green-from-birth trap). Mitigation: CLICTRL-3.1 authors the test and the gate is proven red via `! go test -run TestIdempotencyMatrix` against a tree where CLICTRL-2.2's header guard is reverted (the `missing_key_400` sub-case then fails on a 201). The orchestrator must observe the flip around CLICTRL-2.2 + CLICTRL-3.1, not the bare current-tree run.

### Acceptance — AUTHSTORES

> One end-to-end gate per story. Each boots the REAL assembled product with the value-delivering component real; each is proof-first (its named test already exists from the GREEN pass) and is RUN against the current tree at execution time — report exit/result and confirm right-reason red when the production change is reverted (catch green-from-birth + filter-matches-nothing).

#### GATE S-auth-identity-bound-dryrun
**Run:** `go test ./test/scenarios/auth/ -run TestDryRun_IdentityBoundFloor -count=1`
**Real component:** real `controlapi.NewApp` (auth middleware + handlers + audit) over httptest +
SQLite. **Observable outcome:** a `mode:dry_run`-granted key's flag-less `POST /instances` returns
the `would_have_created` envelope, persists no row, audits `executed:false`; an ordinary key commits.
**Red-when-absent:** revert AUTHSTORES-4 (mode floor) → the floored write commits a real instance;
test red. **Docker:** not required.

#### GATE S-auth-grant-scope-enforced
**Run:** `go test ./test/scenarios/auth/ -run TestGrantScope_TemplateTagEnforced -count=1`
**Real component:** real control-api over httptest + SQLite. **Observable outcome:** in-scope
`template:register` (tag `analytics`) → 201 + persisted; out-of-scope (tag `billing`) → 403 +
`auth.access_denied` audit row, no `billing` template created. **Red-when-absent:** revert
AUTHSTORES-6 (target extraction) → out-of-scope register returns 201; test red. **Docker:** not required.

#### GATE S-claimproducer-9b-probe
**Run:** `go test ./lib/services/test/scenarios/conformance_9b/ -run TestClaimProducer9b_Probe -count=1`
**Real component:** the real `lib/protocols/conformance/claimproducer.Run` driven over gRPC against
two real producers. **Observable outcome:** honest producer reports `Serialization9b: ok`; dishonest
(reader-lease) producer reports `Serialization9b: FAIL` naming invariant 9b. **Red-when-absent:**
revert AUTHSTORES-8 (the check) → no 9b row; the dishonest producer passes clean; test red.
**Docker:** not required (in-test gRPC servers, no testcontainers).

#### GATE S-claimproducer-scopesconflict-wired
**Run:** `go test ./lib/services/test/scenarios/scopes_conflict/ -run TestScopesConflict_OverlapHeldOff -count=1`
**Real component:** real rimsky stack (control-api + scheduler + supervisor) via testcontainers + a
real overlap-advertising producer over gRPC. **Observable outcome:** of two prefix-overlapping
non-byte-equal writers only one acquires; the conflicting fan-out sub-claim is rejected (no double
commit). **Red-when-absent:** revert AUTHSTORES-10 (byte-equal-only) → both writers acquire; test
red. **Docker:** REQUIRED (testcontainers + `make core-images` + `make service-images`).

#### GATE S-fsstore-explicit-sync-route
**Run:** `go test ./lib/services/stores/filesystem/store/ -run TestAdminSync_ExplicitReadmits -count=1`
**Real component:** real fs `Store` + its `AdminHandler` over httptest; gRPC `Open` over the producer
interface. **Observable outcome:** drained explicit queue → Open Unavailable; after `POST
/admin/sync/{selector}` a dropped folder becomes Available on the next Open. **Red-when-absent:**
remove the route (AUTHSTORES-12) → 404, Open stays Unavailable; test red. **Docker:** not required.

#### GATE S-fsstore-atomic-staging-reference
**Run:** `(cd examples && go test ./atomic-staging-fs-producer/... -count=1)`
**Real component:** the real recovered fs atomic-staging producer (real POSIX `os.Rename` swap).
**Observable outcome:** Commit atomically renames staging into the canonical path (staging gone);
Abandon leaves the canonical path unchanged (staging discarded). **Red-when-absent:** break the
rename into copy-without-remove → contract test red; deleting the dir → build red. **Docker:** not
required. (The full-stack held-subgraph integration of the SAME pattern is already covered by the
existing `fs_held_swap_e2e_test.go` Gate-10 scenario; this story's gate is the copyable example +
its behavioral test, which the spec story names as the deliverable.)

#### GATE S-pgstore-atomic-staging-substrate
**Run:** `go test ./lib/services/stores/postgres/store/ -run TestAtomicStaging_SchemaSwap -count=1`
**Real component:** real pg store against real Postgres (testcontainers). **Observable outcome:**
Open reserves a staging schema; Commit atomically swaps staged rows into the canonical schema
(staging gone); Abandon discards staging (canonical unchanged); a forced collision yields
`pg/swap_failed`. **Red-when-absent:** revert AUTHSTORES-16 (swap) → Commit no-ops, no staging schema;
test red. **Docker:** REQUIRED (testcontainers Postgres).

#### GATE S-pgstore-row-count-ratio-check
**Run:** `go test ./lib/services/stores/postgres/server/ -run TestExecutor_RowCountRatio -count=1`
**Real component:** real pg store verifier `executeCore` + real shared sql-checks against real
Postgres. **Observable outcome:** in-bounds ratio → Success terminal; out-of-bounds →
`pg/verifier_check_failed/row_count_ratio` Error with the computed ratio in the payload, via one
aggregate-only query. **Red-when-absent:** remove the compiler arm (AUTHSTORES-18) → unknown-kind →
in-bounds dispatch errors; test red. **Docker:** REQUIRED (testcontainers Postgres).

#### GATE S-pgstore-claim-unavailable-swap-failed-emit
**Run:** `go test ./lib/services/test/scenarios/pg_error_classes/ -run TestPGErrorClasses_Delivered -count=1`
**Real component:** real pg store + real rimsky stack + a real subscriber (testcontainers).
**Observable outcome:** an empty pick-policy items table delivers `pg/claim_unavailable` to a
subscriber/`error_types` chain; a forced staging swap collision delivers `pg/swap_failed` — both
observed at a real surface (event-log / subscriber callback). **Red-when-absent:** revert
AUTHSTORES-20 (emit/routing) → both classes go silent; test red. **Docker:** REQUIRED (testcontainers
+ `make core-images` + `make service-images`).

### Acceptance — TEMPLCASCADE

> Each gate boots the REAL assembled product (real control-api + scheduler + supervisor on testcontainers Postgres via `scenario.Start`), proof-first, value-delivering component real. One per story. RUN each against the current tree (pre-implementation) and report below.

#### AG-TEMPLCASCADE-1 — `S-template-validation-claim-scope-end-to-end`
Gate test: `TestAcceptance_ClaimScopeEndToEnd` in `test/scenarios/stores/claim_scope_directive_e2e_test.go`.
Drive: real stack + a real claim-producer over loopback gRPC (`stores/stub/testfixture.Start`, `WriteSemanticsSync`) returning a known ClaimScope for selector `/scope-A`; a stub executor. (1) `POST /templates` (via `h.DeployTemplate`) for a node acquiring claim alias `a` with `region: "{{claim.a.claim_scope}}"` returns success (not 400). (2) create + dispatch an instance; assert `h.Stub.Observed()` for the worker shows `Attributes["region"]` equals the stringified live ClaimScope bytes the producer returned. (3) a sibling deploy of the same template using `{{claim.a.scope}}` is rejected at registration (HTTP 400 with a validation error). Observable surfaces: registration HTTP response + the executor's received attribute on a real dispatch.
gate-cmd: `go test ./test/scenarios/stores/... -run TestAcceptance_ClaimScopeEndToEnd -count=1`
Run-against-current-tree result: **RED** — registration of the `claim_scope` spelling returns HTTP 400 today (validator at `template_validator.go#1369` admits only `scope`), so step 1 fails before dispatch. Not green-from-birth; filter matches the named test. (Docker required.)

#### AG-TEMPLCASCADE-2 — `S-template-validation-source-kinds-docstring-accuracy`
Doc-drift story → automated accuracy check IS the acceptance gate (no full-stack boot — the "real product" surface is the resolver source + its dispatch arms; an in-process parse-vs-resolver test is the value-delivering check per the spec's doc-drift rule).
Gate test: `TestSubstitutionDocstringMatchesResolver` (Pass TEMPLCASCADE-2.1).
gate-cmd: `go test ./lib/graph/attribute/... -run TestSubstitutionDocstringMatchesResolver -count=1`
Run-against-current-tree result: **RED** — header `substitution.go#7` says "Five recognized source kinds:" over six bullets omitting trigger/child; the count + membership assertions fail. Not green-from-birth.

#### AG-TEMPLCASCADE-3 — `S-template-validation-ref-validation-mode`
Gate test: `TestAcceptance_RefValidationMode` in `test/scenarios/attributes/ref_validation_mode_e2e_test.go`.
Drive: boot three real stacks (or one stack reconfigured across sub-cases) with the operator mode set to `all`, `available`, `none` respectively (via the new `AppDeps.RefValidationMode` plumbed through `scenario.HarnessOpts`). For each, `POST /templates` for a template referencing a not-yet-provisioned executor: mode `all` → HTTP 400 missing-reference; mode `available` → 200/201 for that ref while a genuinely-invalid PROVISIONED ref still 400s; mode `none` → 200/201. The "genuinely-invalid provisioned ref" sub-case needs a provisioned executor whose advertised schema actually constrains the attribute (otherwise the permissive `{"type":"object"}` stub never makes any ref "invalid"): stand up a constraint-advertising executor via the TEMPLCASCADE-3.0 knob — e.g. `ExtraExecutors{"constrained": scenario.StartStubExecutorWithSchema(t, <schema declaring a violated property>)}` — and reference THAT executor for the provisioned-but-invalid leg. Observable surface: the differing registration HTTP responses against the real control-api.
gate-cmd: `go test ./test/scenarios/attributes/... -run TestAcceptance_RefValidationMode -count=1`
Run-against-current-tree result: **RED/COMPILE-FAIL** — `HarnessOpts` has no `RefValidationMode` field, `AppDeps` no mode, and no stub-schema knob exists; the test does not compile against HEAD (the mode + knob do not exist). Once they are added the `available`/`none` cases stay red until the legs honor the mode. Not green-from-birth; requires Pass 3 (incl. task 3.0). (Docker required.)

#### AG-TEMPLCASCADE-4 — `S-template-validation-instantiation-mandatory`
Gate test: `TestAcceptance_InstantiationStaticConfigGate` in `test/scenarios/attributes/instantiation_static_config_gate_e2e_test.go`.
Drive: boot a real stack where the referenced executor exists + has handshaked and advertises a CONSTRAINING schema — stand it up via the TEMPLCASCADE-3.0 knob as an `ExtraExecutors` entry whose Capabilities schema declares a property with `minimum: 0` (e.g. `ExtraExecutors{"constrained": scenario.StartStubExecutorWithSchema(t, <schema with a minimum:0 property>)}`); the default permissive `{"type":"object"}` stub cannot make any static config "invalid". Register (ref mode `none`) + deploy a template whose node references that constrained executor and whose node default sets that property to `-1`. `POST /instances` → assert HTTP 400 with a validation error naming the attribute and citing the `minimum:0` violation; assert via `GET /instances` the instance is NOT persisted. A well-formed instance of the same template returns 201 and runs to a terminal state. Observable surface: instance-create HTTP response + persisted instance absence/presence.
gate-cmd: `go test ./test/scenarios/attributes/... -run TestAcceptance_InstantiationStaticConfigGate -count=1`
Run-against-current-tree result: **RED/COMPILE-FAIL** — no stub-schema knob exists on HEAD (the test cannot stand up a constraint-advertising executor), and `handleCreateInstance` runs no node-attribute schema validation; once the knob (task 3.0) lands, the misconfigured `POST /instances` still returns 201 (the violation only surfaces later as a dispatch `dispatch_bag_violates_executor_schema`), so the 400 assertion fails until Pass 4. Not green-from-birth. (Docker required.)

#### AG-TEMPLCASCADE-5 — `S-template-validation-lenient-marker-recovery-e2e`
Gate test: `TestLenientMarkerRecoveryE2E` (Pass TEMPLCASCADE-5.1 — this scenario IS the acceptance gate).
gate-cmd: `go test ./test/scenarios/attributes/... -run TestLenientMarkerRecoveryE2E -count=1`
Run-against-current-tree result: **RED/COMPILE-FAIL** — the test file does not exist on HEAD; no scenario drives the `?` marker through a real stack. Once authored, it exercises the lenient-recovery path end to end (resolver lenient flag exists, but no e2e proof) plus the strict-failure companion. Not green-from-birth. (Docker required.)

#### AG-TEMPLCASCADE-6 — `S-cascade-operator-frame-in`
Gate test: `TestAcceptance_OperatorFrameInJoinsRunningFrame` in `test/scenarios/cascade_operator_frame_in_e2e_test.go`.
Drive: boot a real stack; create an instance whose cascade frame is currently open (a source node settled, a dependent mid-drain in running frame `F`). Run the operator invalidate against the real control-api — `POST /nodes/{target}/invalidate` body `{"frame":"in"}` (the surface the CLI `--frame in` and the MCP `node_invalidate` tool both reach). Assert at the real audit/event surface + node row: the target transitions fresh→stale and acquires the SAME `frame_id == F` (joins the running frame), re-evaluated within that frame's drain — one frame_id observed end to end, not two sequential frames. The dry-run path still echoes `frame: in`, now truthfully.
gate-cmd: `go test ./test/scenarios/... -run TestAcceptance_OperatorFrameInJoinsRunningFrame -count=1`
Run-against-current-tree result: **RED** — the handler omits SourceFrameID (`nodes.go#177-186`), so `invalidateInFrame#238` falls back to next-frame; the target gets a freshly-enqueued next-frame id, not `F`. The "same frame_id" assertion fails. Not green-from-birth. (Docker required.)

#### AG-TEMPLCASCADE-7 — `S-cascade-waitset-topic-taxonomy`
Gate test: `TestAcceptance_WaitSetTopicKindTaxonomy` in `test/scenarios/cascade_waitset_topic_taxonomy_e2e_test.go`.
Drive: boot a real stack; run an instance whose template gates a receiver on a transient-class signal (and separately message-class + terminal-class edges). While the receiver is gated, read the wait-set ledger via the real surface (`InTx` read of `rimsky_wait_set`, or the node/breakpoint snapshot surface): assert `topic_kind` shows the correct class per edge — transient→`transient`, message→`message`, terminal→`terminal`, with no two distinct classes collapsed. Assert the broadened CHECK is in force (the row inserted without rejection) and a gated run completes correctly end to end (drain/dedupe unchanged). The migration + `waitSetTopicKindFor` are the real components.
gate-cmd: `go test ./test/scenarios/... -run TestAcceptance_WaitSetTopicKindTaxonomy -count=1`
Run-against-current-tree result: **RED** — today `waitSetTopicKindFor` maps transient/terminal/message all to `state`, and the CHECK would reject a `transient`/`message`/`terminal` value anyway; the per-class assertions fail (and an attempted broadened-value insert is rejected by the unbroadened CHECK). Not green-from-birth. (Docker required.)

### Acceptance — HOSTAGENT

> One end-to-end gate per story, each booting the real assembled product (real proxy binary + real in-process `rimsky agent` + real exec'd local binary; real conformance runner / real CLI against a real producer). Proof-first: each gate's RED→GREEN flip is established by its pass's RED task. All `test/scenarios/...` and `lib/services/test/scenarios/...` gates require a running Docker socket (testcontainers); the atomic_staging conformance gates also require the bundled images (`make service-images`).
>
> GATE-VERDICT (applies to every bare `go test … -run '<Name>'` gate below): a bare `-run` filter EXITS 0 when the named test is absent (filter-matches-nothing), so these are judged by OUTPUT, not exit code (the EXECUTORS-cluster guarded form): **RED** iff output contains `FAIL`/a real failure AND NOT `no tests to run`; **GREEN** iff output contains `ok` AND NOT `no tests to run`; `no tests to run` is treated as NOT-satisfied (never a green gate). Each gate's `Run-now result:` records the current-tree bare run; right-reason-RED is the run AFTER the pass's RED task authors the named test, pinned to that pass's `! go test …` task.

#### Gate — `S-hostagent-per-run-scope-isolation`
`go test ./test/scenarios/ -run 'TestHostAgentPerRunScopeIsolation' -count=1`
Boots the real proxy + connected agent + a PID-logging spawned binary; one
instance fans out into two concurrent run-scopes that both dispatch the same
late-bound executor node; asserts two DISTINCT child PIDs (one per run-scope),
each receiving only its own run-scope's dispatches; terminating one run-scope
reaps only that run-scope's child. Value-delivering proxy + agent + binary all
real. Run-now result: current-tree bare run exits 0 with `no tests to run` (test
absent) → NOT-satisfied (not a green gate); right-reason-RED is the post-2.1 run
(proxy keys by instance id today).

#### Gate — `S-hostagent-latebind-all-protocols`
`go test ./test/scenarios/ -run 'TestHostAgentLateBindAllProtocols' -count=1`
Boots the real proxy + connected agent + a multi-protocol spawned binary; drives
a validation dispatch (deliberately-rejecting validator → rejection at the
validation surface), a publisher dispatch (real message published), and a
data-processing dispatch (real typed-data op) — alongside executor +
claim-producer — each served by the real spawned binary, forwarded by the same
path; none returns gRPC `Unimplemented`. Run-now result: current-tree bare run
exits 0 with `no tests to run` (test absent) → NOT-satisfied; right-reason-RED is
the post-4.1 run (three protocols are Unimplemented stubs today).

#### Gate — `S-hostagent-per-binding-exec-overrides`
`go test ./test/scenarios/ -run 'TestHostAgentPerBindingExecOverrides' -count=1`
Boots the real proxy + connected agent + a spawned binary that echoes its
argv/env/cwd; binds with per-binding args/env/cwd + a short per-binding timeout;
asserts the child ran with those args/env/cwd and that the binding timeout (not
the global default) bounds the spawn wait; a no-override binding still spawns
(backward compatible). Run-now result: current-tree bare run exits 0 with `no
tests to run` (test absent) → NOT-satisfied; right-reason-RED is the post-6.1 run
(only `path` is carried/applied today).

#### Gate — `S-hostagent-anonymous-mode-latebind`
`go test ./test/scenarios/ -run 'TestHostAgentAnonymousModeLateBind' -count=1`
Boots the real proxy + an agent registered under the anonymous routing identity;
creates an instance via the anonymous path (null owner key) naming a
late_bind_services binding; dispatches and asserts the late-bound child runs and
returns a real dispatch outcome (worker → fresh, terminal/success) rather than
terminating with `host_agent_not_connected`. Run-now result: current-tree bare
run exits 0 with `no tests to run` (test absent) → NOT-satisfied; right-reason-RED
is the post-7.1 run (proxy short-circuits owner-less instances today).

#### Gate — `S-conformance-claimproducer-terminals`
`go test ./lib/services/test/scenarios/atomic_staging/ -run 'TestConformanceClaimProducerTerminalsCLI' -count=1`
Builds the real `rimsky` CLI, stands up the real bundled postgres claim-producer
over gRPC, runs `rimsky conformance claim-producer --endpoint grpc://<addr>`,
and asserts passing `ok Commit/Abandon/Release/TerminalIdempotency` rows + exit
0; then runs the same CLI against a producer whose Commit errors and asserts a
`FAIL Commit` row + non-zero exit. Real conformance runner + real CLI + real
producer. Run-now result: current-tree bare run exits 0 with `no tests to run`
(test absent) → NOT-satisfied; right-reason-RED is the post-10.1 run (runner never
drives the terminal verbs today).

### Acceptance — EXECUTORS

> One end-to-end gate per story, booting the REAL assembled product, value-delivering component real (NO canned-success stub — the sign-off gate exists precisely to forbid that), proof-first. Each gate's cmd was RUN against the current tree; the recorded result is the green-from-birth / filter-matches-nothing guard.

#### AG-EXECUTORS-1 — `S-executors-signoff-binds-real-output`
- **Boots:** real claude-agent HTTP-bridge `/execute` entry point + real Ed25519
  verification + real per-dispatch MCP callback server; fake CLI subprocess only
  (the LLM is not the thing under test; the gate is).
- **Observable:** incremental-writeback dispatch with `attributes_delta` omitted —
  a signature over `"null"` → `AsyncCallbackBody.error.error_class ==
  "agent/signoff_unobtained"`; a signature over the REAL accumulated value →
  `AsyncCallbackBody.success` committing the accumulated `endpoints`.
- **Gate cmd:** `(cd lib/services/executors/claude-agent && npx vitest run -t 'sign-off gate binds the accumulated incremental writeback')`
- **Satisfied iff:** stdout shows `Tests …[1-9]+ passed` AND no `no tests`.
- **Current-tree run result:** `no tests` (test absent) → NOT satisfied → valid gate.

#### AG-EXECUTORS-2 — `S-executors-mcp-catalog-transports`
- **Boots:** real claude-agent with a startup catalog (`allow_inline=false`) + real
  spawn assembly emitting real `--mcp-config`; module/http-loopback transports stood
  up for real; real `/execute` for the e2e success path.
- **Observable:** a `{ref:}` to a stdio catalog server emits a `type:"stdio"`
  `--mcp-config` entry and the dispatch using a tool only that server provides
  reaches terminal success; an inline server is rejected with an `allow_inline`
  config error; module + http-loopback dispatches reach terminal success.
- **Gate cmd:** `(cd lib/services/executors/claude-agent && npx vitest run -t 'catalog ref|dispatches successfully using a module-transport')`
  (a SINGLE `-t` regex alternation — vitest errors out if passed two `-t` flags; the
  alternation selects both named tests in one run; both must pass).
- **Satisfied iff:** both named tests appear and report `…passed` (a positive
  `Tests …[1-9][0-9]* passed` count), and the run is NOT empty (vitest prints
  `0 passed`/`Tests …skipped`, never literal `no tests` — rely on the positive-count
  guard).
- **Current-tree run result:** empty / `0 passed` (tests absent) → NOT satisfied → valid gate.

#### AG-EXECUTORS-3 — `S-executors-claude-agent-error-classes`
- **Boots:** real `/execute` bridge; real subprocess-output classification of four
  fake stderr signatures.
- **Observable:** four dispatches → `AsyncCallbackBody.error.error_class` exactly
  `agent/context_exceeded`, `agent/refused`, `agent/tool_use_failed/<tool>`,
  `agent/rate_limited` (the last with `handle_rate_limits=false`); each is a member
  of advertised `declared_error_classes` (`agent/tool_use_failed/*` wildcard).
- **Gate cmd:** `(cd lib/services/executors/claude-agent && npx vitest run -t 'emits agent/context_exceeded')`
- **Satisfied iff:** named test `…passed`, no `no tests`.
- **Current-tree run result:** `no tests` → NOT satisfied → valid gate.

#### AG-EXECUTORS-4 — `S-executors-http-node-429-park-resume`
- **Boots:** real http-node `executeCore` against a real `httptest` upstream that
  429s (with `Retry-After`) then 200s.
- **Observable:** the 429 dispatch resolves to `StreamClose_Park` with
  `Park.Reason == PARK_REASON_SNOOZE` and a `Park.ResumeAt` computed from
  Retry-After (NOT a `StreamClose_Error`); the re-dispatch at resume reaches
  `StreamClose_Success`.
- **Gate cmd:** `go test -run 'TestHttpNode_429ParksWithResumeAtAndAutoWakes' ./lib/services/executors/http-node/...`
- **Satisfied iff:** `ok`, no `no tests to run`.
- **Current-tree run result:** `[no tests to run]` → NOT satisfied → valid gate.
- **FLAG (acceptance altitude):** the supervisor's real `SweepParkedNodes`
  auto-wake is a runtime-cluster surface, not executor-cluster. This gate proves
  the executor's Park-emit + resume-success contract (the executor half). If the
  reviewer requires the supervisor wake in the SAME gate, it belongs to a
  full-stack scenario test under `test/scenarios/` co-owned with the runtime
  cluster — surfaced here for cross-cluster coordination.

#### AG-EXECUTORS-5 — `S-executors-validator-header-secret-refs`
- **Boots:** real `/execute` bridge + a real local HTTP MCP validator returning
  401 unless it receives the exact resolved bearer token; `VALIDATOR_TOKEN` set in
  the executor env.
- **Observable:** the dispatch reaches terminal success (validator reached → header
  resolved on the wire) AND the persisted/parsed attribute form shows only
  `${env:VALIDATOR_TOKEN}`, never the plaintext token.
- **Gate cmd:** `(cd lib/services/executors/claude-agent && npx vitest run -t 'resolves \${env:VAR} in validator mcp_servers headers')`
- **Satisfied iff:** named test `…passed`, no `no tests`.
- **Current-tree run result:** `no tests` → NOT satisfied → valid gate.

#### AG-EXECUTORS-6 — `S-examples-reference-signoff-validator`
- **Boots:** the copy-and-modify reference validator's `signSignoff` + the REAL
  executor `verifyRequiredSignoffs`.
- **Observable:** a signature the reference validator produced over the documented
  sign-off message is accepted by the executor's real verifier (`ok===true`); a
  signature over a different value is rejected (`ok===false`).
- **Gate cmd:** `(cd lib/services/executors/claude-agent && npx vitest run -t 'the reference sign-off validator produces an Ed25519 signature')`
- **Satisfied iff:** named test `…passed`, no `no tests`.
- **Current-tree run result:** `no tests` → NOT satisfied → valid gate.
- **NOTE (altitude — byte-contract only, not a separate dispatch e2e):** this gate
  asserts the BYTE-CONTRACT only — that a signature the reference validator's
  `signSignoff` produces over the documented sign-off message is accepted by the REAL
  executor `verifyRequiredSignoffs` (`ok===true`), and a signature over a different
  value is rejected (`ok===false`). It deliberately does NOT re-prove the end-to-end
  dispatch flow (`cli.required_signoffs` naming the validator's key → signature flowing
  via `report_complete` → terminal success): that dispatch path is already exercised
  end to end by AG-EXECUTORS-1 (sign-off gate binds the real bound output through the
  real `/execute` bridge) and AG-EXECUTORS-5 (validator reached with the resolved
  bearer → terminal success). AG-6's NEW surface is solely the reference validator's
  signature byte-compatibility with the executor's verifier; the dispatch wiring is
  covered by 1/5 and is not re-tested here.
- **FLAG (placement judgment):** see Pass EXECUTORS-6 — the reference validator's
  home (claude-agent non-dist `examples/` vs the Go `examples/` module). The binding
  constraints (Apache, copy-and-modify, repo-gate-tested correctness, not the
  dist-excluded fixture) are met either way; flagged for reviewer ratification.

#### AG-EXECUTORS-7 — `S-examples-validation-and-data-processing`
- **Boots:** the two new example gRPC servers in-process over real gRPC.
- **Observable:** `Validate` accepts a well-formed context (`valid=true`) and
  rejects a bad one (`valid=false` + finding); `BeginCandidate`→`CommitCandidate`
  round-trips a candidate handle; both run green under the workspace gate.
- **Gate cmd:** `go test -run 'TestValidate_AcceptsWellFormedAndRejectsBadContext|TestBeginThenCommitCandidate_RoundTrips' ./examples/...`
  plus `go build ./examples/...` and `(cd examples && golangci-lint run)`.
- **Satisfied iff:** `ok`, no `no test files` / `no tests to run`; build + lint exit 0.
- **Current-tree run result:** packages absent → `no test files` → NOT satisfied → valid gate.

#### AG-EXECUTORS-8 — `S-executors-verifier-severity-partition`
- **Boots:** real verifier-shape-checks dispatch over real check runs against a
  real rows payload.
- **Observable:** a failing `warning`-severity check → terminal Success with the
  failure surfaced as a non-blocking warning; a failing `error`-severity check →
  terminal Error `verifier/check_failed/<kind>`; the services-local typed
  `checks.Severity` (NOT the forbidden `lib/foundation/spec.Severity`) is the value
  actually consumed by the failure classification.
- **Gate cmd:** `go test -run 'TestVerifier_WarningSeverityFailIsNonBlocking_ErrorSeverityFailBlocks' ./lib/services/executors/verifier-shape-checks/...`
- **Satisfied iff:** `ok`, no `no tests to run`.
- **Current-tree run result:** `[no tests to run]` → NOT satisfied → valid gate.

#### AG-EXECUTORS-9 — `S-executors-http-node-error-class-field`
- **Boots:** real http-node `executeCore` against real `httptest` 4xx upstreams.
- **Observable:** with field `code` configured and body `{"code":"quota_exhausted"}`
  → Error `http/request_invalid/quota_exhausted`; a 4xx body with no error-class
  field → Error ending in `/_unspecified`, NOT `http/expectation_mismatch`.
- **Gate cmd:** `go test -run 'TestHttpNode_ConfigurableErrorClassFieldAndUnspecifiedFallback' ./lib/services/executors/http-node/...`
- **Satisfied iff:** `ok`, no `no tests to run`.
- **Current-tree run result:** `[no tests to run]` → NOT satisfied → valid gate.

### Acceptance — SENSLIFEOBS

> One end-to-end gate per story. Each boots the real assembled product; the value-delivering component is real (no stub stands in for the thing under test). All are proof-first (the named RED test above drove the behavior).

#### AG-S-sensors-cron-state-dsn-durability
`gate: go test ./lib/services/sensors/sensor-cron/ -run 'TestSensorCronStateDSN' -count=1`
`gate-meta: docker-required (real sensor-cron Publisher service + a real Postgres state DB via testcontainers).`
The REAL sensor-cron `SensorService` backed by a REAL Postgres state DB
(`RIMSKY_SENSOR_CRON_STATE_DSN` set) registers a cron sub with a future
`next_fire_at`, the binary is dropped and reconstructed against the same DSN
WITHOUT a re-Subscribe, `ListSubscriptions` shows the sub with its persisted
`next_fire_at`, and it fires exactly one publisher-message envelope on the
originally-scheduled window. With the DSN empty, the in-memory default path
is unchanged (sub gone after restart). Observable at the message-POST surface
and `ListSubscriptions`.

#### AG-S-sensors-cron-replica-posture-accuracy
`gate: go test ./lib/services/sensors/sensor-cron/ -run 'TestSensorCronReplicaPostureAccuracy' -count=1`
`gate-meta: no-docker (real Publisher service via httptest + source-scan).`
The REAL sensor-cron Publisher service: one replica POSTs exactly one
envelope per window; two independently-running replicas sharing a
`publisher_subscription_id` POST exactly two (honest N× fan-out per
`concept:replica`); and a source scan proves no advisory-lock / leader-election
primitive exists in the binary's source, so the documented single-replica
posture is the implemented behavior.

#### AG-S-lifecycle-fullstack-terminate-backfill
`gate: go test ./test/scenarios/ -run 'TestForceTerminateAwaitAsyncStuckFullStack|TestBackfillPartitionOverrideFullStack' -count=1`
`gate-meta: docker-required (real scheduler+supervisor+control-api on testcontainers Postgres; remote stub claim-producer over gRPC for the backfill leg).`
Two full-stack scenarios (NOT handler-altitude fakes): (1) a node driven to
`running` through the REAL dispatch path awaiting an async callback that never
arrives is force-terminated via `POST /instances/{id}/terminate` — the node-run
goes `failed`/`instance_killed`, the main run-scope closes, `terminated_at`
is set, and `DELETE` then succeeds; (2) a backfill with a partition-selector
override fired via `POST /instances/{id}/backfills` is honored end-to-end —
the live scheduler/supervisor materialize child runs against the OVERRIDDEN
selector (two partition RunScopes, not the template default's one), driving
them to completion.

#### AG-S-observability-forensic-last-attribute
`gate: go test ./test/scenarios/ -run 'TestNodeLatestAttributeBagFullStack' -count=1`
`gate-meta: docker-required (real control-api + supervisor + executor on testcontainers Postgres).`
Against the real stack, after a node executes across two runs, both the
control-api node-read (`GET /nodes/{id}`) and the observability node read
(`GET /v1/observability/nodes/{instance_id}/{node_type}`) return the node's
MOST-RECENT resolved attribute bag — the row served by the real
`NodeAttributes().GetLatestByNode` primitive keyed on the node's main run
scope — not the earlier run's bag, and absent for a never-executed node.

#### AG-S-observability-breakpoint-hit-event
`gate: go test ./test/scenarios/breakpoints/ -run 'TestBreakpointHitEmitsEvent' -count=1`
`gate-meta: docker-required (real scheduler+supervisor evaluate the checkpoint on testcontainers Postgres).`
Against the real stack with a `before_dispatch` breakpoint, when the
supervisor records a hit a `breakpoint.hit` event-log row is appended IN THE
SAME TXN as the BreakpointHits ledger row (carrying instance id, node id,
breakpoint id, hit id, checkpoint, mode), and a client polling
`GET /events?kind=breakpoint.hit` observes it — so a recorded hit is always
reflected on `/events`.

---

## Manual checks after completion

The acceptance gates above are almost entirely automated; their only prerequisites are infrastructure, not human judgment:

- **Docker socket + locally-built images.** Every testcontainers-backed gate (the full-stack `test/scenarios/...` and `lib/services/test/scenarios/...` gates, the store gates against real Postgres, and the services-harness CLI gates) needs a running Docker socket. The services-harness and atomic-staging conformance gates additionally pull **locally-built** image tags — run `make core-images` (for `rimsky-all-in-one:latest`) and `make service-images` (for the bundled-service tags) before those gates, or the harness fails when testcontainers cannot find the image locally. The SQLite-backed auth gates, the in-test gRPC conformance gates, the fs-store gate, and the doc-drift / examples / executor-unit gates do NOT require Docker.
- **Proto regeneration is NOT a manual check.** `make proto-gen` is a task step inside the HOSTAGENT passes (HOSTAGENT-1.3, 3.2, 5.2), run by the implementer as part of those passes — not a post-completion manual step.

No genuinely-manual (human-judgment) checks are called for beyond the Docker/image prerequisites above; the fragments name none.
