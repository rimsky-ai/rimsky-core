# Implementation notes — public-documentation architecture

Plan: `docs/plans/2026-05-04-public-docs-architecture-plan.md`
Spec: `docs/specs/2026-05-04-public-docs-architecture-design.md`

This file is the durable record of deviations, judgment calls, and items the user should review after the run. Entries are appended by each dispatch of the implementer subagent.

Format:

```
## Task N — <title>

**Deviation:** <what differed from the plan>
**Reason:** <why>
**Surfaced for:** <user / future-work / nothing-action-needed>
```

---

## Task 4 — Subcommand routing initialization cycle

**Deviation:** Plan's `runAll` body referenced the `subcommands` table; declaring `runAll` inside that same table creates a Go initialization cycle. Split the iteration target into a separate `allLints` slice that lists the six lints by name + fn directly. The `subcommands` table still includes `runAll` for routing.
**Reason:** Plan's literal code does not compile; this is the smallest fix that preserves the plan's intent (single command-table for routing, runAll iterates the six concrete lints).
**Surfaced for:** nothing-action-needed.

## Task 9 / Task 16 — Extended public-anchor regex to match `service`

**Deviation:** Plan's `protoMessageRE` matches only `message|enum`. The three protocol concept files (`claim-producer.md`, `executor.md`, `lifecycle-subscriber.md`) cite the proto `service` symbol (`ClaimProducer`, `NodeExecutor`, `LifecycleSubscriber`), which is the natural cross-reference for the protocol layer. Extended the regex to `message|enum|service`.
**Reason:** The frontmatter field is named `proto_message` but the spec/plan tells the implementer to cite each protocol concept's service in the form `<Name> in protocols/proto/v1/<file>.proto` — the only matching top-level proto symbol is `service`. Without this fix, the lint rejects the three protocol concept files.
**Surfaced for:** nothing-action-needed (the lint is more permissive in a way the plan clearly intended).

## Task 22 — llms-txt-validity link resolution + corpus-dump skip

**Deviation:** Two extensions to the llms-txt-validity lint relative to the plan's literal Go code:
- Added a `validateLinks bool` parameter to `validateLLMSTxtShape`. The lint now skips per-link resolution for `llms-full.txt` because that file is a generated corpus dump from concept bodies — the `[name](other-name.md)` "See also" links inside concept bodies don't resolve from the concatenated file's location and aren't intended to.
- Extended link resolution from "file-dir + URL → repoRoot + URL" to also try "repoRoot + 'docs/' + URL". The plan tells implementers that `llms.txt` paths are docs-root-relative; without the third try, the default `-repo-root=.` flow fails for paths like `agents/examples/foo.md`.
**Reason:** Both changes are necessary for the lint to pass over the actual generated artifacts.
**Surfaced for:** nothing-action-needed.

## Task 22 — llms-full.txt description blockquote

**Deviation:** Added the `> ` prefix to the description line in `cmd/rimsky-docs-llms-full/main.go::generate`. The plan's literal generator code wrote the description as plain prose; the lint requires a `> <description>` blockquote near the top.
**Reason:** Without the `>` prefix, llms-txt-validity rejects the generated llms-full.txt.
**Surfaced for:** nothing-action-needed.

## Reviewer round 1 — `proto_message` → `proto_symbol` rename

**Deviation:** Renamed the frontmatter field `proto_message` to `proto_symbol` across 23 concept files, all lint code (`cmd/rimsky-docs-lint/frontmatter.go`, `public_anchor_validity.go`, `frontmatter_test.go`, `main.go`), the glossary code (`cmd/rimsky-docs-glossary/parse.go`), the test fixtures under `cmd/rimsky-docs-lint/testdata/` and `cmd/rimsky-docs-glossary/testdata/`, the spec, the plan, and `docs/vocabulary.md`.
**Reason:** Reviewer flagged that the field name `proto_message` lies — it now references services and enums in addition to messages (the three protocol concept files cite `service` symbols; `WriteSemantics` cites an enum). `proto_symbol` is the accurate name. The lint regex was already permissive (`message|enum|service`) per the prior plan-deviation note.
**Surfaced for:** nothing-action-needed.

## Reviewer round 1 — Concept files: real proto types and method shapes

