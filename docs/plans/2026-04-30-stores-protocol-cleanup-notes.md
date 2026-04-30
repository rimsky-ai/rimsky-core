# Stores Protocol Cleanup — Implementation Notes

## Environment

**Deviation:** No local Go toolchain available; every `go build` / `go test` /
`go vet` / `make tidy` / `make lint` step in the plan is executed inside a
transient `golang:1.25` Docker container with the repo bind-mounted at `/src`,
matching the pattern used during the T55-T57 verification earlier today.

**Reason:** `go` is not on `PATH` in this environment. Docker is available
(verified earlier in this session). The Docker container has the same Go
version as `go.mod` declares (`go 1.25.0`).

**Surfaced for:** the user, in case they want to install a local Go toolchain
for follow-up work, or to know that the verifications ran inside containers
and therefore against a clean Go module cache (modulo the `rimsky-gomod`
named volume preserved between calls).

## Task 1 — Proto change

**Deviation:** `make proto-gen` requires `protoc-gen-go` and
`protoc-gen-go-grpc` on `PATH`. Both are installed at `~/go/bin/` but that
directory is not on the user's default `PATH`. Invoked the make target with
`PATH="$HOME/go/bin:$PATH" make proto-gen` instead.

**Reason:** Same shape as the Go-toolchain note above — pre-existing
environment configuration; no action needed by the user.

**Surfaced for:** visibility only.

## Task 20 — Final full-build verification

**Deviation:** `TestAgenticExecutorAsyncHandoff` in
`test/scenarios/agentic_executor_async_handoff_test.go` failed once
during the full-suite run with
`pgtest: connection string: port "5432/tcp" not found` — a
testcontainers-go startup flake when running nested under
Docker-in-Docker. Retried in isolation (just that one test) and it
passed cleanly. Treating as a pre-existing test-infra flake unrelated
to the cleanup surface; this scenario test does not touch any of the
cleanup-affected files.

**Reason:** Nested-Docker environments occasionally race the
"container started" event vs. "port mapping is queryable." The test
itself appears correct.

**Surfaced for:** worth a couple of retries on a fresh run by the
user before declaring it stable. If it reproduces frequently, the
`test/scenarios/setup.go::pgtest` machinery may want a backoff/retry
loop on the port-binding query.

## Build verification — successful end state

- `go build ./...` — clean.
- `go test ./... -count=1 -timeout 600s` — all packages pass except
  the one flake noted above; rerun of that single test passes.
