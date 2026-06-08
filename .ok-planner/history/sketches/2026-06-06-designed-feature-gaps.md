# Rimsky: User Features We Spec'd, Marked Done, and Never Actually Shipped

## Executive summary

You asked one question: of the user features we designed and marked done, which were quietly not implemented or deferred in execution? The answer is **42 confirmed user-facing gaps**. Every one is a feature a user can read about — in a spec, a concept doc, a CLI flag, an API field, a config key, or a declared error class — and reach for, only to find it absent, silently ignored, or behaving the opposite of the design.

**Lead with the worst:** the single most dangerous gap is the **claude-agent sign-off gate that passes when it should fail**. When the agent produces its output via incremental `attributes_set` (exactly as the `report_complete` tool *instructs* it to), the gate verifies a signature over the literal string `"null"` instead of the real output (`lib/services/executors/claude-agent/src/agent-run.ts:694-722`, `signoff.ts:39-47`). The host believes the output is cryptographically attested. It is not. An unattested run passes a security gate, silently. Two other auth gaps are nearly as bad: **dry-run is no longer identity-bound** — any write-capable key can flip `?dry_run=true` at will, and a `mode: dry_run` grant is silently swallowed (`auth_middleware.go:299-306`), so an "attempt-only" key cannot be minted; and **grant `scope` is parsed, persisted, then ignored** — an operator who scopes a key believes it is constrained while the matcher grants platform-wide access (`check.go:22-29`).

**The dominant pattern is silent-skip and contradicts-design:** valid, validated configuration is accepted and then ignored. Operator `frame: in` invalidate is downgraded to `frame: next` while the CLI flag, API field, and dry-run echo all keep claiming it worked. `RIMSKY_SENSOR_CRON_STATE_DSN` is read by nobody. Multi-tag `?tag=` filters drop every repeat past the first. Declared error classes (`agent/rate_limited`, `pg/swap_failed`, etc.) validate in `error_types:` config but are emitted at zero sites — the policy is dead on arrival. A `compose:` namespace is reserved and actively rejected by the CLI for a `rimsky compose` command that **does not exist** and returns "unknown command".

**Deferral tally — the part you most care about:** of the 42 gaps, **30 carry a deferral tag** (deferralTag != "none"): 13 were quietly tagged "out of scope / V2 / future" mid-spec, and 17 more were dropped in execution and recorded only in divergence files, plan-notes, or self-justifying code comments — never surfaced to you for sign-off. The remaining 12 carry no deferral tag at all: they were presented as delivered and simply were not built. **You approved zero feature deferrals. We made at least 30.**

---

## Section 2 — Deferred WITHOUT sign-off (the 30 you were never shown)

Every row below has `deferralTag != "none"`. These are the deferrals you say were never surfaced. Quote = the out-of-scope/V2/future line or the divergence/code note that closed the feature on paper.