**Deviation:** Replaced fictional types in concept and protocol files with the real proto shapes from `protocols/proto/v1/*.proto`. `ClaimResult` → `OpenResponse` (with `Acquired`/`Unavailable` oneof). `CapabilitiesResult` → `CapabilitiesResponse`. `ClaimVerbRequest`/`ClaimVerbAck` → the per-verb pairs (`CommitRequest`/`CommitResponse`, `AbandonRequest`/`AbandonResponse`, `ReleaseRequest`/`ReleaseResponse`). The executor concept and protocol guide now correctly split into the required `NodeExecutor` (one method, `Execute`) and the optional `ExecutorObservability` (`GetCapabilities`, `GetTrace`, `StreamTrace`).
**Reason:** Reviewer's correctness checks against the proto sources. External implementers copying the prior pseudocode would have written non-conforming services.
**Surfaced for:** nothing-action-needed.

## Reviewer round 1 — SQL-table scrub on public surface

**Deviation:** Removed all `rimsky_*` table-name references from concept files (`claim.md`, `claim-handle.md`, `claim-producer.md`, `holding-subgraph.md`, `named-lock.md`, `scope.md`, `template.md`) — both `layer_senses:` frontmatter and prose. Replaced with consumer-visible language ("the claim handle", "the per-run record", "the registered-template store"). Extended the vocabulary lint config (`docs/.vocabulary-lint.yml`) with a forbidden pattern `\brimsky_(claim_handle|worker_request|claim_holders|templates|nodes|template_tags|instances|schedules|lifecycle_idempotency|node_attributes|events)\b` so future edits cannot reintroduce SQL-table refs.
**Reason:** Spec §1, §13 forbids SQL tables on the public surface. The lint catches reintroduction.
**Surfaced for:** nothing-action-needed.

## Reviewer round 1 — Vocabulary lint: skip YAML frontmatter on .md files

**Deviation:** Extended the vocabulary lint scanner to skip lines between the leading `---` and trailing `---` of YAML frontmatter on `.md` files. Removed the in-frontmatter HTML-comment ignores from `concepts/template.md` and `concepts/instance.md` (they relied on the prior fragile mid-frontmatter suppression). Added a regression test `TestVocabulary_FrontmatterSkipped` and a fixture under `testdata/vocabulary-frontmatter-skip/`.
**Reason:** The `deprecated_terms:` frontmatter list is the official declaration site for deprecated vocabulary; the lint should not also re-scan it as prose. The HTML-comment-ignore pattern inside YAML was fragile.
**Surfaced for:** nothing-action-needed.

## Reviewer round 1 — `region` added to vocabulary lint

**Deviation:** Added `\bregion\b` (scoped to public-surface `.md` and `.txt` files) to `docs/.vocabulary-lint.yml`. The concept-file frontmatter for `scope.md` already lists `region` in `deprecated_terms`; the lint catches in-prose drift back to the older word. Updated `docs/protocols/claim-producer.md` table column "Regional `r`" / "Regional `rw`" to "Scoped-access `r`" / "Scoped-access `rw`" so the prose stays consistent.
**Reason:** Spec §7.3 says additional forbidden terms get added when surfaced. The reviewer surfaced `region`.
**Surfaced for:** nothing-action-needed.

## Reviewer round 1 — Error-file frontmatter shape lint

**Deviation:** Extended the `frontmatter` subcommand of `rimsky-docs-lint` to also validate files under `docs/agents/errors/` with a separate `errorFM` shape (`error: <code>`, `surfaced_to: <one of allowlist>`). The allowlist is spec §8.4 verbatim: `executor | claim-producer | lifecycle-subscriber | operator | cli-user`. Five existing error files used the unlisted value `caller`; rewrote them to `cli-user` (these are CLI/control-api caller-facing errors).
**Reason:** Reviewer pointed out that `surfaced_to: caller` was undocumented and the lint wasn't catching it. Extending the existing `frontmatter` subcommand keeps the lint surface flat.
**Surfaced for:** nothing-action-needed.

## Reviewer round 1 — Example CLI commands realigned with the actual CLI surface

**Deviation:** Rewrote every example file under `docs/agents/examples/` and the `concepts/template.md` / `concepts/tag.md` "How you encounter it" sections to use the real CLI verbs and flag shapes:
- `template register --file X` → `template register X` (positional)
- `template deploy --hash sha256-...` → `template deploy sha256-...` (positional)
- `instance create --template <ref> --param key=val` → `instance create <ref> --params '{"key":"val"}'`
- `compose apply --file rimsky-compose.yml` → `compose up -f rimsky-compose.yml`
- `template deregister` → `template rm`
- `tag move`/`tag delete` → `tag mv`/`tag rm`