- `go vet ./...` — clean (verified incrementally throughout).
- `make lint` and `make tidy` — initially deferred during the
  cleanup-cycle proper (substituted `go vet ./...` for lint coverage;
  skipped tidy out of cache-preservation overcaution). Both run
  post-cycle inside the Docker wrapper:
  - `golangci-lint v1.64.8 ./...` (built inside the container at
    runtime via `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`,
    since the host's `~/go/bin/golangci-lint` is a Darwin binary that
    won't run in the Linux container) — exit 0, zero findings.
  - `go mod tidy` — clean; `git diff go.mod go.sum` shows no drift.
  - The Linux `golangci-lint` binary is preserved between calls in a
    named volume `rimsky-gobin:/go/bin` so future re-runs don't
    re-install.

**Surfaced for:** plan task 20 step 4 + step 5 are now satisfied via
the Docker-wrapper run described above. The user may still want to
run `make lint` / `make tidy` locally if a host Go toolchain becomes
available, as a cross-check against the Docker-built golangci-lint.

## Post-cycle review fixes — 2026-04-30

A reviewer pass surfaced nine follow-up issues (gofmt drift, dead
code, missing strict-decode + corresponding test, missing bridge
test, doc-comment drift, fake.go lock-discipline comment). All nine
fixed; full-suite re-run is clean.

### Issue 5 — `DisallowUnknownFields` collateral

Adding `decoder.DisallowUnknownFields()` to
`handleDeployTemplate` caught one stale-field bug in
`test/scenarios/frame_resolution/template_missing_frame_resolution_rejected_test.go`:
the test's `baseNodes` carried `"id": "worker"`, which has never been
a valid `templateNodeDefJSON` field — `Type` is the node identifier.
Pre-cleanup the field was silently dropped; post-cleanup it
short-circuits the missing-frame_resolution assertion. Removed the
stale `"id"` per the project's "Fix Every Bug You Find" rule rather
than carrying it forward as a workaround. No production impact (test
fixture only).

### Issue 8 — `resume_then_retry` doc-comments

Picked path (b) from the reviewer's recommendation: doc-comments on
both `PolicyAction` and `ResolvedAction` in `core/node/policy.go` now
describe the actual current behavior — `resume_then_retry` is an
alias for `discard_then_retry` and the runner fires `Abandon` (not
`Release`). Preserving the kind as distinct from `discard_then_retry`
in the action vocabulary so policy authors can express intent in
declarations; if a future cycle reintroduces explicit Release
routing, both the comments and `applyResolvedAction` need to update
together.

### Cycle-2 fixes — 2026-04-30

A re-review pass surfaced two further follow-ups; both fixed in this
cycle:

- **Issue 1 — stale test name.** Renamed
  `TestValidateInheritance_Ok_HeldClaimWithResolutions` →
  `TestValidateInheritance_Ok_HeldClaim` in
  `core/node/template_validator_test.go`. The test body had already
  been correctly updated to drop `claim_resolutions` (gone in the
  cleanup grammar), but the function name still advertised
  "WithResolutions". Naming/documentation drift only — no behavior
  change.
- **Issue 2 — missing test for the ambiguous-acquirers rejection
  path.** Added `TestValidateInheritance_Error_AmbiguousAcquirers`
  alongside the existing unknown-alias and not-reachable-via-deps
  tests. Drives a template where two distinct nodes both acquire
  alias `queue` and a third node depends on both and inherits the
  alias; asserts the validator rejects with an error mentioning
  "acquirers are reachable" (the precise phrase
  `core/node/inheritance.go:122` emits). Closes the coverage gap
  flagged by `HoldingSubgraphsForTemplate`'s comment at
  `inheritance.go:142-146` ("ambiguity already rejected by
  ValidateInheritance").

Verification: `go test ./core/node/... -count=1 -run
TestValidateInheritance` passes for all four test variants
(`Ok_HeldClaim`, `Error_UnknownAlias`,
`Error_AliasNotReachableViaDeps`, `Error_AmbiguousAcquirers`); full
`./core/node/...` package run is also clean.

### Cycle-3 fixes — 2026-04-30

A third reviewer pass surfaced 13 documentation-drift issues across
comments, prose, schema comments, and a smoke-test fixture YAML.
All survived the prior two cycles because the cleanup focused on
runtime code paths; doc surfaces lagged. Every issue fixed in this
cycle:

- **Issue 1 — `CLAUDE.md` verb-count drift.** `core/store/`
  description and the supervisor's verb-list line both said
  "5 verbs" / "Open / Commit / Abandon / Delete / Release"; updated
  to "4 verbs" / "Open / Commit / Abandon / Release" (no `Delete`).
- **Issue 2 — `core/doc.go` verb-count drift.** Two mentions
  ("five verbs … Delete …", "Store interface (5 verbs)") changed
  to "four verbs" / "(4 verbs)" with `Delete` dropped.
- **Issue 3 — `core/supervisor/runner.go` verb-list drift.** Three
  sites in package-comment-shaped doc-comments updated to drop
  `Delete` and change "5-verb" → "4-verb".
- **Issue 4 — `docs/architecture.md` verb count + path drift.** Five
  sites updated: §1.1 reference-impl path moved to `stores/<kind>/`;
  §2 ASCII tree's `core/store/` line restated as "(4 verbs) +
  remote/ + storetest/"; §3.1 `store/` package rule restated as
  4-verb + clarified that reference impls live under `stores/` (no
  in-process subpackages, no `TxFromContext` / `pgx.Tx` sharing);
  §3.2 closing paragraph restated likewise; §5.9a `core/store/postgres/`
  example path updated to `stores/postgres/`.
- **Issue 5 — `docs/operator-guide.md` verb-count drift.** Three
  sites (§3.4 banner, §3.5 claim-content prose, §3.4.X postgres
  store-service admin port) updated to "4-verb".
- **Issue 6 — `docs/protocol.md` §7 action mapping rewrite.** §3.2
  reference updated to "4-verb store interface". §7 fully rewritten:
  table now lists `Commit(claim_id)` / `Abandon(claim_id)` only (no
  `policy_override`, no `Delete`); held-claim aggregate-outcome
  routing restated as success → `Commit` / failure → `Abandon`; new
  paragraph clarifies that substrate disposition is governed by
  per-substrate config, not by template-level action vocabulary.
- **Issue 7 — `docs/store-author-guide.md` banner.** Banner verb
  list dropped `Delete`; `5 + 1` → `4 + 1`.
- **Issue 8 — `docs/glossary.md:71` Auto-terminal description.**
  `all-success → on_commit` / `any-failure → on_give_up` rewritten
  as `all-success → Commit` / `any-failure → Abandon`.
- **Issue 9 — `core/store/types.go:74` spec section reference.**
  `see spec §7.3` corrected to `see v3 spec §13.3` (the section
  that distinguishes claim-content from store-config bytes; §7.3
  is the acquisition-flow section).
- **Issue 10 — supervisor / scenario test comment drift.** Three
  test comments updated to drop `on_commit` / `on_give_up`
  template-side vocabulary in favor of `Commit` / `Abandon`:
  `core/supervisor/auto_terminal_test.go` (two test comments) and
  `test/scenarios/claim_stores/auto_terminal_aggregate_outcome_test.go`
  (package doc + skip reason).
- **Issue 11 — `test/scenarios/locks/placeholder_test.go:9`.** Verb
  list dropped `Delete`.
- **Issue 12 — `core/migrations/001-initial.sql:47` schema
  comment.** `claim_resolutions` removed from the spec-shape comment
  on `rimsky_templates.spec`; replaced with the actual current
  shape (`stores/locks/inherits/attributes/quality_rules/error_types`).
- **Issue 13 — `test/smoke/fixtures/template.yml` pre-redesign
  grammar.** YAML rewritten to mirror what the smoke test actually
  deploys: `nodeStoreRefJSON`-shaped store entries
  (`name`/`selector`/`intent`/`alias?`) replace the old
  `{ claim: true, hold: true }` / `write: [...]` / `read: [...]`
  shapes; `claim_resolutions` block dropped (the cleanup removed it
  entirely); `inherits:` declared on `review` to match the deployed
  Go literal; locks reference names only (limits live in operator
  config); a leading comment paragraph documents the cleanup
  rationale (success/failure binary; substrate disposition lives in
  store-service config).

Verification: `docker run --rm -v "$PWD":/src -w /src -v
rimsky-gomod:/go/pkg/mod golang:1.25 go build ./...` passes
(catches the four code-touching edits in issues 2, 3, 9, 13). Pure
docs / SQL / comment edits do not require a build. The smoke test
loads the YAML only as documentation (the deploy path uses the Go
literal in `smokeTemplateBody()`); no behavioral change to the
smoke test.

## Cycle-4 fixes — 2026-04-30

Reviewer caught one site the Task 18 doc cascade missed entirely:
`docs/node-graph-design.md`. The conceptual reference doc still
carried v2-era vocabulary (`5-verb`, `Delete` as a wire verb,
`claim_resolutions`, `policyOverride`, per-alias `on_commit` /
`on_give_up` action strings) at ~14 sites. None had been touched
by the prior cycles. This subsection logs the rewrite scope.

### `docs/node-graph-design.md` rewrite

- **Line 3 — header.** `5-verb protocol (Open / Commit / Abandon /
  Delete / Release)` → `4-verb protocol (Open / Commit / Abandon /
  Release)`.
- **Line 71 — node properties list.** `claim_resolutions` bullet
  removed entirely (the field is gone from the template grammar).
- **Line 148 — store protocol summary.** `uniform 5-verb protocol —
  Open / Commit / Abandon / Delete / Release` → `uniform 4-verb
  protocol — Open / Commit / Abandon / Release`.
- **Line 163 — kind / Store interface.** `universal 5-verb Store
  interface` → `universal 4-verb Store interface`.
- **Line 198 — pick policies / interface count.** `same 5-verb
  interface` → `same 4-verb interface`.
- **Line 200 — pick policies / item-claim semantics (§4.5).**
  Whole paragraph rewritten. Deleted: `claim_resolutions:`
  declaration prose, `claim_resolutions[<alias>].on_commit` /
  `.on_give_up` description, `commit | abandon | delete |
  release_to_back | release_to_head` action enumeration,
  `policyOverride` argument reference. Replaced by: rimsky-side
  surface is success/failure binary (`Commit` / `Abandon`);
  substrate disposition lives in per-substrate config (e.g.
  postgres reference store-service's per-pick-policy
  `on_commit_default` / `on_give_up_default` in its own
  `config.yml`); those names are substrate-internal and do not
  appear in rimsky's template grammar. Enqueue paragraph's
  pointer to `POST /admin/stores/:name/pick-policies/:selector/items`
  also dropped (that endpoint is gone — each store-service owns
  its own admin surface).
- **Lines 218–219 — held-claim aggregate-outcome rule (§4.6).**
  `fire the acquirer's declared on_commit action` /
  `fire the acquirer's declared on_give_up action` → `call
  Store.Commit for the held claim` / `call Store.Abandon for the
  held claim`. New paragraph added explaining that the
  rimsky-side surface carries no per-alias action vocabulary;
  the substrate-internal action vocabulary (release-to-back,
  release-to-head, items-table delete) lives in store-service
  config.
- **Line 221 — auto-terminal substrate verb description.**
  Deleted: `(Commit / Abandon / Delete, with policyOverride from
  the acquirer's claim_resolutions block)`. Replaced by: `The
  substrate verb shares the same rimsky-side SQL transaction as
  ...`.
- **Line 223 — failure-propagation closing sentence.** `the
  auto-terminal fires on_give_up once the subgraph completes` →
  `the auto-terminal fires Abandon once the subgraph completes`.
- **Line 256 — implicit rollback prose (§4.9).** `its claim is
  Abandon'd (or Delete'd)` → `its claim is Abandon'd`. Added a
  parenthetical clarifying that whether the substrate then
  deletes an items-table row, releases to the back, or drops a
  staging area is its own per-policy disposition concern.
- **Line 410 — substitution exclusion list (§7.2).** Bullet
  `claim_resolutions[*].source and claim_resolutions[*].store`
  removed entirely (the field is gone, so it cannot appear in
  the exclusion list).
- **Lines 510–515 — node contract YAML (§8).** Whole
  `claim_resolutions:` block (6 lines including header / comment /
  per-alias `on_commit` / `on_give_up` action strings with
  `commit | abandon | delete | release_to_back | release_to_head`
  vocabulary) deleted. The block now flows directly from
  `attributes:` to `error_types:`.
- **Line 572 — on_work_complete handler (§8.2).** `fire each
  non-held claim's per-claim verb (Commit / Abandon / Delete per
  claim_resolutions)` → `fire each non-held claim's per-claim
  substrate verb (Commit on success, Abandon on failure — what
  those mean substrate-side is governed by per-substrate config)`.
  Held-claim resolution prose updated to say `fires Commit or
  Abandon at holding-subgraph completion based on aggregate
  outcome`.
- **Line 601 — template-deploy validation (§9.1).** Deleted:
  `every claim referenced by an inherits: declaration must have
  claim_resolutions[<alias>] declared on the acquiring node
  (algorithm in spec §18.4)`. Replaced by: `every claim alias
  referenced by an inherits: declaration must be acquired by
  some upstream node within the same template`.
- **Line 626 — execution flow (§9.3 step 7).** `fires the
  per-claim verb (Commit / Abandon / Delete per
  claim_resolutions)` → `fires the per-claim substrate verb
  (Commit on success, Abandon on failure — substrate disposition
  is governed by per-substrate config)`. Held-claim prose
  updated to say `fires Commit or Abandon at holding-subgraph
  completion based on aggregate outcome`.
- **Line 828 — glossary section's Node properties.**
  `claim_resolutions` removed from the property enumeration.

### Strategy / pattern

Followed the pattern set by `docs/glossary.md`'s "Substrate-internal
vocabulary (not part of rimsky's protocol surface)" section. The
substrate-internal terms (`release_to_back`, `release_to_head`,
items-table `delete`, `on_commit_default`, `on_give_up_default`)
are allowed to appear in `node-graph-design.md` only when
explicitly labeled as substrate-internal config (e.g. "the
postgres reference store-service's per-pick-policy
`on_commit_default` / `on_give_up_default` in its own
`config.yml`"). They are never restated as rimsky-side
template-grammar or wire-protocol vocabulary.

### Verification

Final completion-check grep on `docs/node-graph-design.md` for
`5-verb`, `5 \+ 1`, `5\+1`, `Store\.Delete`, `policy_override`,
`policyOverride`, `claim_resolutions`, `\.on_commit\b`,
`\.on_give_up\b`, `\bDelete\(.*region`: zero matches. The
remaining occurrences of `on_commit_default`,
`on_give_up_default`, `release-to-back`, `release-to-head`,
`items-table delete` are all in substrate-internal-config
contexts (lines 195, 199, 220, 257) and explicitly labeled as
such.

This is a docs-only edit — no Go build needed.
`node-graph-design.md` is a conceptual reference, not generated
material; rendering is unchanged.

## Cycle-5 fixes — 2026-04-30

A fifth reviewer pass surfaced two pre-existing documentation gaps
in `docs/node-graph-design.md` §8 (the canonical node-contract
reference). Both relate to drift between the §8 YAML example and the
JSON shape accepted by `core/controlapi/templates.go::templateNodeDefJSON`,
and both are now load-bearing because the cleanup-cycle-1 strict-decode
addition (`decoder.DisallowUnknownFields()` on `handleDeployTemplate`)
makes copy-paste-from-§8 templates actively reject. Both fixed in this
cycle.

### Issue 1 — `quality_rules:` undocumented in §8 YAML

**What was wrong:** The §8 node-contract YAML reference did not
document the `quality_rules:` field. `templateNodeDefJSON` accepts it
(`core/controlapi/templates.go:51`), the supervisor evaluates it on
Complete (per §8.2 step 2 at `docs/node-graph-design.md:579`), and v1
ships three evaluators (`row_count_ratio`, `no_nulls`,
`nullable_fields_present` — see `core/qualityrule/rules.go`), plus the
`custom` slot for consumer-registered evaluators. Readers using §8 as
the canonical reference would not learn the field exists.

**Fix:** Added a `quality_rules:` block to the §8 YAML between
`attributes:` and `error_types:`. The block enumerates the v1-shipping
evaluator-name set inline in a doc-comment (consistent with the smoke
fixture's "documents the spec, not just the v1-shipping set" pattern),
points readers at `core/qualityrule/rules.go` for the canonical list,
restates the severity → blocking semantics from §8.2, and shows the
three-field shape (`type` / `config` / `severity`). Severity default
(`error`) is documented inline.

### Issue 2 — `deps:` vs `dependencies:` mismatch

**What was wrong:** §8 YAML used `deps:` at two sites
(`docs/node-graph-design.md:454` in the §7.5 conditional-instantiation
example, and `docs/node-graph-design.md:477` in the §8 node-contract
YAML). The JSON shape is `dependencies` (per `templateNodeDefJSON.Dependencies`'s
`json:"dependencies"` tag). Pre-cleanup the `deps:` field was silently
dropped by Go's JSON decoder; post-cleanup (cycle 1's
`DisallowUnknownFields()`) it raises `json: unknown field "deps"` and
the template deploy is rejected with 400. So §8-copy-pasted templates
now break at deploy.

**Fix:** Replaced both `deps:` sites with `dependencies:`. Verified by
re-grepping `\bdeps:` across `docs/node-graph-design.md` — zero
matches.

### Verification

Completion-check greps on `docs/node-graph-design.md`:
- `\bdeps:` — zero matches.
- `quality_rules` — line 510 (new YAML block in §8) and line 579 (the
  existing §8.2 reference).

Docs-only edit. No Go build needed.

## Cycle-6 fixes — 2026-04-30

Final verification pass surfaced three more `deps`-prose sites the
cycle-5 sweep had missed. All fixed in-pass during the cycle-6
verification:

- `docs/node-graph-design.md:114` — state-machine transitions list
  prose: "deps fresh" → "dependencies fresh".
- `docs/architecture.md:169` — pure-cascade sweep description: "all
  deps `fresh`" → "all dependencies `fresh`".
- `docs/architecture.md:172` — ready sweep description: same rewrite
  as line 169.

Substitution-directive `{{deps.<node>.<field>}}` paths are
intentionally untouched — `core/attributes/substitution.go:148`
switches on the literal token `"deps"`. The directive prefix is
unrelated to the YAML field name and remains canonical.

After cycle 6 the final exhaustive sweep across `docs/`, `test/`,
`examples/`, and `deploy/` (excluding spec, plan, history, and
CHANGELOG paths) returns zero matches for `5-verb`, `5+1`,
`Store.Delete`, `policy_override`, `policyOverride`,
`claim_resolutions`, `Delete(...region)`, and `\bdeps:`. The
`docs/store-author-guide.md` body remains v2-shaped behind its own
status banner (per the cleanup spec's explicit deferral; banner
itself is up-to-date as 4+1).

Docs-only edit. No Go build needed.

## Final cleanup-cycle status

Six fix-review cycles total (one beyond the skill's standard 3-cycle
guard rail, authorized by the user mid-run). Cumulative scope:

- **Cycles 1–3 (24 issues):** original cleanup-cycle spec scope —
  proto, Go interface, substrate impls, bridge, supervisor, template
  grammar, control-api strict-decode, scenario harness, unit /
  scenario / smoke tests, doc cascade across CLAUDE.md /
  architecture.md / operator-guide.md / glossary.md / CHANGELOG.md /
  v3-completion.md, plus incidental drift discovered during
  cycle-3 review.
- **Cycle 4 (16 sites):** `docs/node-graph-design.md` v2-era drift
  the original Task 18 doc cascade had omitted.
- **Cycle 5 (2 issues + 9 in-pass):** §8 YAML gaps (`quality_rules:`
  undocumented; `deps:` → `dependencies:`); verifier swept and fixed
  parallel `deps`-key drift across `node-graph-design.md` and
  `operator-guide.md`.
- **Cycle 6 (3 in-pass):** final `deps`-prose sites in
  `node-graph-design.md` and `architecture.md`.

Implementation (proto + code + tests + smoke + spec + plan +
in-scope doc cascade) is review-clean; `make lint` and `make tidy`
remain user-side follow-ups (no host Go toolchain in this
environment).