| Feature | How it was deferred (quoted) | Where |
| --- | --- | --- |
| claude-agent MCP catalog + 4 transports + policy | "documentation should reflect implemented reality ... documented-but-unimplemented ... rimsky-docs fixes (scrub the descriptions to match the code), not rimsky-core work" | `sketches/rimsky-core-bugs.md:8-12` |
| Atomic-staging reference fs ClaimProducer + pattern doc (Piece 3) | built in commit e1487e1, deleted wholesale in c1ce756 ("carve ... docs, examples/atomic-staging-fs-producer/ out") | git history; `examples/README.md:23-28` |
| OnNodeParked lifecycle event | "explicitly out of scope for this cycle ... opens design questions ... don't have settled answers" | `spec:2026-05-14-subscription-cascade...:367-371` |
| http-node 429 rate-limit park | "Rate-limit-on-429 park behavior is deferred to its own design pass" | `spec:2026-05-14...:365,373` |
| Per-key dry-run via grant mode | "There is no per-entry mode modifier; dry-run is a per-request flag" (code-comment-only redesign; not in divergence record) | `auth/grant.go:33-34`, `check.go:19-21` |
| Action+resource scoping | "V1 is action-only ... Scoping deferred to V2" | `spec:2026-05-15-control-plane-mcp-and-auth:20,167` |
| Per-key rate limits | "Rate limits ... Deferred to V2 ... do not ship as platform surface here" | `spec:2026-05-15...:19,28` |
| MCP stdio transport | "V1 is HTTP-transport only ... Stdio would require a separate forwarder process; deferred" | `spec:2026-05-15...:23,378` |
| MCP conformance check | "M2 conformance probe: not extended ... What was implemented: Nothing ... The implementer's report flagged this explicitly" | `divergences 2026-05-15...:17-23` |
| Operator-defined roles + CLI config-dir workaround | "V1 ships compiled-in role templates ... Server has no role concept ... drop additional JSON files into a CLI-resolved config directory" (workaround never built) | `spec:2026-05-15...:21,332` |
| Producer-aware ScopesConflict wired into acquisition | No deferral recorded anywhere; C3 note records only interface/fake additions, never that acquisition keeps calling byte-equal — "un-surfaced, un-recorded execution drop" | `plan-notes 2026-05-15-data-platform...:237-246` |
| Multi-replica sensor-cron advisory lock | "scoped but not implemented ... Did NOT write the ... test ... because the implementation doesn't exist yet ... out of scope for this plan" | `plan-notes 2026-05-17...:115-122` |
| sensor-cron `RIMSKY_SENSOR_CRON_STATE_DSN` durability | "No state_db.go file exists ... env-var plumbing ... is also absent ... The task is skipped" | `divergences 2026-05-17...:16-31`; `sensor.go:16-21` |
| row_count_ratio on SQL store | "The sketch's row_count_ratio is deferred ... v1-ambiguous ... Addable later" | `spec:2026-05-19...:298,494` |
| Multi-tag AND `?tag=` filter | "Multi-tag combinations ... are not in v1; addable later when a friction case lands" | `spec:2026-05-19...:240`; `nodes.go:311-312` |
| Inheritance-by-reference / base nodes | "Inheritance-by-reference (deferred) ... It was deferred" | `spec:2026-05-19...:63-69,493` |
| Claim-scope substitution `{{claim.<alias>.claim_scope}}` | Not deferred — "rename applied to the resolver but silently missed in the template validator; Audit G never caught it" | `spec:2026-05-19...:599-601,625` (rename mandate) |
| Atomic staging on SQL (postgres) substrate | DOUBLE deferral: "not yet shipped ... wrap the postgres store or supply a sidecar" then "a separate feature, not part of recovering coverage" | `concept:atomic-staging` Notes 2026-05-19; `spec:2026-06-02...:326-330` |
| postgres-store `pg/claim_unavailable` + `pg/swap_failed` emit sites | "UNRECORDED, never-surfaced execution skip ... classes added to declaredErrorClasses per step 3 but the step-2 emit sites were silently dropped" | `plan 2026-05-23...:1349,1351` (directed); no divergence entry |
| http-node configurable error-class field name | "Unrecorded silent drop" — spec states "configurable field name; default error_class"; divergence silent | `spec:2026-05-23...:437` |
| Late-bind publisher/validation/data-processing via proxy | "ship as registered services that return UNIMPLEMENTED until wired in follow-up specs" | `spec:2026-05-24...:82-83,635`; `concept:host-agent-proxy` Notes |
| Per-run-scope spawn isolation | "Spawn-dedup keyed on instance-id, not run-scope-id ... Forced choices driven by what the existing protos actually carry" (recorded as forced choice, not approved deferral) | `divergences 2026-05-24...` §11c, §12 |
| Per-binding env/args/cwd/timeout overrides | "Per-binding env / args / cwd / timeout overrides" listed under Out of scope | `spec:2026-05-24...:639,580` |
| Pool-routed / multi-agent bindings | "Pool-routed bindings (multiple agents serving a capability with rimsky picking one)" | `spec:2026-05-24...:637,578` |
| Long-running pinned (attach) bindings | "Long-running pinned bindings (B from the earlier 2×2 — attach to an already-running local process rather than spawn)" | `spec:2026-05-24...:638` |
| Anonymous-mode late-bind | "Anonymous-mode users therefore cannot use late-bound services in v1 — flagged as a tension" (all 3 workarounds marked "do NOT pick") | `spec:2026-05-24...:187`; `tension:anonymous-mode-locks-out-late-bind` |
| rimsky-host-agent-conformance binary | "rimsky-host-agent-conformance binary" under Out of scope; "a follow-up; not v1" | `spec:2026-05-24...:~636,591` |
| MCP push delivery of breakpoint hits | "MCP push semantics ... are also out of v1 scope ... resources/subscribe and notifications/resources/updated are NOT in this spec" | `spec:2026-05-24...` §1,§6.1; `spec:2026-06-02...` #7 |
| Prebuilt CLI binaries on the GitHub Release | "CLI binaries are not release-attached assets ... binary distribution is a separate concern ... a future spec" | `spec:2026-05-27...:582` |
| rimsky watch chronological feed | "watch feed is source-grouped per poll cycle, not chronologically interleaved ... Inferred reason: Cleaner/simpler shape" | `divergences 2026-05-28...:63-78` |
| template lint drift_summary | "drift_summary ... Requires a stable error-code taxonomy ... Its own spec when wanted" (prerequisite also never built) | `spec:2026-05-28...:29-30` |
| Live event SSE + breakpoint-hit emission | "Events SSE + breakpoint-hit event emission -> captured as a new sketch on approval ... explicitly out of scope" | `spec:2026-05-29...:33,18-19` |
| Template-level `terminate_after_run` default | "No template-level default for the flag (per-instance only); a template default can be added later" | `spec:2026-06-03...:112-113` |
| Richer self-termination modes (drain-to-quiet, count) | "richer modes — drain-to-quiet, count-based — can be added later behind the same flag" | `spec:2026-06-03...:151-152` |
| Sign-off gate × incremental attributes_set | "Incremental attributes_set writeback interacting with the gate ... reconciling sign-offs with incremental writeback needs its own design" | `spec:2026-06-04...:178` |
| Validator connection-header secret/env-ref resolution | "Secrets / env-refs for validator connection headers ... is a separate later pass" | `spec:2026-06-04...:177,46` |
| Published signing contract + reference validator | "publishing a reusable example validator MCP server ... is a separate effort" | `spec:2026-06-04...:180`; `divergences 2026-06-04...:345` |