Source of truth: `cmd/rimsky-cli/main.go`, `modeling/cli/templates.go`, `modeling/cli/instances.go`, `modeling/cli/compose/cmd.go`.
**Reason:** Spec §8.5 requires examples to be runnable verbatim.
**Surfaced for:** nothing-action-needed.

## Reviewer round 1 — claude-agent-userdata example: trace endpoint replacement

**Deviation:** Replaced the `GET /worker_requests/{wr_id}/trace` references (no such control-api route exists; only `GET /events?...` is registered in `modeling/controlapi/`) with `GET /events?instance_id=<id>` and a working `jq` filter that asserts the `attributes_substituted` event's `substituted_fields` list does not include any userdata-derived fields. Updated `concepts/userdata.md` similarly. The substantive verification — that Rimsky did not substitute `userdata` — is now expressed as "the `attributes_substituted` event lists only schema-source fields, and `userdata` isn't one of them."
**Reason:** The original example referenced a non-existent endpoint and a non-existent `userdata` field on `WorkStartedPayload`. The events log is the actual observable artifact for Rimsky-side substitution behavior.
**Surfaced for:** nothing-action-needed.

## Reviewer round 1 — frame.md / frame-resolution.md / lifecycle-subscriber.md: corrected `config_field`

**Deviation:** `frame.md` and `frame-resolution.md` had `config_field: rimsky.yml:frame_resolution`, but `frame_resolution:` is a template-level declaration, not a `rimsky.yml` block. Changed both to `config_field: (none)`. `lifecycle-subscriber.md` had `config_field: rimsky.yml:claim_producers`, but lifecycle is opt-in via `protocols:` on any peer (claim-producer or executor). Changed to `config_field: (none)`.
**Reason:** The frontmatter anchor was misleading; the prose in each file was correct.
**Surfaced for:** nothing-action-needed.

## Reviewer round 1 — node-state.md genesis-case reconciliation

**Deviation:** Added an explicit genesis-case paragraph after the bit-truth table in `concepts/node-state.md` and rewrote the matching "Common mistakes" bullet so the bit table and the prose agree. The genesis exception: a freshly-created node carries the named state `fresh` but has no value yet; once it runs at least once and emits a value, the bit semantics align with the table for the rest of the node's lifecycle.
**Reason:** The reviewer caught an internal contradiction between the bit table (`fresh = has_value=true`) and the prose ("nodes start their lifecycle in `fresh` (with no value yet)").
**Surfaced for:** nothing-action-needed.

## Reviewer round 1 — Makefile: docs-roots dep on docs-glossary

**Deviation:** Changed `docs-roots: docs-llms-full` to `docs-roots: docs-llms-full docs-glossary` so running just `make docs-roots` regenerates everything before copying the repo-root mirrors. `docs-build` now reduces to `docs-roots` (kept as a user-facing alias for symmetry).
**Reason:** Reviewer noted the prior dep chain meant `make docs-roots` could copy stale generated files.
**Surfaced for:** nothing-action-needed.

## Reviewer round 2 — Example YAML bodies realigned with the real template DSL

**Deviation:** Reviewer round 1 entry "Example CLI commands realigned with the actual CLI surface" was overstated — it realigned the CLI verbs and flag shapes but did NOT realign the YAML template bodies the commands consume. Round 2 closes that gap. Every example file under `docs/agents/examples/` now uses the real `TemplateSpec` / `TemplateNodeDef` / `NodeStoreRef` / `NodeAttributesDef` / `InheritEntry` shapes from `modeling/node/template.go`:
- No `template:` wrapper; top-level fields directly (`name`, `version`, `frame_resolution`, `nodes`).
- `nodes:` is a list of entries with `type:`; not a map keyed by name.
- `dependencies:` (not `deps:`).
- `stores:` (not `claims:`); each entry has `name:` (not `producer:`), `selector:`, `intent:`, optional `alias:`.
- `attributes: { schema: { type: object, properties: ... } }` (not `attributes: { properties: ... }`).
- `userdata:` is a YAML map (not a string).
- `inherits: [{ claim: <alias> }]` (not `inherits: [<alias>]`).
- `frame_resolution:` is a required scalar field; no implicit default.

