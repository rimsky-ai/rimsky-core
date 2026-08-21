# Concept-catalog alignment findings — 2026-08-20

Working record for `issue:concept-catalog-carries-non-definitional-content`. Five readers checked all 74 files under `.ok-planner/design/concepts/` against the concept rules in `.claude/skills/_shared/artifact-definitions.md` ({{CONCEPT-DEFINITION}}, {{CONCEPT-TEMPLATE}}, and the preamble's "interface designs, route shapes, CLI grammars, schemas, and implementation diagrams live in code"). Clause labels: instance-enumeration, interface/schema, guarantee (story content), choice-argument (decision content). Invariants counts are recorded but not findings: the template sanctions the section.

Totals: 48 of 74 files carry findings; 26 clean. Roughly 505 invariants across the catalog.

## Findings by file

- anonymous-mode — instance-enumeration: name `"anonymous"`, wildcard `*` permission (#11)
- api-key — instance-enumeration: "OIDC, SAML" edge-termination examples (#15)
- asset — instance-enumeration: presentation-surface operation list (#15); interface/schema: field list `name`, `selector`, `intent`, `alias`, `lifetime` (#21)
- attribute — interface/schema: in-grammar function non-list `{{coalesce}}`/`{{newest}}`/`{{merge}}` (#35); choice-argument: aggregation-lives-in-executors rationale (#35)
- breakpoint — interface/schema: MCP resource-listing + REST route exposure (#21)
- cascade-graph — interface/schema: read-only endpoint family, route definitions, JSON marshalling (#11, #19)
- claim-co-holdership — interface/schema: co-hold block keying and rename field (#11)
- claim — instance-enumeration: terminal verb list (#27)
- claim-handle — interface/schema: CHECK-constraint references (#43); instance-enumeration: producer commit verb (#16)
- claim-producer — instance-enumeration: verb lists begin/commit/abandon, list-versions/list-partitions/get-version-schema (#21, #40)
- claim-scope — interface/schema: column name `claim_scope_data`, lock kind, helper prefix (#39); choice-argument: byte-equal-conflict rationale vs richer semantics (#24)
- claim-tree — instance-enumeration: split-scope verb (#9)
- conformance — interface/schema: per-protocol CLI subcommand family (#9); instance-enumeration: nine scenarios, ten checks, backend list (#19, #33)
- data-processing — instance-enumeration: capability/lifecycle verb lists (#9, #19)
- discovery-cache — interface/schema: entry shape field list (#11)
- dry-run — guarantee: preview/validate promise stated as what an operator can do (#15)
- error-policy — instance-enumeration: four action literals retry/give_up/pass/release_and_requeue (#11)
- event-log — interface/schema: proto-enum + wire-string registration steps, index list (#11, #15); instance-enumeration: `auth.key_rotated` actor semantics (#28)
- executor — interface/schema: park-outcome field shape (#9); instance-enumeration: `async_ack_id` (#28)
- inertness — choice-argument: verbatim-audit-log policy rationale (#41-43)
- lifecycle-subscriber — instance-enumeration: lifecycle event list (#17)
- lineage — interface/schema: query-surface lookup keys, walk semantics, `truncated` flag (#26)
- message — interface/schema: receiver-node materialization row shape, idempotency-conflict response contract (#11, #15)
- module-layout — instance-enumeration: dependency and pinned-library lists (#14, #40); choice-argument: dual-licensing rationale (#47)
- node — instance-enumeration: utility-node examples (#30)
- node-run — interface/schema: seven-state machine table + transition diagram (#29-64); instance-enumeration: recovery-mode and reason literals (#13, #23)
- node-subscription — instance-enumeration: signal wire identifiers `terminal/success` etc. (#13)
- observability — interface/schema: protocol RPC method enumeration (#9)
- parked-state — choice-argument: post-frame-review idiom argued vs frame-blocking (#49)
- peer-auth — choice-argument: mTLS-vs-api-keys rationale (#24)
- permission — instance-enumeration: `service:enroll` verb (#22)
- persistence-database — interface/schema: container method list (#10); instance-enumeration: ledger table list (#14)
- publisher — instance-enumeration: peer-service examples (#17)
- publisher-subscription — guarantee: "rimsky guarantees the secret is never logged and never returned over any API surface" (#38); choice-argument: active+mounting acceptance rationale (#35)
- rimsky — interface/schema: params flag grammar (#34); instance-enumeration: capability-surface verb examples (#43)
- rimsky-yml — interface/schema: address-book entry field list (#29)
- role-template — instance-enumeration: six role-name literals (#11)
- run-scope — choice-argument: inline-disambiguator-drift rationale (#21)
- sensor — instance-enumeration: event-kind literal, publisherkit library (#35); interface/schema: three auth modes with algorithm detail (#36); choice-argument: retry-forever rejection rationale (#35)
- service — instance-enumeration: `tls` config key literal (#32)
- signal — interface/schema: CEL bindings and function library (#150-165)
- supervisor — interface/schema: three per-run routes with HTTP status contract (#11)
- terminal-resolution — interface/schema: terminal-kind → signal → verb wiring table (#26-36); instance-enumeration: Commit/Abandon/Release, Execute RPC variants (#22, #48)
- terminal-tag — instance-enumeration: `executor_protocol_violation` literal (#26)
- transition-reason — instance-enumeration: reason literals enumerated under the file's own rule that membership is owned by code (#9-11, #37)
- validation — interface/schema: request/response schema (#9-11)
- wait-set — interface/schema: persisted-row key composition stated as schema (#8, #26, #44)
- write-semantics — choice-argument: three-level structure argued vs two rejected shapes; reader-lease pattern forbidden vs named alternative (#18, #22-24, #36)

## Clean files

advisory-lock, atomic-staging, auto-terminal, blob-backend, cancel-siblings, cascade, cascade-mode, child-execution, claim-lifetime, control-api, delegation, fan-out, frame, graph, host-agent, host-agent-proxy, instance, lineage-record, message-schema, message-sender-node, named-lock, orphan-reaper, service-address-book, sub-graph, tag, template