(The two `rimsky compose` rows and the additional-blob-backends row also carry deferral context; compose appears in §3 under CLI. Count of deferralTag != "none" = 30, broken out in the rollup.)

---

## Section 3 — Gaps by subsystem (severity-ordered within each)

### CLI / onboarding

| Feature | Design promised | Code actually does (file:line) | Status | Sev |
| --- | --- | --- | --- | --- |
| `rimsky compose up`/`down` subcommand | declarative project bring-up/teardown owning `compose:` namespace (`modeling-layer-contract §12.2:427, §12.4:435`) | no `compose` case; default prints `unknown command "compose"`, exit 2 (`cmd/rimsky/main.go:23-82`); endpoint/guardrails built but no command | missing | high |
| conformance claim-producer terminal verbs | 7-category suite incl. Commit/Abandon/Release + idempotency-under-retry (`spec:2026-05-04-service-protocol-contract §2.6:178-186`) | `Run` drives only Capabilities/Open/uniformity/optional probes; never calls Commit/Abandon/Release/re-Open (`conformance/claimproducer/runner.go:41-139`) | missing | high |
| `rimsky compose up`/`down` (CLI surface dup) | a `Compose` verb group (`rimsky.md:37`, `tag.md:12`) | only reserved-prefix guard ships (`templates.go:209,236-238`; `tags.go:35`); `compose` returns unknown command (`main.go:78-81`) | missing | medium |
| `rimsky watch` chronological feed | one timestamp-ordered feed (`spec Feature 4:140`) | drains events, then hits, then terminal per poll cycle; no cross-source sort (`cmd/rimsky/cli/watch.go:62-142`) | partially-wired | low |
| Prebuilt CLI binaries on GitHub Release | downloadable cross-platform binaries (`spec:2026-05-27...:582`) | `make cli-release` builds them but is invoked nowhere; `gh release create` passes only notes (`Makefile:252,81-88`; `SKILL.md:292`) | spec-tagged-out-of-scope | low |

### control-api / MCP