The `rimsky-compose-multi-template.md` manifest body was also rewritten against the real `Manifest` / `TemplateRef` / `InstanceRef` shapes from `modeling/cli/compose/manifest.go` (templates and instances are lists, not maps; `path:`/`tag:`/`state:` and `template:`/`name:`/`params:`/`restart:` field names; no top-level `tags:` block).
**Reason:** Reviewer round 2 surfaced the YAML-shape issues. Spec §8.5 mandates copy-pasteable examples; previous shape would error on `rimsky-cli template register`.
**Surfaced for:** nothing-action-needed.

## Reviewer round 2 — Example verifications reframed to match the stub's actual behavior

**Deviation:** The bundled `executor-stub` runs in stub mode (`RIMSKY_EXECUTOR_STUB_MODE=1`) and keys behavior on `node_type` only — it ignores `userdata` for behavior selection. Several example files asserted behavior the stub doesn't drive (`stub_complete_success`, `stub_errored_give_up`, `stub_capture_address` userdata strings). Reframed:
- `holding-subgraph.md` now demonstrates the all-success → `Commit` path (the path the stub actually drives), with a note that exercising the any-failure → `Abandon` path requires an executor that can drive the desired outcome (e.g. `claude-agent` or a programmatically-scripted stub used by scenario tests).
- `two-node-with-claim.md` likewise demonstrates the all-success path (no `userdata` lever required).
- `claude-agent-userdata.md` keeps its purpose — proving Rimsky never inspects `userdata` — but the verification now observes the `attributes_substituted` event (which is the actual observable artifact for Rimsky-side substitution behavior). The example uses `executor: stub` rather than `claude-agent` because the verification is purely about Rimsky-side behavior, not about the executor's parsing of `userdata`.
- The 404 claim for an empty claim-handle holders list was wrong — the route returns `200 OK` with `{"holders": []}`. Fixed.
**Reason:** Reviewer round 2 caught these. Spec §8.5: "Complete, copy-pasteable, no-ellipsis examples."
**Surfaced for:** nothing-action-needed.

## Reviewer round 2 — Vocabulary lint extended for SQL-implementation language and `regional` derivation

**Deviation:** Extended `docs/.vocabulary-lint.yml`:
- The `\bregion\b` pattern was widened to `\bregion(s|al|ally)?\b` to catch `regional` / `regions` / `regionally`. The standalone `regional` in the claim-producer guide's reference-impl list slipped through round 1.
- New patterns scoped to public-surface markdown: `\b(BYTEA|BLOB)\b`, `\bON DELETE\b`, `\bSQL predicate\b`, `\bscope_data\b`, `\btemplate_hash TEXT\b`. Each rejects SQL-implementation language that betrays the persistence layer (spec §1, §4 forbid SQL-table references on the public surface).
- Concept files realigned: `frame.md` ("SQL predicate" → consumer-visible description), `frame.md` (`worker-request row` → `dispatched run`), `holding-subgraph.md` (`ON DELETE SET NULL foreign key` → consumer-visible lifetime guarantee), `instance.md` (`template_hash TEXT` SQL-shape → `template_hash` field name only), `scope.md` (`BYTEA`/`BLOB` and `scope_data` → "opaque bytes" / "byte sequence" / "scope bytes").
**Reason:** Reviewer round 2 catalogued the SQL-implementation leaks. Round 1's lint scope didn't catch column-shape, type, or predicate vocabulary.
**Surfaced for:** nothing-action-needed.

## Reviewer round 2 — `Errored` and `Complete` event descriptions realigned with the proto

**Deviation:** `docs/protocols/executor.md` previously said the executor "carries the named error action: retry, invalidate(targets), or give_up" on `Errored`. The proto carries only `error_class` (string) and `payload` (Struct); the supervisor's policy chain in the template (`modeling/node/policy.go::Evaluate`) maps `(error_class, retry_counter)` to one of `retry` / `discard_then_retry` / `resume_then_retry` / `invalidate(targets)` / `give_up`. Reworded to make the executor-vs-supervisor split explicit and enumerate all five resolutions. Also enumerated the three `Complete` fields (`changed`, `change_summary`, `attributes_delta`) and noted when `attributes_delta` may be empty (incremental-callback path). Concept files (`cascade.md`, `invalidate.md`, `recalculate.md`) updated in lockstep — the executor reports `error_class`; the template's policy chain decides the action.
**Reason:** Reviewer round 2 caught the wire-vs-doc mismatch. External implementers copying the prior pseudocode would have written non-conforming services.
**Surfaced for:** nothing-action-needed.

