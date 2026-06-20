# Design corpus audit findings — 2026-06-15

Consolidated output from `/review-design` whole-corpus sweep. Four catalogs reviewed in parallel against the canonical rules in `skills/_shared/artifact-definitions.md`.

## How to use this file

Each finding has a `- [ ]` checkbox. Walk top-to-bottom and flip each:

- `- [x]` → fix this one (will be passed to the fixer)
- `- [ ]` (leave unchecked) → skip / defer / reject the finding
- Optional: add a `// <note>` after the checkbox line to record reasoning (won't affect dispatch)

When you're ready, tell me and I'll dispatch parallel fixers on the `[x]` set only, then re-review and loop to clean (capped at 3 cycles per catalog). I will NOT touch any unchecked finding.

## Volume

| Catalog | Findings |
|---|---|
| Tensions | 9 |
| Decisions | 18 (14 bodies + 4 TOC entries) |
| Stories | 12 |
| Concepts | ~90 |
| **Total** | **~129** |

## Rules being enforced (canonical wording lives in `skills/_shared/artifact-definitions.md`)

- **Self-containment** (concepts / stories / decisions): no file/dir paths, code-symbol citations, external doc refs, quoted code, or "owns / does NOT own" code-path lists in artifact body or frontmatter. Slug-form references across catalogs are fine.
- **Concept-specific tightening — no implementation enumeration**: concept body must not enumerate current instances (CLI verbs, library names, file extensions, route paths, wire-format identifiers, license names, command-line flags, env-var names). The concept names the kind; instances live in decisions / code / specs.
- **Story-specific framing — delivery surface is not part of the story**: which surface a user reaches through (CLI verb, HTTP route, wire message, header, config key, permission token, wire field name) is a technical choice and lives in `decisions/`. The story names the capability and what the user observes.
- **Decision exemption — Choice may name the artifact**: a decision's Choice/Rationale/Alternatives sections MAY name the specific artifact picked (Postgres, pgx, go-chi/chi, a cron string, a numeric threshold). Exemption is scoped to artifact identity — does NOT permit project file paths, code-symbol citations, external doc refs, quoted code, schema-grammar enumeration, or call-sequence narration.
- **Decision-vs-spec separation**: decision bodies name choice + reasoning. They do NOT enumerate implementation steps, schema details, file structure, or call sequences (those are spec territory).
- **Current-state-only**: no historical (`changed on YYYY-MM-DD`, `previously called X`, `was tightened per spec Q`) or forward-looking (`we plan to`, `will be replaced`, `TODO`, `deferred`, `open question for later`) content in any artifact body. No `## Notes` / `## History` / `## Changelog` sections.
- **Tension surface**: `## What is muddy` and `## Evidence` may carry code-citation evidence (snapshots, may rot — that's expected). `## Resolution candidates` is path-free and lives forward in time.
- **TOC consistency**: one-sentence TOC summaries follow the self-containment rule; every TOC bullet resolves to a live artifact and every live artifact has a TOC entry.

---

## Tensions (9)

- [ ] **`.ok-planner/design/tensions/event-vocabulary-implies-delivery.md:3 — invalid `category` value**
  Frontmatter: `category: vocabulary-drift`. Allowed: `overloaded | unspecified | unclear | inconsistent | conflicting | vestigial | muddy-boundary`. Fix: pick `inconsistent` or `unclear`.

- [ ] **`.ok-planner/design/tensions/event-vocabulary-implies-delivery.md:18 — historical journal content in `## What is muddy`**
  Quoted: "It has already misled a design: an agent proposed a per-emission event-payload-binding feature ... The feature was dropped once the mismatch surfaced." Violates CURRENT-STATE-ONLY-RULE. Fix: rewrite to describe current muddiness ("the vocabulary invites readers/agents to propose per-emission-delivery features the engine cannot implement") without the historical anecdote.

- [ ] **`.ok-planner/design/tensions/event-vocabulary-implies-delivery.md:31 — historical journal content in `## Evidence`**
  Quoted: "A dropped per-emission event-payload-binding design, abandoned once the invalidate-then-pull mechanism was understood. The corrected accuracy text now lives in `concept:named-event` and `concept:node-subscription`." Violates CURRENT-STATE-ONLY-RULE. Fix: name only the current-state surface (the concept files where accurate description lives) without the journal narrative.

- [ ] **`.ok-planner/design/tensions/graph-runtime-scheduler-import-exception.md:4 — invalid `category` value**
  Frontmatter: `category: layering`. Allowed values listed above. Fix: pick `muddy-boundary` or `unspecified`.

- [ ] **`.ok-planner/design/tensions/memory-blob-audit-gap.md:1-5 — invalid `category` value + missing `affects:`**
  Frontmatter has `category: durability` and no `affects:` field. Fix: pick an allowed category and add `affects: [blob-backend]`.

- [ ] **`.ok-planner/design/tensions/state-count-drift.md:21 — historical content in `## What is muddy`**
  Quoted: "The discrepancy isn't an error in any one place; it's a vocabulary that accreted `parked` later and hasn't been globally reconciled." Violates CURRENT-STATE-ONLY-RULE ("accreted later"). Fix: describe current divergence only without how it got that way.

- [ ] **`.ok-planner/design/tensions/pre-v1-hash-instability.md:19 — historical content in `## Why it matters`**
  Quoted: "Post-v1 readers will hit a wall: the rule says 'pinned' but historical practice says 'rebuild on breaking changes.'" Violates CURRENT-STATE-ONLY-RULE. Fix: reframe as current-state (documented stance vs. actual canonicalization-binding behavior today) without invoking historical practice.

- [ ] **`.ok-planner/design/tensions/internal-service-auth-unspeced.md:29 — spec-path reference in `## Evidence`**
  Quoted: ``"This spec: `.ok-planner/specs/2026-05-24-host-agent-and-proxy-design.md` §"Multi-process behavior" and §"Cache freshness".`` Spec paths archive to `history/specs/` and rot. Fix: replace with current-state code or concept citation, or remove.

- [ ] **`.ok-planner/design/tensions/unreachable-service-row-stall.md:28 — spec-path reference in `## Evidence`**
  Quoted: ``"This spec: `.ok-planner/specs/2026-05-24-host-agent-and-proxy-design.md` §"Error handling".`` Same issue. Fix: replace with current-state citation or remove.

---

## Decisions (18 — 14 bodies + 4 TOC entries)

### Decision bodies (14)

- [ ] **`decisions/graceful-shutdown.md` Choice — enumerates a call sequence (spec territory)**
  Quoted: "On interrupt, terminate, run-timeout expiry, or natural completion: supervisor stops new dispatches → in-flight dispatches and spawned children receive a polite terminate signal → a five-second hardcoded grace → a hard kill on anything still running → control-api stops → SQL connection closes → the most-recent-run pointer updates per `decision:artifact-layout` → exit. A second interrupt escalates to hard exit." Fix: state the choice (soft drain with a hardcoded five-second SIGTERM-to-SIGKILL grace; a second SIGINT escalates to hard exit) without the step-by-step sequence; call-sequence belongs in a spec.

- [ ] **`decisions/launch-integration.md` Choice — enumerates orchestration steps (spec territory)**
  Quoted: "start each runner in order, track each runner's stop function, select on a combined signal-or-role-failure channel, drain in reverse order." Fix: name the choice (the compose verb's three role runners reuse the single-process all-in-one launcher; the unified process-role marker is set) and drop the start/track/select/drain enumeration.

- [ ] **`decisions/launch-config-injection.md` Choice — enumerates schema fields + call-sequence (spec territory)**
  Quoted: "a unified config file matching the `concept:rimsky-yml` shape (persistence driver, blob backend, executors block, claim-producers block) and a separate supervisor-tuning file (concurrency, heartbeat, callback host/port, advertise host). Config-path environment variables are set on the in-process environment before the runners start." Fix: state the choice (synthetic unified-config and supervisor-tuning files written to the run directory and pointed at via standard config-discovery surfaces; files persist as part of the run artifact) without field enumeration or env-var sequencing.

- [ ] **`decisions/services-source.md` Choice — enumerates schema entry fields (spec territory)**
  Quoted: "Each entry is a name → entry map where the entry carries transport (executors only), endpoint, TLS mode, declared capabilities (claim producers), protocols list, and an optional observability endpoint. The claim-producers block follows the `concept:rimsky-yml` rule that each entry carries a permitted write-semantics list." Fix: name the choice (extend compose schema with executors and claim-producers blocks mirroring the unified config; compose file primary, sibling unified-config secondary; publishers and named-locks pass through from the sibling) without per-field grammar.

- [ ] **`decisions/service-spawn-flag.md` Choice — enumerates implementation call sequence (spec territory)**
  Quoted: "The verb spawns binaries directly using the same exec-and-ready-poll mechanism the host-agent uses, registers each spawned endpoint in the synthetic unified config's executors block, and dispatches to the spawned port directly via the in-process supervisor. The host-agent proxy chain (per `concept:host-agent-proxy`) is not used here because the supervisor is in-process and dials the spawned endpoint directly..." Fix: name the choice (extend the per-service spawn flag to the compose-run verb via a shared spawn primitive; in-process supervisor dials the spawned endpoint directly, bypassing the host-agent proxy chain) without per-action narration.

- [ ] **`decisions/topology-test-coverage.md` Choice — enumerates test steps (spec territory)**
  Quoted: "boot, assert one rimsky process serves all three role surfaces, drive a node to terminal, round-trip a memory-backend blob across roles ... boot scheduler, supervisor, and control-api as separate containers against shared Postgres, drive the same scenario to terminal" Fix: name the choice (services integration harness covers both the single-process all-in-one and the three-container split topologies); let a spec carry per-topology assertions.

- [ ] **`decisions/event-log-kind-enum.md` Choice — enumerates kind catalog + consumer modules + marshaling discipline (spec territory)**
  Quoted: "...the non-signal-class events: auth-related kinds, state-transition kinds, lock-acquired, work-started, attribute-substitution, breakpoint-hit, and the rest..."; "Rimsky's app logic — scheduler, supervisor, breakpoint evaluator, audit handler, read-API kind filters — consumes typed values exclusively..."; "The persistence layer marshals typed → storage at write and storage → typed at read; an unknown string at the unmarshal boundary is a defensive error, not a control-flow input." Fix: name the choice (operational event-kind values are a closed protocol-layer enum; signal-class kinds keep their existing type-path discipline; persistence marshals typed at the boundary) without enumerating kinds, modules, or marshaling sites.

- [ ] **`decisions/sqlite-multiproc-safety.md` Choice — enumerates implementation mechanism (spec territory)**
  Quoted: "(1) The SQLite-backed persistence driver's read-then-write operations are transactional — no bare read-then-write surfaces relying on in-process connection serialization — so immediate-mode transactions provide cross-process atomicity. (2) The SQLite-backed advisory locker's scheduler-tick and migration locks are filesystem-lock-based (lock files alongside the database file)... The per-name and per-scope in-tx locks already hold cross-process via immediate-mode transactions and are unchanged." Fix: name the choice (read-then-write paths are transactional under immediate-mode SQLite; the SQLite advisory locker is file-lock based so tick and migration exclusion hold across processes; no startup gate, deliberate-config presumption stands) without per-lock implementation detail.

- [ ] **`decisions/single-process-mode.md` Choice — enumerates startup call sequence (spec territory)**
  Quoted: "runs migrate synchronously, then starts all three roles (scheduler, supervisor, control-api) in-process via the existing library entry points, each on its configured port, with one signal-handled shutdown." Fix: name the choice (no-command entrypoint path runs all three roles in one process after migrate; single-role command preserves per-role-process behavior; unified-mode env marker set only in the unified path) without startup-sequence narration.

- [ ] **`decisions/peer-tls-enforcement.md` Choice — enumerates dial sites + per-mode behavior (spec territory)**
  Quoted: "every peer dial site honors the configured mode — the runtime's peer clients (store, publisher, data-processing, validation), the executor dial, and the observability-handshake dial: `required` dials with verified TLS against system roots; `off` (the default) stays plaintext; failures under `required` name the peer and the mode" Fix: name the choice (the `tls` key is writable on every peer entry kind and honored at every peer dial site with values `off | required`, default `off`); let a spec name per-site plumbing.

- [ ] **`decisions/hard-dep-settled-guard.md` Choice — enumerates dispatch-path call sequence (spec territory)**
  Quoted: "an upstream that already has a run row in the frame but no in-flight run is not re-affirmed on receiver re-visits — its value is already in the receiver's drained wait-set. The in-flight probe runs first, so a still-running or just-woken upstream falls through to the normal gate-insert path" Fix: name the choice (the hard-dep pull carries a settled-this-frame guard so independently-settled multi-hard-dep upstreams cannot mutually re-seed) without walking the dispatch path.

- [ ] **`decisions/message-sender-kind-discriminator.md` Choice — contains a separate decision's content**
  Quoted: "The idempotency dedup discriminator has its own three-value sender-kind enum that differs from the envelope's by one value — `operator` / `publisher` / `anonymous` (no `instance`, since instance-sender messages are blocked at the wire by the operator-or-publisher gate), where `anonymous` buckets anonymous-mode operator emits separately so the bootstrap admin's later emits don't dedup against the anonymous-floor emits that preceded the key mint. The two sender-kind enums are not the same enum and should not be conflated." Cross-cuts another decision (`message-idempotencies-dedup-tuple`). Fix: trim Choice to the envelope sender-kind enum and the sender-subject identity field; move the dedup-discriminator content to `decisions/message-idempotencies-dedup-tuple.md` (or delete from here).

- [ ] **`decisions/subscription-mounting-state.md` Choice — enumerates row-creation behavior (spec territory)**
  Quoted: "instance-create inserts subscription rows in `mounting` and returns; the instance-detail surface exposes per-subscription state" Fix: name the choice (publisher-subscription state set is `mounting | active | failed | stopped`, with `mounting` observable so contention does not require synchronous failure on instance-create) without naming the insert site and detail surface.

- [ ] **`decisions/upstream-gating-at-eligibility.md` Choice — enumerates retained mechanisms + scenarios (spec territory)**
  Quoted: "The wait-set ledger and its drained-rows substitution role are retained unchanged; self-edge ('drain my own queue') idioms and cycle handling keep working, pinned by scenario coverage of the deterministic diamond topology" Fix: name the choice (the all-upstreams guarantee is enforced at dispatch eligibility, propagation-path-independent) without naming the wait-set ledger, self-edge idioms, cycle handling, or specific test topologies.

### Decisions TOC entries (4)

- [ ] **`decisions.md:32 — TOC entry violates one-sentence rule + packs multiple clauses**
  Quoted: `` "`coding-style` — Rimsky's coding methodology is Plumbline; both lint checks active (`comment_hygiene`, `citation_resolution`) with GoDoc/JSDoc exemptions; PostToolUse + CI lint enforce." `` Fix: compress to one sentence summarizing chosen methodology and enforcement posture.

- [ ] **`decisions.md:66 — TOC entry enumerates entrypoint role-selection behavior in three clauses**
  Quoted: ``"`image-entrypoint-role-selection` — A single entrypoint binary that runs all three roles when given no command, a single role when the command names one, and runs migrate once per deployment with the owner role determined by the command."`` Fix: compress to one-sentence summary (a single command-dispatching entrypoint with migrate owned by one role).

- [ ] **`decisions.md:112 — TOC entry enumerates pipeline steps**
  Quoted: ``"`release-chain` — The shared release chain: lint, then license-lint, then core-images, then service-images, then test-all, then scan, then push-images."`` Fix: "Shared sequential release chain spanning lint, license-lint, image builds, full tests, scan, and push."

- [ ] **`decisions.md:116 — TOC entry enumerates template sections**
  Quoted: ``"`release-notes-template` — Template-driven sections (Breaking / What's new / Fixes / Internal / Image set / Go module / npm); every entry traces to a diff hunk."`` Fix: one sentence summarizing the choice (template-driven release notes with every entry traceable to a diff hunk).

---

## Stories (12)

- [ ] **`stories/operator-onboarding.md:14, :22, :26 — prescribes specific CLI verb (`run-template`)**
  Quoted: "copy a shipped example templatespec, drive it through the run-template CLI verb against an all-in-one stack" (14); "drives it through the run-template CLI verb against a running all-in-one stack" (22); "the run-template verb is a stub that prints a fake ID without driving register + deploy + instantiate" (26). Fix: name the capability generically ("the shipped one-shot instance-run entry point" / "the documented onboarding command"); move the literal verb name to a decision.

- [ ] **`stories/terminate-after-run.md:11, :14, :28-29, :37, :61 — prescribes specific CLI verbs (`run-template`, `watch`) + control-API field name**
  Quoted: "through the run-template CLI verb or an equivalent direct" (11); "the shipped watch verb" (14); "the control-API's instance-creation surface and the run-template CLI verb's terminate-after-run flag; the run-template verb's transient mode" (27-29); "drives the run-template verb and the watch verb, the watch loop exits" (37); "the run-template verb's terminate-after-run flag plumbs through but the control-API create body doesn't carry the field" (61). Fix: name the durable user expectation (operator opts an instance into self-termination at creation; a terminal-watching client exits cleanly) without verbs or API field plumbing; move details into a decision for the dev-loop CLI grammar.

- [ ] **`stories/event-log-read.md:22 — prescribes specific CLI verbs (`watch`, `logs`)**
  Quoted: "Through the control-api or the event-log CLI surface (watch / logs verbs)". Fix: "through the control-api or its CLI mirror"; move verb names to a decision.

- [ ] **`stories/lineage-admin.md:22 — prescribes specific CLI verb (`lineage-prune`)**
  Quoted: "an operator submits a prune request through the control-api or the lineage-prune CLI verb carrying a cutoff". Fix: "through the control-api or its CLI mirror"; move literal verb name to a decision.

- [ ] **`stories/validation-names-the-mode.md:14, :22 — names specific YAML config-key path (`templates.ref_validation_mode`)**
  Quoted (14): "names the `templates.ref_validation_mode` config key with its relaxed settings"; (22) "names the config key (with the relaxed settings) for register-first workflows". Fix: refer to "the reference-validation-mode config key" generically; keep the literal key in `decision:validation-error-names-mode`.

- [ ] **`stories/audit-log-read.md:14, :22 — names specific permission-token identifier (`audit:read`)**
  Quoted: "Audit-read surface gated on `audit:read`" (14); "Through the audit-read surface (gated by `audit:read`)" (22). Fix: refer to "the audit-read permission" without the literal token.

- [ ] **`stories/compose-namespace-guard.md:26 — names specific permission tokens (`tag:create`, `instance:create`)**
  Quoted: "A non-compose caller holding `tag:create` or `instance:create` succeeds at creating a `compose:`-prefixed resource". Fix: "a non-compose caller holding the tag-create or instance-create permission"; move literal tokens to a decision.

- [ ] **`stories/store-filesystem.md:22 — prescribes specific HTTP method ("POST to the admin sync route")**
  Quoted: "with `sync_strategy: explicit` and an empty queue, a POST to the admin sync route picks up a newly-dropped folder". Fix: "a request to the admin sync route" / "an admin sync trigger"; move wire shape to a decision.

- [ ] **`stories/http-node.md:22 — names specific HTTP response header (`Retry-After`)**
  Quoted: "a 429 response with `Retry-After` causes the node-run to enter `parked` with the corresponding resume time". Fix: "a 429 response with a retry-after directive"; keep literal header spelling in a decision.

- [ ] **`stories/producer-class-routing.md:22 — quotes specific error-class string identifiers (`pg/claim_unavailable`, `acquire/unavailable`)**
  Quoted: "A template with `error_types: { pg/claim_unavailable: retry }` on a node…"; "A template declaring only `acquire/unavailable:` still matches…". Fix: describe the shape ("a template routing a producer-declared acquisition error class to retry…"; "a template declaring only the generic acquire-fallback family…"); keep literal identifiers in the relevant decisions.

- [ ] **`stories/validation-warnings-surfaced.md:22 — names specific wire response field (`validation_warnings`) + flag spelling (`warnings_as_errors=true`)**
  Quoted: "returns the advisory in the response's `validation_warnings`; with `warnings_as_errors=true` the same advisory rejects the registration". Fix: describe the capability ("returns the advisory in the response's warnings collection; with the warnings-as-errors flag, the same advisory rejects the registration"); keep literal spellings in `decision:merge-validator-warnings`.

- [ ] **`stories/mcp-transport.md:22, :26 — prescribes literal "HTTP route" delivery surface + enumerates specific resource-route list**
  Quoted (22): "discovers a tool catalog covering the templates / tags / instances / nodes / messages / events / audit / breakpoints / assets / backfills / lineage / diagnostics / auth surfaces; invoking a tool mirrors the equivalent HTTP route — the same auth gate fires, the same observable state results, and the same response is returned through the MCP wire"; (26) "An MCP tool gate is weaker than the equivalent HTTP route's gate". Fix: express parity with the control-api surface generically ("the MCP catalog mirrors the control-api surface end-to-end"); move specific surface enumeration to a decision.

---

## Concepts (~90)

### `anonymous-mode.md`

- [ ] **`:27 — CLI-verb enumeration ("auth-init command")**
  Quoted: "running the auth-init command enables authentication" — violates no-implementation-enumeration. Fix: "running the bootstrap step" without naming the verb.

- [ ] **`:36 — CLI-verb + route + role-template enumeration**
  Quoted: "Operator runs the auth-init command. The CLI posts to the key-mint endpoint with the bundled `admin` role expansion; no bearer token." Fix: state the bootstrap sequence at concept altitude without literal verb/route/role names; move walkthrough to a decision if it must persist.

- [ ] **`:43 — CLI-verb enumeration**
  Quoted: "the auth-init command works again". Fix: drop verb name.

### `api-key.md`

- [ ] **`:12 — wire-format detail (prefix, encoding, byte count, algorithm)**
  Quoted: "Plaintext format: an `rk_` prefix followed by 44 base64url characters (33 bytes of CSPRNG entropy = 264 bits). The server stores only a SHA-256 hash of the plaintext" — violates no-implementation-enumeration. Fix: "a high-entropy CSPRNG-generated plaintext, stored only as a one-way hash"; move format/algorithm to a decision.

- [ ] **`:16 — external-standards enumeration**
  Quoted: "deployments that need richer identity (OIDC, SAML, mTLS) terminate that at their edge and inject API keys downstream". Fix: drop the parenthetical standards list.

- [ ] **`:20 — CLI-verb enumeration**
  Quoted: "the lifecycle verbs (mint / list / show / revoke / rotate / sweep)". Fix: "the lifecycle operations" without enumerating verbs.

- [ ] **`:24 — algorithm name**
  Quoted: "The server retains only the SHA-256 hash." Fix: "the server retains only a one-way hash."

- [ ] **`:31 — route + event-kind enumeration**
  Quoted: "**Mint** — the key-mint endpoint. CSPRNG plaintext minted; its SHA-256 hash stored; plaintext surfaced in the response and never persisted. Emits a key-created audit event." Fix: describe minting at concept altitude without route/algorithm/event-kind strings.

- [ ] **`:32 — route + event-kind + code-component enumeration**
  Quoted: "**Rotate** — the key-rotate endpoint with a grace duration…the rotation-grace sweep (a periodic scheduler job) then revokes it. Emits a key-rotated audit event, and later a key-revoked event with a rotation-grace reason." Fix: state rotation as a property without route, sweep mechanism, or event-kind strings.

- [ ] **`:33 — route + event-kind enumeration**
  Quoted: "**Revoke** — the key-revoke endpoint…Emits a key-revoked audit event with a manual reason." Fix: describe revocation without route or event-kind strings.

### `asset.md`

- [ ] **`:13 — code-component naming**
  Quoted: "The asset presentation surface is a query alias over the claim-handle ledger… The accessor lists an instance's claim handles…" Violates SELF-CONTAINMENT-RULE. Fix: describe as a derived view at concept altitude.

- [ ] **`:17 — route + CLI-verb + UI surface enumeration**
  Quoted: "the control-api asset endpoints (list, detail, versions, materialization-history, materialize, delete), the matching CLI asset subcommands, the dashboard asset-primary panel". Fix: "the control-api asset endpoint family and matching CLI subcommands"; drop verb-by-verb list and dashboard reference.

- [ ] **`:23 — template-field enumeration**
  Quoted: "Rimsky-aware fields outside `data:`: `producer`, `scope`, `lifetime`, `write_semantics`." Fix: speak of the rimsky-interpreted dimensions (producer identity, scope, lifetime, write-semantics) in prose without literal field-name list.

- [ ] **`:24 — route enumeration**
  Quoted: "The asset-delete endpoint releases the claim handle…" Fix: "Asset deletion releases the claim handle…"

- [ ] **`:25 — route enumeration**
  Quoted: "The asset-materialize endpoint is an alias for sending an invalidate-kind message…" Fix: "Asset materialization is an alias for sending an invalidate-kind message…"

### `atomic-staging.md`

- [ ] **`:22-29 — substrate-product enumeration in table**
  Table rows: "Postgres schema swap", "Iceberg branch fast-forward", "Filesystem directory atomic-rename", "S3 copy+delete", "Manifest pointer flip", "Kafka". Fix: describe atomicity envelopes by category (transactional swap, metadata-pointer fast-forward, filesystem rename, copy-window, manifest-write, append-only-log) without product names; move named-substrate matrix to a decision.

### `attribute.md`

- [ ] **`:19 — implementation-language + package name**
  Quoted: "The template-key name and Go-package name are both `attributes`." Fix: drop the sentence.

- [ ] **`:45 — code-component naming**
  Quoted: "Enforced at registration by the template validator's attribute-source check (rejects the declined forms)." Fix: "Enforced at registration: the declined forms are rejected by the template validator."

- [ ] **`:47 — code-component naming**
  Quoted: "Enforced by the template validator's attributes-schema check at registration and its effective-schema check at dispatch (the latter invoked from the dispatch-time attribute-resolution path)." Fix: state enforcement at concept altitude without naming the named checks.

- [ ] **`:51 — code-component naming**
  Quoted: "Enforced by the control-api override validator at registration and the runtime override applier at dispatch." Fix: state enforcement as a property without naming components.

- [ ] **`:55 — literal override-key path enumeration**
  Quoted: "instance-level overrides (`attribute_overrides.by_executor.<exec>.<attr>` or `attribute_overrides.by_node.<node>.<attr>`)". Fix: speak of by-executor and by-node overrides without dotted-key syntax.

- [ ] **`:71 — route + persistence-shape naming**
  Quoted: "Per-entry match counters persist on the instance row's match-count map. The supervisor increments after the merge returns, in a short dedicated transaction. Operators and tests read the counter from the instance-fetch endpoint…" Fix: state visibility property without naming the route or row.

### `backfill.md`

- [ ] **`:17 — route + CLI-verb enumeration**
  Quoted: "the control-api backfill endpoints (create, list, show, partitions, cancel), the matching CLI backfill subcommands". Fix: "the control-api backfill endpoint family and matching CLI subcommands" without verb list.

- [ ] **`:21 — HTTP status code + verb name**
  Quoted: "`backfill:create` **rejects (400)** a target that is not a fan-out node". Fix: "Backfill submission rejects a target that is not a fan-out node wired to accept the partition override" — no status code, no verb name.

- [ ] **`:23 — code-component naming**
  Quoted: "a cancelled filter on the pending-delivery path". Fix: "pending undelivered messages are skipped" without code-site name.

- [ ] **`:24 — route enumeration**
  Quoted: "the single-backfill fetch endpoint queries the message ledger and the node-run ledger". Fix: "Status rollup queries the message ledger and the node-run ledger…"

- [ ] **`:28 — operator-surface CLI/route enumeration (whole section)**
  Quoted: "The control-api offers five backfill operations on an instance: create a backfill (...), list recent backfills, fetch a single backfill (...), list its per-child partition states (...), and cancel an operation. The CLI exposes the same five as create/list/show/cancel subcommands, with create taking an instance, target node, range, and reason." Fix: rewrite at concept altitude ("operators can create, observe, and cancel backfills via the control-api and CLI surfaces") or drop the section.

### `blob-backend.md`

- [ ] **`:11 — implementation enumeration of impls**
  Quoted: "Four implementations: inline (default; spill disabled), Postgres large-object, filesystem, and an in-memory backend legal only in the single-process deployment mode." Fix: drop the impl list; describe interface + threshold; instances live in decisions/code.

- [ ] **`:11 — method-name enumeration**
  Quoted: "It exposes five methods (write, read, ranged read, delete, and a backend-name accessor)." Fix: describe operations without method names ("write, read, and delete operations over a name-tagged backend").

- [ ] **`:15 — impl enumeration repeated**
  Quoted: "a pluggable backend lets operators pick the storage shape (Postgres large-object, shared filesystem, etc.)". Fix: drop the parenthetical.

- [ ] **`:19 — Boundary references "four impls"**
  Quoted: "Owns: the abstraction, the four impls, the spill threshold, the orphan-blob ledger and sweep." Fix: "Owns: the abstraction, the implementations, the spill threshold, the orphan-blob ledger and sweep."

- [ ] **`:25 — impl enumeration in invariant**
  Quoted: "Handles are self-describing strings carrying a backend prefix (inline, Postgres large-object, filesystem, in-memory)". Fix: drop the parenthetical instance list.

### `breakpoint.md`

- [ ] **`:28 — transport-name enumeration**
  Quoted: "exposing **both** the read-only MCP resource-listing and resource-read extension and a read-only REST route that surface hits". Fix: "hit delivery is owned by `concept:control-api`; this concept owns the ledger, not the transport."

- [ ] **`:46 — code-implementation naming**
  Quoted: "Implemented by passing a populated set of used-executor names to the matcher validator." Fix: state property — `by_match` rejects executors not used by the template — without naming the data passed.

- [ ] **`:48 — code-implementation naming**
  Quoted: "Breakpoint matchers leave the used-executors set empty…" Fix: "breakpoint matchers don't perform the used-executor cross-check."

- [ ] **`:50 — code-component naming**
  Quoted: "This is enforced by the control-api breakpoint matcher-refs check." Fix: "These cross-checks are enforced at registration."

### `cancel-siblings.md`

- [ ] **`:13 — persistence-shape naming**
  Quoted: "Snapshotted on the parent claim-handle row at acquire-time (in a JSON aggregation-policy column)." Fix: drop the parenthetical.

- [ ] **`:13 — literal config-syntax**
  Quoted: "Declared on a fan-out parent's `error_policy: { strict: { cancel_siblings: true } }`." Fix: "Declared on the fan-out parent's error policy as a boolean field."

### `claim-handle.md`

- [ ] **`:35 — implementation-language naming**
  Quoted: "Revival from a terminal state back to active is not permitted at the Go layer." Fix: drop "at the Go layer."

- [ ] **`:40 — route enumeration**
  Quoted: "the operator-driven asset-release endpoint (for the asset Release path)". Fix: "via the operator-driven Release surface" without endpoint name.

- [ ] **`:71 — route enumeration**
  Quoted: "released only by explicit operator action (the asset-release endpoint)". Fix: drop endpoint name.

### `claim-lifetime.md`

- [ ] **`:14 — specific default value**
  Quoted: "the retention sweep reaps the row after the configured trailing window elapses (default 30d)". Fix: drop "(default 30d)"; let decision own the number.

- [ ] **`:14 — route enumeration**
  Quoted: "Released only by explicit operator action (the asset-release endpoint)". Fix: drop endpoint name.

### `claim-producer.md`

- [ ] **`:12 — reference-impl enumeration**
  Quoted: "Production-side reference implementations (filesystem, postgres) ship as standalone binaries on the consumption side, outside the platform; an in-rimsky stub carve-out stays as test infrastructure. The only in-rimsky concrete implementation of the claim-producer protocol is the gRPC peer client." Fix: drop the enumeration; describe the kind generally.

- [ ] **`:30 — specific bundled store named**
  Quoted: "The bundled SQL-based postgres store additionally registers the executor protocol to support verification of its own staged content; see concept:executor. The same binary plays both roles via separate gRPC service registrations on a single endpoint. Other SQL-substrate stores can use the same dual-role pattern." Fix: state the dual-role pattern generally without naming the specific bundled store.

### `claim-scope.md`

- [ ] **`:29 — specific producer named**
  Quoted: "The reference filesystem producer enforces this by requiring absolute concrete paths only." Fix: state requirement generally without naming the reference producer.

- [ ] **`:40 — specific producer instance**
  Quoted: "The standard filesystem producer is concrete-paths only (canonicalizes by requiring absolute paths so byte-equality holds)." Fix: drop the bullet; instance behavior belongs in producer code/docs.

### `conformance.md`

- [ ] **`:11 — code-layout reference**
  Quoted: "A `rimsky conformance <protocol>` subcommand family — one subcommand per protocol — over a shared conformance library in the protocols module (one sub-package per protocol)." Fix: drop the parenthetical layout description.

- [ ] **`:13-19 — subcommand-catalog enumeration**
  Quoted: "Executor conformance — exercises an executor against its execute RPC… Stub-mode probe… Claim-producer conformance over gRPC… Blob-backend conformance via in-process construction — six checks (round-trip 1KB, round-trip 10MB, range read, delete-then-read-returns-not-found, idempotent delete, concurrent writes). The subcommand adapts each concrete backend (memory / filesystem / pg-largeobject) to…" Fix: state generally ("one subcommand per protocol; each protocol's library defines its own scenario battery") without enumerating each instance and its scenarios.

- [ ] **`:21 — code-layout reference + specific binary**
  Quoted: "The conformance library lives in the protocols module; each subcommand is a thin wrapper (parse flags, dial endpoint, invoke library, format output, exit). The conformance surface ships inside the single rimsky binary." Fix: drop the layout claim and binary mention.

- [ ] **`:25 — library / language enumeration**
  Quoted: "The runner logic lives in an importable Go library, so Go service authors can also invoke the same suite from their own Go tests against an in-process or testcontainers-hosted target." Fix: state generally ("the runner logic is library-form so authors can invoke it directly from their tests without going through the subcommand").

- [ ] **`:29 — specific flag / entry-point naming**
  Quoted: "the lifecycle check flag on the executor conformance subcommand is the documented escape hatch, backed by a lifecycle-check entry point in the conformance library". Fix: state generally ("lifecycle-subscriber checks are surfaced as an opt-in within an adjacent protocol's conformance battery").

- [ ] **`:33-38 — invariants enumerate subcommands / flags / binaries / backends**
  Multiple invariants name specific subcommands, flags ("require-stub-mode flag"), binaries ("the rimsky binary"), and backend impls. Fix: lift each invariant to general form (stub-mode must be probed before scenarios; conformance surface is part of the all-targets build; lifecycle-subscriber has no dedicated surface; uniformity check skips for non-byte-equal producers; memory-backend process-role gate is bypassed under conformance).

### `control-api.md`

- [ ] **`:13 — route catalog enumeration**
  Quoted: "routed at bare, unversioned paths covering template registration, instance lifecycle (create, pause, resume), per-instance breakpoint management (set, delete, resume), the auth surface, observability reads, and admin diagnostics endpoints". Fix: state generally ("exposes the operator action surface at bare unversioned paths"); specific routes belong in implementation.

- [ ] **`:14 — wire-format / package enumeration**
  Quoted: "MCP (Model Context Protocol) — JSON-RPC 2.0 over HTTP at a dedicated MCP endpoint, served by an in-process MCP package". Fix: state property generally ("a Model Context Protocol skin sharing the same operation set"); drop JSON-RPC version + package layout.

- [ ] **`:20 — specific tool reference**
  Quoted: "HTTP+JSON is easier to script, expose through ingress, and inspect with curl during incidents than gRPC". Fix: replace `curl` with "an HTTP introspection client".

- [ ] **`:34-38 — `## MCP-as-skin` section names code packages**
  Quoted: "The MCP protocol skin is hosted in-process by a package under the control-api implementation… lives in the in-process MCP package." Fix: state the property ("the MCP skin runs in-process and dispatches tool invocations directly into the router") without referring to packages.

### `data-processing.md`

- [ ] **`:25 — substrate enumeration**
  Quoted: "Does NOT own: the substrate (Parquet, GeoParquet, PostGIS, Iceberg — producer-internal)". Fix: drop parenthetical; "the substrate (producer-internal)" suffices.

### `dry-run.md`

- [ ] **`:11 — literal URL-param syntax**
  Quoted: "resolves from EITHER the `?dry_run=true` control-plane request flag". Fix: state generally ("a request flag on the control-plane request") without literal syntax.

- [ ] **`:11 — quoted wire shape**
  Quoted: "returning a synthetic envelope of the form `{ dry_run: true, would_have_X: { ... } }`". Fix: state generally ("returning a synthetic response envelope flagged as preview") without literal JSON.

- [ ] **`:13 — implementation-plumbing reference**
  Quoted: "The auth middleware threads the resolved mode through the request context; handlers read it back from the context and gate the side-effectful path through a shared dry-run-response helper that emits the synthetic envelope." Fix: state generally ("the resolved mode is threaded through to every write handler, which gates its side-effectful path on it").

- [ ] **`:21 — action-name enumeration**
  Quoted: "`auth:create` / `auth:revoke` / `auth:rotate` are previewable like any other write." Fix: drop the example trio; "every write action including auth-surface writes" suffices.

- [ ] **`:25-29 — invariants enumerate actions / events / wire shapes**
  Quoted: "`?dry_run=true` on a `*:read` action", "For `template:register`, this includes firing the validation protocol's checks", "The middleware emits `auth.access_attempted` with `mode: dry_run`". Fix: lift to general form (flag is a no-op on read actions; validation runs faithfully on registration-time checks; audit row records the mode).

- [ ] **`:31-39 — `## Synthetic response shape` section is wire-spec**
  Quoted: "Each handler picks a verb that describes the intent. The synthetic envelope sets `dry_run` to `true` and carries a single `would_have_<verb>` key (`would_have_created`, `would_have_invalidated`, and so on)… holds a placeholder `instance_id` of `dry-run-not-persisted`…" Fix: delete the section; wire shape belongs in implementation/API docs.

- [ ] **`:39 — specific clients enumerated**
  Quoted: "Clients (CLI, MCP) check the top-level `dry_run` flag to render the response distinctly from a live invocation." Fix: state generally ("clients check the response's dry-run flag to render preview results distinctly") without naming CLI/MCP.

### `error-policy.md`

- [ ] **`:12 — specific config field**
  Quoted: "a per-node `max_retries_without_progress` setting or a supervisor-level default". Fix: state the cap generally; field name belongs in config docs.

- [ ] **`:14 — quoted wire shape**
  Quoted: "the runtime forces `Error { error_class: \"retry_loop_no_progress\" }`". Fix: state generally ("the runtime forces an Error with a reserved retry-loop-no-progress class") without literal syntax.

- [ ] **`:22 — three-name relationship cites implementation locus**
  Quoted: "**Implementation** — the runtime policy-chain resolver, entered from the terminal-error dispatch." Fix: drop the third bullet; concept-design distinguishes only design-noun vs operator-surface.

- [ ] **`:46 — specific defaults / config-field name**
  Quoted: "Per-node `max_retries_without_progress = 0` disables the cap; `nil` falls back to the supervisor default (100); `N > 0` uses N." Fix: state generally; default number lives in a decision file.

- [ ] **`:50 — wire/metric-label enumeration**
  Quoted: "the unknown-class default — `give_up(\"unknown_error_class\")`" and "A terminal-verdict metric tagged with `error_class=\"retry_loop_no_progress\"` increments when the cap forces a failure." Fix: state default action and metric existence generally without literal syntax.

### `event-log.md`

- [ ] **`:16 — build-mechanics reference**
  Quoted: "Adding a new operational kind = adding an enum value in the events proto + regenerating Go bindings (no schema migration; the storage column stays `TEXT`)." Fix: state at concept altitude ("operational kinds extend through the enum, not through schema migration") without naming the build mechanic.

- [ ] **`:24 — Go type name**
  Quoted: "operational kinds via the proto-declared `OperationalKind` enum (see `decision:event-log-kind-enum`)". Fix: state generally ("operational kinds via the typed enum at the app boundary"); keep the decision cross-reference.

- [ ] **`:24 — schema detail**
  Quoted: "The persistence column stays `TEXT` for marshaling flexibility — no `CHECK` constraint". Fix: state property generally ("the persistence column is an unconstrained string; the enum at the app boundary is the gate") without naming SQL types.

- [ ] **`:27 — specific event-name + payload fields**
  Quoted: "The per-request auth-audit write (`auth.access_attempted`) is synchronous in the request path — written inline after the handler returns (so `response_status` and `duration_ms` are known)". Fix: state generally without literal event-name or payload field names.

- [ ] **`:29-37 — `## Auth event kinds` section is wire-shape enumeration**
  Quoted: "Five `auth.*` event kinds capture the control-plane auth surface… `auth.access_attempted` — emitted by… Payload includes `key_id`, `key_name`, `identity_kind`, `protocol_skin` (`http` | `mcp`), `action`, `request_path`, `request_method`, `request_params` (verbatim), `response_status`, `mode` (`execute` | `dry_run`), `executed` (bool), `duration_ms`, `client_ip`, `user_agent`. … `auth.access_denied`… `auth.key_created`… `auth.key_revoked`… `auth.key_rotated`…" Fix: delete the section; the set of auth event kinds and their payloads belongs in implementation or a decision file.

### `executor.md`

- [ ] **`:11 — reference-impl enumeration**
  Quoted: "Production-side reference implementations (an HTTP-node executor, an LLM-agent executor, and two verifier executors) live on the consumption side, outside the platform. The stub test-double executor is the only in-rimsky implementation, used by conformance and the scenario harness." Fix: drop the enumeration; describe the concept generally.

- [ ] **`:11 — wire-format enum values**
  Quoted: "The park outcome carries an inner park reason from the closed two-value set `AWAIT_CALLBACK | SNOOZE` (`concept:parked-state`)." Fix: state generally ("the park outcome carries an inner reason from a closed two-value set — see `concept:parked-state` for the values").

- [ ] **`:21 — specific bundled store named**
  Quoted: "The bundled SQL-based reference store registers this protocol alongside `concept:claim-producer`. The same binary plays both roles via separate gRPC service registrations on a single endpoint. Other SQL-substrate stores can use the same pattern." Fix: state the dual-role pattern generally without naming the specific bundled store.

### `fan-out.md`

- [ ] **`:23 — quoted DSL syntax**
  Quoted: "the field is typically authored as a substitution directive (`{{trigger.message.payload.partition_request_override | <template-default>}}`)". Fix: state generally ("authored as a substitution directive that prefers the triggering message's override and falls back to a template default"); literal grammar lives in template-DSL docs.

- [ ] **`:23 — quoted DSL grammar**
  Quoted: "The fallback grammar is `<directive> | <literal>` (the literal being `null` / `true` / `false` / a number / a quoted string) — there is no `default:` keyword." Fix: drop this sentence; substitution grammar lives in template-DSL documentation.

### `frame.md`

- [ ] **`:14 — config-field name enumeration**
  Quoted: "per-instance `frame_delivery_mode` (`serial_queue` default, `coalesce` opt-in — owned by `concept:message`; distinct from this concept's own `frame_resolution_mode`)". Fix: replace literal field names with prose ("the message-side per-instance delivery-mode setting" / "this concept's own resolution-mode").

- [ ] **`:16 — schema spec + numeric defaults**
  Quoted: "The frame-resolution-mode is a required top-level string field on the template, with two valid values (`coalesce` / `serial_queue`); the template validator rejects empty or unknown values at registration. A companion frame-timeout field is optional with default 600000 ms (10 min) and a hard floor of 60000 ms (60 s)…" Fix: state generally ("template carries a required frame-resolution-mode and an optional frame-timeout with a default and hard floor enforced at registration"); numeric values belong in a decision file.

- [ ] **`:18 — algorithm + schema-layout naming**
  Quoted: "The frame-resolution-mode is JCS-canonicalized into the template's content-addressed spec at registration; it is *not* denormalized onto the instance row." Fix: state generally ("the mode lives canonically in the template spec rather than the instance"); the JCS choice lives in a decision.

- [ ] **`:39 — literal field syntax**
  Quoted: "`frame: in | next` per-emit discipline". Fix: state generally ("the per-emit in-vs-next attribution discipline").

- [ ] **`:48 — specific numeric values**
  Quoted: "Hard floor 60s; default 600s." Fix: drop the numbers; "configurable, with a hard floor and a default" suffices. Numbers belong in a decision.

- [ ] **`:48 — metric/event identifier**
  Quoted: "the scheduler emits a single `frame.stuck.observed` warning". Fix: state generally ("the scheduler emits a single stuck-frame warning").

### `host-agent-proxy.md`

- [ ] **`:11 — config-shape reference**
  Quoted: "Declared in the rimsky config (`concept:rimsky-yml`) once per protocol it serves — an entry under the executor block, one under the claim-producer block, and so on, all pointing at the same binary." Fix: state generally ("declared in the rimsky config under each service-protocol surface it fronts").

### `host-agent.md`

- [ ] **`:11 — CLI verb name**
  Quoted: "bundled into the rimsky CLI binary and invoked as the agent subcommand". Fix: "bundled into the rimsky CLI binary as a long-running daemon subcommand"; drop the specific verb name.

### `lifecycle-subscriber.md`

- [ ] **`:15 — specific bundled store**
  Quoted: "the bundled postgres store ships a no-op skeleton on the deployed callback — a documented fork-point an operator extends". Fix: replace "the bundled postgres store" with "the bundled stores" (or analogous general phrasing).

### `message.md`

- [ ] **`:22 — specific publisher name**
  Quoted: "identity of the sender (`operator`; publisher name like `sensor-cron`; `instance:<id>`)". Fix: replace `sensor-cron` with a generic placeholder.

- [ ] **`:32 — bundled-publisher key formulas**
  Quoted: "Bundled publishers generate keys per fire (cron: `{subscription_id}+{fire_window_iso}`; http: `{subscription_id}+{body_sha256}`; object-store: `{subscription_id}+{object_etag}`; webhook: `{subscription_id}+{idempotency_header_value}`)." Fix: drop the per-publisher enumeration; state the general property. Per-publisher key formulas belong in each publisher's spec.

### `module-layout.md`

- [ ] **`:18 — reference-impl named**
  Quoted: "copy-and-modify reference implementations of each rimsky-implementable protocol (executor, publisher, claim-producer, validation, data-processing, lifecycle-subscriber) plus the atomic-staging filesystem-producer reference". Fix: drop "plus the atomic-staging filesystem-producer reference"; existence of bundled-pattern references belongs in the services/decisions catalogs.

- [ ] **`:18 — bundled TS executor named**
  Quoted: "the bundled TypeScript executor reference". Fix: state the licensing carve-out generally; let the identity live in services/licensing decisions.

- [ ] **`:21 — MCP shim named**
  Quoted: "The operator MCP shim is part of the root module, NOT a separate Go module." Fix: drop the MCP-specific sentence or generalize ("operator-side protocol shims are part of the root module").

- [ ] **`:39 — Postgres named**
  Quoted: "A lint rule denies the Postgres driver outside an allow-list (which includes the test-support Postgres test-container helper and the migrations-applying test fixture so they can use it)." Fix: "a lint rule denies backend-specific persistence drivers outside an allow-list"; specific driver belongs in `decision:depguard-pgx-isolation`.

- [ ] **`:46 — build-tool property**
  Quoted: "the SQLite driver (pure-Go, no CGO)". Fix: drop the parenthetical; build property lives in `decision:sqlite-modernc-pure-go` and `decision:build-cgo-disabled`.

- [ ] **`:47 — specific backend named**
  Quoted: "cross-process state flows through Postgres only." Fix: replace "Postgres only" with "the persistence layer only" (or "the `concept:persistence-database` surface only").

### `named-event.md`

- [ ] **`:33 — blob-backend enumeration**
  Quoted: "Payloads can be spilled via the configured blob backend (one of inline, Postgres large-object, filesystem, or in-memory — see `concept:blob-backend`)." Fix: "Payloads can be spilled via the configured blob backend (see `concept:blob-backend`)."

### `orphan-reaper.md`

- [ ] **`:11 — sweep-function names enumerated**
  Quoted: "The runtime carries a family of sweep functions — stale-heartbeat, orphaned-node-run, ready, and orphaned-claim-handle sweeps." Fix: drop the enumeration; "the runtime sweeps both ledgers on a periodic cadence" (the cutoff and claimant-guarded delete are the load-bearing invariants).

### `permission.md`

- [ ] **`:27 — routes / MCP tool names named**
  Quoted: "Each action declares the HTTP routes and MCP tool names that map to it." Fix: "Each action declares the call-surface bindings that map to it" — without naming specific transports.

- [ ] **`:47 — MCP-specific naming in invariant**
  Quoted: "resolves MCP tool names → action → handler." Fix: "resolves tool-call surfaces → action → handler."

### `persistence-database.md`

- [ ] **`:33 — driver-name enumeration**
  Quoted: "the platform defaults to Postgres outside the all-in-one deployment, and an operator overriding to SQLite is presumed to have chosen deliberately." Fix: state defaults abstractly via the driver selector ("the platform default driver is the network-shared one; the local-file driver is the all-in-one default") or move specific-driver naming to `decision:persistence-dual-backend`.

- [ ] **`:35 — code-path allowlist read aloud**
  Quoted: "The raw-Postgres-driver isolation rule restricts direct driver use to the Postgres adapter, its test helpers, the binary entrypoints, the scenario harness, the bundled services, and the smoke-test harness — graph and control code go through the database interface." Fix: "raw driver use is restricted to the adapter layer; graph and control code go through the database interface" — defer to lint config.

### `replica.md`

- [ ] **`:27-33 — per-binary HA-posture enumeration**
  Quoted: "For every binary, the v1 contract documents its replica posture: The control-api binary … The supervisor binary … The scheduler binary … Bundled sensor binaries … Bundled executor binaries (the agent, HTTP-node, and verifier reference implementations) … Bundled store binaries (the filesystem and postgres reference implementations) …" Fix: drop the per-binary list; per-binary posture belongs in each binary's own concept. Leave `replica` with the design statement plus the abstract pattern.

- [ ] **`:32 — test-double named**
  Quoted: "The in-rimsky stub executor test double inherits the same posture for completeness." Fix: drop the sentence.

- [ ] **`:33 — test-double named**
  Quoted: "The in-rimsky stub store test double is single-process by construction." Fix: drop the sentence.

### `rimsky.md`

- [ ] **`:31 — CLI flag mechanics**
  Quoted: "The ephemeral-run verb resolves a template by either a positional file argument or a named-template flag (mutually exclusive). Params are supplied via a whole-params-blob flag and/or a repeatable per-entry flag (mixable, later-wins). A late-bound service binds a service name to a local binary path." Fix: state at concept altitude ("The ephemeral-run surface accepts a template (file or named), parameter values, and late-bound service bindings"); drop flag mechanics.

- [ ] **`:38-43 — capability-surface bullets enumerate CLI verbs**
  Quoted: "**Dev-loop surface** — … ephemeral runs, template register / deploy / undeploy, instance instantiate / remove, listing, logs, health, and operator-init. **Compose surface** — … compose-family verbs (up, down, plan, status, run) plus a dev shorthand. … **Authentication surface** — anonymous-mode bootstrap, login, key creation, listing, detail, revoke, rotate, and status … **Host-agent control surface** — start / status / stop verbs …" Fix: keep surface category + one sentence of intent per bullet; drop literal-verb lists.

### `role-template.md`

- [ ] **`:12-19 — bundled-role catalog enumeration**
  Quoted: "The six V1-bundled templates are: `admin` … `operator` … `read-only` … `agent-supervisor` … `publisher-service` … `debug-operator` …" Fix: drop the enumerated catalog; describe the role-template kind in general; move the V1 catalog to a decision (e.g. `decision:role-template-catalog`) or to the CLI's operator-facing reference.

### `sensor.md`

- [ ] **`:15 — bundled-sensor catalog**
  Quoted: "The bundled reference impls (the cron, HTTP, object-store, and webhook sensors) are sensors-by-construction; they share no protocol-level surface with rimsky beyond the Publisher protocol itself." Fix: drop the parenthetical; "bundled reference impls are sensors-by-construction; they share no protocol-level surface with rimsky beyond the Publisher protocol". Per-sensor catalog lives in `story:sensor-cron`, `story:sensor-http`, `story:sensor-object-store`, `story:sensor-webhook`.

### `service.md`

- [ ] **`:25 — CLI verb name in Boundaries**
  Quoted: "Conformance-validation entry points (the `rimsky conformance <protocol>` subcommand family in the single binary, not standalone per-protocol binaries; see `concept:conformance`)." Fix: "Conformance-validation entry points (see `concept:conformance`)"; drop the verb-shape clause.

- [ ] **`:32 — CLI verb name in invariant**
  Quoted: "Conformance is validated by the per-protocol `rimsky conformance <protocol>` subcommands shipped in the single binary (see `concept:conformance`)." Fix: "Conformance is validated per-protocol (see `concept:conformance`)."

---

## Notes / reasoning surface

Use this section to record judgment-call observations as you walk — anything you want to push back on (e.g. "the rule is too strict here", "this would damage the prose", "OpenLineage version is genuinely durable in the bundled-subscriber story"). I won't read these as instructions; they're for your own reference and for revisiting the canonical rules later if needed.

(write here)