| Feature | Design promised | Code actually does (file:line) | Status | Sev |
| --- | --- | --- | --- | --- |
| Operator in-frame invalidate (`frame: in`) | join the running cascade frame (`spec:2026-05-05 §5.1,§5.4`) | downgraded to `frame: next` — no SourceNodeID/FrameID on operator path, so `invalidateNextFrame` always wins (`cascade_invalidate.go:238-241`); CLI/API/dry-run still advertise `in` (`nodes.go:154-159,177-185`) | silent-skip | medium |
| Per-key rate limits | forward-compat `rate_limit` grant field, platform enforcement (`spec:2026-05-15:19,28`) | binary allow/deny only; no token bucket/quota/429 anywhere (`auth_middleware.go:257-346`); field swallowed into Extras | spec-tagged-out-of-scope | medium |
| MCP stdio transport | reachable by stdio-only MCP clients via forwarder (`spec:2026-05-15:23,378`) | only `POST/GET /mcp` HTTP mounted (`mcp_route.go:92-94`); no forwarder binary | spec-tagged-out-of-scope | medium |
| Resolution-flavor on node detail | dashboard renders passed vs committed vs non-propagating (`spec:2026-05-05 §6.2`) | `nodeResponse` omits `SettlingSignalType` though present on NodeRow; exposed only on run-tree/backfills (`controlapi/nodes.go:32-77`, `backfills.go:358`) | partially-wired | low |
| MCP conformance check | `rimsky conformance mcp` against live skin (`divergences 2026-05-15:17-23`) | no `mcp` case; probe is an executor stub probe; coverage only in-process test (`conformance.go:45-65,499-581`) | deferred-never-resumed | low |
| MCP push of breakpoint hits | resources/subscribe + pushed notifications (`spec:2026-05-24 §6.1`) | only polling read; subscribe → method-not-found; idle SSE keep-alive (`mcp/server.go:144-157,167-200`) | spec-tagged-out-of-scope | low |
| Multi-tag AND `?tag=` filter | AND across repeated `?tag=` (`spec:2026-05-19:240`) | reads first value only into `NodeListFilter.Tag`; drops repeats (`controlapi/nodes.go:313`; `persistence/nodes.go:89-91`) | spec-tagged-out-of-scope | low |
| Forensic last-attribute read (GetLatestByNode) | wired into control-api/observability/lineage/dashboard (`spec line 116`) | primitive + denorm column + index exist; zero user-facing callers — every named surface returns row/schema/lineage (`controlapi/nodes.go:99-119`, `observability/handler.go:423-469`) | partially-wired | low |
| template lint drift_summary | category-grouped validation errors (`spec:2026-05-28:29-30`) | flat `{path,msg}` only; prerequisite error-code taxonomy never built (`template_validator.go:22-25`) | spec-tagged-out-of-scope | low |
| Live event SSE + breakpoint-hit emission | SSE on GET /events + hits in event log (`spec:2026-05-29:33`) | cursor-poll list only; hits written solely to `rimsky_breakpoint_hits` (`events.go:31-32`; `breakpoint_eval.go:246`) | spec-tagged-out-of-scope | low |

### auth

| Feature | Design promised | Code actually does (file:line) | Status | Sev |
| --- | --- | --- | --- | --- |
| Per-key dry-run via grant mode | un-flippable attempt-only key (`spec:2026-05-15:35,236-257`) | dry-run from caller's `?dry_run=true` only; grant `mode` swallowed into Extras (`auth_middleware.go:299-306`; `grant.go:42-49`) | contradicts-design | high |
| Action+resource scoping | key restricted to e.g. template-tag `analytics` (`spec:2026-05-15:20`) | matches action string only; `scope` persisted-but-ignored → silent over-grant (`check.go:22-29`; `action.go:23-41`) | spec-tagged-out-of-scope | high |
| Operator-defined roles + config-dir workaround | drop JSON into `~/.rimsky/roles/` loaded like bundled set (`spec:2026-05-15:21,332`) | only `--role-file` path + fixed embedded bundle; no config-dir scan → named custom role fails (`auth_common.go:76-101`; `roles/embed.go:22-28`) | partially-wired | low |

### host-agent / late-bind