## Reviewer round 2 — Outbound link from public surface inlined

**Deviation:** `docs/agents/examples/minimal-rimsky-yml.md` previously linked to `../../../deploy/rimsky.yml`, which reaches outside the public surface (spec §1: "cites within itself, into proto files, and into source code only via concept-file frontmatter for the proto-message anchor"). The relevant rimsky.yml content is now inlined into the example body. `docs/protocols/lifecycle-subscriber.md`'s "look at how the bundled producers register their handlers" sentence was replaced with concrete YAML boilerplate showing the lifecycle-subscriber opt-in shape.
**Reason:** Spec §1 forbids outbound links into source-tree files.
**Surfaced for:** nothing-action-needed.

## Reviewer round 2 — CLI human output keys consistent across `template register` / `template get` / `tag get`

**Deviation:** Round 1 changed `RunTemplateRegister`'s human output to `template_hash`. Round 2 catches the two remaining places that emit human-readable template references: `tags.go::RunTagGet` (was `template_id`) and `templates.go::RunTemplateGet` (was `id`). Both now emit `template_hash` so all three CLI verbs are consistent and align with the deprecated-vocabulary declaration that `template_id` is replaced by `template_hash`.
**Reason:** "Fix Every Bug You Find" — a partial scrub is itself a bug.
**Surfaced for:** nothing-action-needed.

## Reviewer round 2 — `nodes:` described as a list (not a map keyed by name) in concept prose

**Deviation:** `concepts/node.md` previously called a node "a named entry in a template's `nodes:` block" — phrasing that implies `nodes:` is a map keyed by node name (matching the fictional DSL the example YAML bodies embodied). The real shape is a list of entries each carrying `type:`. Updated the prose in node.md to "an entry in the template's `nodes:` list with a `type:` (the node's name within the template), `dependencies:`, ...".
**Reason:** Reviewer round 2 surfaced the conceptual-vs-actual mismatch.
**Surfaced for:** nothing-action-needed.

## Post-implementation revision — public-vocabulary cleanup

**Deviation:** After the implementation + two cleanup rounds landed, the user identified that several "public presentations" the original plan/spec preserved were either factually wrong or more complex than the underlying reality. We revisited the modeling-over-foundation presentations and shipped a cleanup pass. Per-decision summary below.

### Message vocabulary: collapsed two → one

The original surface presented two messages, `invalidate` and `recalculate`. The foundation contract (`docs/specs/2026-05-04-foundation-contract.md`) actually defines exactly one cascade signal (`invalidate`); recalculation is a per-node action of the scheduler — not a graph-level message. The "two messages" framing forced readers to understand a symmetry that doesn't exist (and the modeling-layer contract itself acknowledged this internally).

Result:
- `concepts/recalculate.md` retired.
- `concepts/cascade.md` and `concepts/invalidate.md` rewritten: one message; recalculation framed as "what the scheduler does next" in prose.
- "Recalculate" survives as a verb describing what the scheduler does to a stale node — the word is not banned, just no longer presented as a peer message.
- `docs/vocabulary.md` deprecated-terms table gained an entry: "`recalculate` (as a graph-level message) → `invalidate` plus the scheduler verb 'recalculate'."

### State vocabulary: kept four-name presentation

`fresh`/`stale`/`running`/`failed` over the `(has_value, has_outstanding_request, auto_recovers)` triple is the right level for operators. Kept. `concepts/node-state.md` already documented the genesis case (`fresh`-without-value at init); minor wording cleanup only — the foundation-internal triple is mentioned but not framed as a separate "layer."

### Error-action vocabulary: expanded three → five

The supervisor's policy chain (`modeling/node/policy.go::Evaluate`) resolves to five actions: `retry`, `discard_then_retry`, `resume_then_retry`, `invalidate(targets)`, `give_up`. The "three actions" framing in the original prose forced operators to reconcile what they saw in error logs (e.g. `discard_then_retry`) against a vocabulary that didn't include those names. Expanded throughout: `concepts/cascade.md`, `concepts/invalidate.md`, `concepts/node-state.md`, `docs/protocols/executor.md`, `docs/humans/dashboard.md`. (The protocol guide already listed all five from round-2; this pass aligned the rest of the surface.)

### Four-layer model: retired from the public surface