| Feature | Design promised | Code actually does (file:line) | Status | Sev |
| --- | --- | --- | --- | --- |
| Per-run-scope spawn isolation | one isolated child per (run_scope, binding) with run-scope lifetime (`concept:host-agent-proxy.md:24`) | keys every spawn on (instance_id, binding) — no wire field carries run_scope_id; all run-scopes share one long-lived child (`dispatch.go:131,164`; `executor.proto:30-94`) | contradicts-design | high |
| Late-bind publisher/validation/data-processing | proxy fronts all protocols (`spec:2026-05-24:247,635`) | 3 handlers return `codes.Unimplemented`; never call spawn path (`unimplemented_handlers.go:22-40`; `dispatch.go:90`) | stubbed-noop | medium |
| Per-binding env/args/cwd/timeout | extensible per-binding shape (`spec:2026-05-24:639`) | `Binding{path}` only; exec with no args, inherited-only env, global cwd/timeout (`hostagent/spawn.go:72-76`; `host_agent.proto:76-79`) | spec-tagged-out-of-scope | medium |
| Pool/multi-agent routing | proxy fronts "a fleet of dev-machine daemons" (`spec:2026-05-24:5,637`) | one connection per api-key, latest-wins displacement (`state.go:28,263-274`; `dispatch.go:121`) | spec-tagged-out-of-scope | medium |
| Anonymous-mode late-bind | unauthenticated bootstrap can register late-bind (`spec:2026-05-24:187`) | every dispatch from anon instance hard-fails `host_agent_not_connected` (null owner short-circuits routing) (`dispatch.go:117-118,261-263`) | spec-tagged-out-of-scope | medium |
| Long-running pinned (attach) bindings | attach to already-running process (`spec:2026-05-24:638`) | only spawn-fresh; `Binding{path}` has no address field; sole bring-up frame is Spawn (`hostagent/spawn.go:72`; `host_agent.proto:76-79`) | spec-tagged-out-of-scope | low |
| rimsky-host-agent-conformance | agent-side conformance binary (`spec:2026-05-24:~636`) | no such binary/subcommand/runner; `conformance host-agent` → unknown subcommand (`conformance.go:46-66`) | spec-tagged-out-of-scope | low |

### stores

| Feature | Design promised | Code actually does (file:line) | Status | Sev |
| --- | --- | --- | --- | --- |
| Producer-aware ScopesConflict in acquisition | call `ScopesConflict` for overlapping scopes when advertised (`spec:2026-05-15:522-526`) | acquisition runs byte-equal only; fan-out sub-claim path runs NO conflict check; peer method has zero production callers → invariant 4b unenforced (`runner_acquire_claims.go:74,231`; `runner_subclaim.go:130-264`) | partially-wired | high |
| `{{claim.<alias>.claim_scope}}` substitution | validates at submit, resolves at runtime (`spec:2026-05-19:599-601`) | split brain: validator admits only old `scope` (HTTP 400 on new); resolver admits only new `claim_scope` (runtime miss on old) — neither spelling works (`template_validator.go:1349`; `attribute/substitution.go:629`) | partially-wired | high |
| fs-store `sync_strategy: explicit` admin refresh | operator-triggered queue refresh via admin endpoint (`spec §8.5`) | config accepted, no-auto-sync honored, but NO sync route (only `/admin/bump-to-head/`); `runSync` unexported, drains to sticky Unavailable (`filesystem/store/admin.go:30-32`; `pick_policy.go:74`) | partially-wired | medium |
| Atomic-staging fs reference producer (Piece 3) | copyable stage-then-swap producer + pattern doc | built (e1487e1) then deleted whole tree (c1ce756); surviving fs store is sync-only with no-op Commit/Abandon (`filesystem/store/store.go:139-143,257-300`) | missing | medium |
| Atomic staging on SQL (postgres) substrate | Open reserves staging schema, Commit atomic swap (`concept:atomic-staging` table) | Open echoes selector as Address/ClaimScope; Commit/Abandon no-op for scope-bytes; `pg/swap_failed` never emitted (`postgres/store/store.go:196-208,274-296,324-337`) | stubbed-noop | medium |
| row_count_ratio on SQL store | fuse into SQL check vocabulary (`spec:2026-05-19:298`) | `Compile()` omits it; executor rejects at execute time `pg/attribute_invalid "unknown check kind"` (`sql-checks/compile.go:61-69`; `postgres/server/executor.go:120-122`) | spec-tagged-out-of-scope | medium |
| Template default for `terminate_after_run` | template-level default all instances inherit (`spec:2026-06-03:112-113`) | per-instance-create flag only; no template field or column default (`spec/template.go:31-99`; `instances.go:132,443`) | spec-tagged-out-of-scope | low |
| Richer self-termination (drain-to-quiet, count) | selectable modes behind the flag (`spec:2026-06-03:151-152`) | plain bool fires at next frame-end; no mode/run-count column (`postgres/frames.go:145-171`; migrations 005) | deferred-never-resumed | medium |
| `pg/claim_unavailable` + `pg/swap_failed` emit | emittable classes operators subscribe to (`plan:2026-05-23:1349,1351`) | declared in `declaredErrorClasses()` so config validates; zero emit sites; conflicts surface via OpenResponse_Unavailable (`postgres/server/executor.go:77,79,107-144`) | stubbed-noop | low |
| Additional blob backends (s3/gcs/azure/redis) | future operator-implementable backends (`spec:2026-05-08:388-390`) | only 4 v1 backends; `ValidateBlobConfig` rejects any other name (`blob_config.go:103-108`); interface + conformance ship | spec-tagged-out-of-scope | low |
| Inheritance-by-reference / base nodes | N config profiles per executor inherited by ref (`spec:2026-05-19:63-69`) | only single `defaults.attributes.by_executor` layer; no inherit/extends field (`spec/template.go:82-99,113-230`) | spec-tagged-out-of-scope | low |

### sensors / publishers

| Feature | Design promised | Code actually does (file:line) | Status | Sev |
| --- | --- | --- | --- | --- |
| Multi-replica sensor-cron advisory lock | exactly-once cron firing across replicas w/ fail-over (`plan:2026-05-17 Task 5`) | in-process `sync.Mutex` only; no advisory lock/DB; test pins N× double-fire as v1 (`sensor-cron/sensor.go:82-95`; `multi_replica_test.go:96-134`) | deferred-never-resumed | medium |
| http-node configurable error-class field | configurable JSON field name, default `error_class`, `_unspecified` fallback (`spec:2026-05-23:437`) | hardcodes `decoded["error_class"]`; returns `http/expectation_mismatch` not `_unspecified` (`http-node/server.go:390,395`) | missing | low |
| sensor-cron `RIMSKY_SENSOR_CRON_STATE_DSN` | durable cron firing across restarts (`spec Stage 3 step 2`) | var read by nobody; no `state_db.go`; in-memory only — peers all honor theirs (`sensor-cron/main.go:37-39`; `sensor.go:16-29`) | silent-skip | low |

### executors

| Feature | Design promised | Code actually does (file:line) | Status | Sev |
| --- | --- | --- | --- | --- |
| Sign-off gate × incremental attributes_set | gate binds bound output regardless of how emitted (`spec:2026-06-04:178`) | incremental path (as tool instructs) → gate verifies signature over literal `"null"`; unattested run passes (`agent-run.ts:694-722`; `signoff.ts:39-47`) | spec-tagged-out-of-scope | high |
| claude-agent MCP catalog + 4 transports + policy | startup catalog, `{ref:}` resolution, http/stdio/module/http-loopback, allow_inline policy | only inline HTTP MCP `{name,url,headers}`; no catalog/ref/transport/policy (`cli-runner.ts:60`; `agent-run.ts:181-186,793-798`; `main.ts:33-87`) | missing | high |
| http-node 429 rate-limit park | park + computed resume_at auto-wake on upstream 429 (`spec:2026-05-14:365`) | classifies 429 as hard Error terminal; never emits Park (`http-node/server.go:383-406`) | spec-tagged-out-of-scope | medium |
| claude-agent error classes (rate_limited/context_exceeded/refused/tool_use_failed) | emitted classes operators policy/subscribe on (`spec §claude-agent:446-457`) | all 4 declared for validator; ZERO emit sites; rate limits divert to PARK, others collapse to generic (`expected-attributes-schema.ts:180-183`; `rate-limit.ts:41`) | stubbed-noop | medium |
| Validator connection-header secret/env-ref | reach auth-gated validators without leaking creds (`spec:2026-06-04:177,46`) | header strings passed verbatim; no `${env:}`/secret resolution; secret leaks via persisted attributes or sent unresolved (`cli-runner.ts:319-323`; `server.ts:851-858`) | spec-tagged-out-of-scope | medium |
| Published signing contract + reference validator | reusable validator + host-facing signing-contract docs (`spec:2026-06-04:180`) | only test-only signer (excluded from dist); no contract doc, no validator service (`signoff-test-signer.ts:5-8`; `tsconfig.json:20`) | spec-tagged-out-of-scope | medium |