The four-layer organization (foundation / modeling / service-protocols / bundled-services) is genuinely useful for engineers reading the codebase. But it is implementation detail for everyone else: an external consumer using Rimsky to build agentic workflows does not need to know which Go module owns which interface to use the system. Forcing it as the leading concept on `humans/landing.md` and the meta-frame in `humans/concepts.md` was making the surface harder to read, not easier.

Result:
- `concepts/four-layer-model.md` retired.
- `concepts/README.md` no longer points at it.
- `humans/landing.md` and `humans/concepts.md` rewritten without the four-layer lead. The first thing a reader now sees on `landing.md` is the one-paragraph "what it is": a graph of nodes; `invalidate` cascades through it; the scheduler recalculates eligible stale nodes; coordination via claims and named locks.
- `docs/agents/llms.txt` no longer lists the four-layer-model bullet.
- `README.md` overview rewritten to describe the system in plain terms; the module-organization listing is preserved but framed as implementation detail.

### `layer_senses:` frontmatter + "Layer senses" prose sections: retired

These sections were the structural cousin of the four-layer-model. With the four-layer-model gone, most of the "Layer senses" sections were just "the modeling layer says X" (which is the same thing the rest of the file was already saying). Where they had useful content (e.g. claim-producer.md's distinction between the protocol-level term "claim producer" and the bundled-services-layer colloquialism "store"), the content moved into a normal prose section.

Result:
- `layer_senses:` frontmatter dropped from `attributes.md`, `claim.md`, `claim-handle.md`, `claim-producer.md`, `executor.md`, `frame.md`, `holding-subgraph.md`, `inheritance.md`, `instance.md`, `lifecycle-subscriber.md`, `named-lock.md`, `node.md`, `node-state.md`, `scope.md`, `template.md`, `userdata.md`.
- "Layer senses" prose sections deleted; useful content folded into "Why it exists" or "How you encounter it" without invoking layer terminology.
- `claim-producer.md` gained a "Naming: claim producer vs store" section that explains the two terms without invoking the four-layer-model.

### Frame resolution merged into frame.md

`frame_resolution:` is a single template-level scalar field with two values; it doesn't carry enough conceptual mass to warrant its own concept file. The two values (`serial_queue` / `coalesce`) and their semantics now live as a section inside `frame.md`.

Result:
- `concepts/frame-resolution.md` retired.
- `concepts/frame.md` absorbed the content as a "Frame resolution: `serial_queue` vs `coalesce`" section.
- All citations updated.

### Auto-terminal: not named on the public surface

The internal mechanism name "auto-terminal" appeared in several public-surface sections describing held-claim resolution. The mechanism is real and load-bearing; the *name* is implementation detail. Reframed in plain terms ("Rimsky fires exactly one automatic resolution at holding-subgraph completion") in `concepts/holding-subgraph.md`, `concepts/claim-handle.md`, `docs/protocols/claim-producer.md`, `docs/humans/dashboard.md`, and the holding-subgraph example.

### `changed: bool` left as a section under cascade

Considered demoting to a single sentence; left as a section ("The `changed` halt-at-this-node signal") in `concepts/cascade.md` because operators reading "why didn't my downstream node fire?" need a discoverable place to read about it.

### Final concept count: 20 (was 23 originally)

Retired: `four-layer-model.md`, `recalculate.md`, `frame-resolution.md`. The remaining 20 are organized for use, not for layering.

### Spec / plan updates

`docs/specs/2026-05-04-public-docs-architecture-design.md` gained a "Post-implementation revision" addendum at the top; the body is preserved as the original design record. The plan was not edited beyond what the round-1/round-2 fixers had already done — the plan describes the original 28-task execution and is now historical.

### Verification

`make docs-build` (idempotent — no diff on re-run); `make docs-lint` (all six lints OK); `go build ./...`; targeted `go test` on `cmd/...` and `modeling/cli/...`; `make lint`; `make license-lint` (0 violations).

**Reason:** User direction during the post-implementation review: "the public-facing 'two messages' is factually wrong and actually more complex than the reality"; "the four-layer model: who cares? that is a great architectural framing for development, but irrelevant to end users, human and agentic"; "yes, all five [error actions]. genuinely useful to know." The cleanup brings the public surface in line with what the system actually does.
**Surfaced for:** nothing-action-needed (the public surface is now consistent with the foundation contract and the actual policy chain).