### subscriptions / lifecycle

| Feature | Design promised | Code actually does (file:line) | Status | Sev |
| --- | --- | --- | --- | --- |
| OnNodeParked lifecycle event | push notification on node park (`spec:2026-05-14:367-371`) | service declares only 7 non-park RPCs; only poll diagnostics + gauge (`lifecycle.proto:24-39`; `admin_diagnostics.go:69-70`) | spec-tagged-out-of-scope | low |

### control-api / template-registration validation

| Feature | Design promised | Code actually does (file:line) | Status | Sev |
| --- | --- | --- | --- | --- |
| `--ignore-missing-refs` startup flag | operator-set toggle disabling registration-time schema validation (`spec:115-119,600-602`) | flag exists nowhere; always-on internal soft-fail heuristic instead (`template_validator.go:951-977`; `runner_dispatch.go:424-442`) | missing | low |

---

## Section 4 — By-status rollup

| Status | Count | What it means |
| --- | --- | --- |
| **silent-skip** | 2 | Valid, validated config accepted, then ignored. **The most corrosive.** `frame: in` invalidate (200 OK, runs as `next`); `RIMSKY_SENSOR_CRON_STATE_DSN` (no-op, no log). |
| **contradicts-design** | 3 | Code does the opposite of the design. Per-run-scope spawn isolation (shares one child); per-key dry-run (caller-elective, not identity-bound); — and the `claim_scope` split-brain straddles this (counted under partially-wired). |
| **stubbed-noop** | 5 | Registered/declared so config validates, then does nothing. claude-agent error classes; pg/* error classes; SQL atomic-staging; late-bind publisher/validation/data-processing. These are the worst kind of "marked done": the surface exists and lies. |
| **missing** | 7 | No code at all. `rimsky compose` (×2), conformance claim-producer terminals, MCP catalog, `--ignore-missing-refs`, atomic-staging fs example, http-node error-class field. |
| **partially-wired** | 7 | Half-built; the user-facing half is absent. ScopesConflict, `claim_scope` substitution, fs-store explicit sync, resolution-flavor, forensic read, watch feed, operator roles. |
| **deferred-never-resumed** | 3 | Scoped, then dropped, never picked back up. MCP conformance, multi-replica cron lock, richer termination modes. |
| **spec-tagged-out-of-scope** | 15 | Demoted to "V2/future/out-of-scope" mid-spec. The largest single bucket. |
| **TOTAL** | **42** | |

**Deferral accountability:** 30 of 42 carry a non-"none" deferralTag — features closed without your sign-off. The 12 with no tag (presented as done, simply not built) include: conformance claim-producer terminals, resolution-flavor, fs-store explicit sync, `--ignore-missing-refs`, ScopesConflict acquisition wire-in, `claim_scope` substitution, http-node error-class field, forensic read, both `compose` rows (the second has "none"), per-key dry-run redesign, and the claude-agent error-class emission.

**Silent-skip + contradicts-design called out specifically (the 5 that accept valid config and betray it):** operator `frame: in` (silent-skip), `RIMSKY_SENSOR_CRON_STATE_DSN` (silent-skip), per-run-scope spawn isolation (contradicts), per-key dry-run (contradicts), grant `scope` over-grant (out-of-scope but behaviorally a silent over-grant). Add the 5 stubbed-noops (declared error classes, late-bind stubs, SQL staging) and you have **10 surfaces where the platform accepts the user's input and silently does not honor it** — the exact "accepted then ignored" failure you described.

---

## Section 5 — The pattern (why designed features kept not landing)

1. **Proto wiring deferred to a "later pass" that never came.** The single most repeated failure. `ScopesConflict` has a built peer client, capability negotiation, fake, and conformance suite — and zero production callers, because acquisition kept calling byte-equal "for now" (`runner_acquire_claims.go:231`). The Park outcome + resume_at auto-wake machinery is fully built, but http-node never emits it. The host-agent proto carries no `run_scope_id`, so spawn isolation was silently keyed on instance-id instead. The grant shape was made "forward-compatible" for `scope`/`rate_limit`/`mode` — and not one of those fields is ever read. We built the plumbing and never connected the faucet.

2. **Handlers/classes registered-but-unimplemented so config validates and lies.** Late-bind publisher/validation/data-processing register as `codes.Unimplemented` stubs. claude-agent declares `agent/rate_limited`/`context_exceeded`/`refused`/`tool_use_failed` in `declaredErrorClasses` (so `error_types:` validates) with zero emit sites. postgres declares `pg/claim_unavailable`/`pg/swap_failed` the same way. The validator's range-check passes, the operator's policy/subscription is dead. This is the mechanism by which "marked done" features fail in production: the registration surface exists, the behavior does not.

3. **Vocabulary advertising behavior the engine doesn't implement.** The CLI reserves and *actively rejects* the `compose:` namespace ("managed by `compose`") for a `compose` command that returns "unknown command". Concept docs (`rimsky.md:37`, `tag.md:12`, `concept:atomic-staging`) describe live flows that don't exist. The dry-run echo reports `frame: in` that never happens. The substitution doc-comment and resolver advertise `claim_scope` while the validator hard-rejects it. We shipped the words before (or instead of) the implementation.

4. **Quiet demotion to "out of scope / V2" mid-spec.** 15 features were tagged out-of-scope inside the very spec that designed them, and 17 more were dropped in execution and buried in divergence files, plan-notes, or self-justifying code comments. The dry-run-mode→query-flag swap exists *only* in code comments. The MCP-catalog deferral was "closed" by deleting docs to "reflect implemented reality." None of these reached you. The deferral machinery (divergence records, plan-notes) became a place to record drops *away from* your sign-off, not toward it.

5. **Audits and rename passes that didn't run.** The `scope`→`claim_scope` rename was mandated "across parser, examples, docs" with "Audit G" tasked to verify no stragglers — the validator was missed and Audit G never caught it, leaving a feature that fails at both gates. The atomic-staging example was built, then carved out wholesale in an unrelated "carve out bundled services" commit, and never re-added. Cleanup and verification steps were specified and then skipped, leaving designed-and-built features stranded.

---

## Section 6 — Bottom line

You asked: of the user features we spec'd and marked done, which were quietly not implemented or deferred? **42 of them.** The platform repeatedly accepts a user's valid, validated input — an API key with a `scope` or `mode`, a node with `error_types: {agent/rate_limited: ...}`, a `frame: in` invalidate, a `RIMSKY_SENSOR_CRON_STATE_DSN`, a `{{claim.alias.claim_scope}}` directive, a `compose:` tag, a sign-off gate over incrementally-written output — and then silently does nothing, does the opposite, or returns "unknown command", while every advertising surface (CLI flag, API field, concept doc, declared class, dry-run echo) keeps insisting it works. That is precisely the "it doesn't work as advertised" you reported, and the mechanism behind every "that wasn't actually implemented for some reason."

On deferrals: **you approved none, and we made at least 30.** Thirteen were demoted to "V2/future" inside the spec that designed them; seventeen more were dropped during execution and recorded only where you'd never look — divergence files, plan-notes, and code comments. The single highest-stakes one is a **security gate that passes unsigned output** (claude-agent sign-off × incremental writeback), closely followed by **dry-run no longer being identity-bound** and **grant scope being silently over-granted** — three auth/safety features marked done that actively mislead an operator about a guarantee they don't have. Fix order should be: the three silent auth/safety lies first, then the five "declared-but-never-emitted" stubs (because they make config validation a liar), then the silent-skips, then the long tail of out-of-scope demotions you can now actually decide on.

---

*Method: 89 subagents over all 37 history spec/plan units → 56 raw findings → adversarial per-finding verification against current code (5 self-cleared as already-fixed; 4 dropped as not-a-gap) → 47 confirmed, consolidated to 42 distinct in synthesis. Every row carries a design citation and a current-code file:line, re-read during this run. Generated 2026-06-06.*
