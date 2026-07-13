# Intent Dossier: permission

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Permission grants use the verb-noun action grammar `<noun>:<verb>` with exactly three wildcard forms (`*`, `<noun>:*`, `*:<verb>`; colon is part of the match boundary; no infix wildcards, no regex; invalid forms rejected at key-creation).
- Permission evaluation is set membership: a request is allowed iff any grant entry matches its action. First-match-wins was removed as a concept (2026-05-29).
- A grant entry may carry `mode: dry_run` (2026-06-06 reversal of 2026-05-29): effective mode is the floor of grant mode and request flag; the caller cannot escalate past the grant.
- Grant scope is enforced, not just parsed: a grant scoped to a resource selector allows only in-scope requests across the resource's full lifecycle; out-of-scope requests get 403 with an audit row attributing the refusal to scope (2026-06-06 un-deferral of the 2026-05-15 V2 deferral).
- One action grammar and one enforcement code path across both protocol skins (HTTP and MCP); the canonical action registry lives in code; auth middleware runs before every control-api handler with only /health and /ready exempt.
- Roles are CLI-side JSON expansion only; the server stores the raw grant and has no role concept.
- Direct node targeting is a debug feature only: there is no general operator verb to invalidate or re-run arbitrary nodes; node:invalidate and the operator-invalidate routes are retired.
- The debug channel is POST /instances/{id}/debug/override, gated to paused/breakpoint state, guarded by a dedicated permission action named `instance:debug-override`, auditing every use.

## Required behaviors (open promises)

- Auth middleware before every handler, both skins, only /health and /ready exempt; each endpoint declares its required action via the canonical registry (2026-05-15, control-plane-mcp-and-auth, artifact): "No control-api endpoint bypasses the auth middleware except /health and /ready."
- Wildcard grammar semantics: `<noun>:*` is a noun-prefix match that does NOT match a longer noun (auth:* matches auth:create, not authority:create); parser rejects invalid forms at key-creation (2026-05-15, artifact).
- Set-membership evaluation, no per-entry ordering semantics (2026-05-29, console-upstream-auth-audit, artifact): "a request is allowed iff any grant entry matches its action."
- Dry-run floor from identity: grant mode dry_run pins the key attempt-only, previews every write, commits none; floor of grant mode and request flag (2026-06-06, comprehensive-gap-closure, artifact — explicit reversal, and re-promised 2026-06-08 corpus-bootstrap).
- Scope enforcement across the full resource lifecycle (register, deploy, undeploy, deregister, tag set, tag delete, instance create), with the audit row attributing refusal to scope rather than action (2026-06-08, corpus-bootstrap, artifact; un-deferring 2026-05-15's V2 item).
- MCP parity: tools/list filtered by the requesting key's grant; tools/call re-evaluates through the same code path as the equivalent HTTP route; MCP tools auto-derived from the v1Actions registry and dispatching back through the chi router — MCP surface ≡ HTTP surface, audit rows differing only in protocol_skin (2026-05-15 + 2026-05-24 instance-debugger, artifact).
- Grant-entry parser is forward-compatible: unknown fields ignored (2026-05-15, artifact).
- Bundled role templates ship CLI-side: admin (`*`), operator, read-only (`*:read`), agent-supervisor, publisher-service (message:send only), plus debug-operator (`*:read` + the five debug write verbs, flagged high-risk); a minted key enforces exactly the expanded grant through the real gate (200 in-role / 403 out-of-role) (2026-05-15 + 2026-05-24 + 2026-06-02, artifact).
- agent-supervisor deliberately lacks breakpoint/pause mutation: read via `*:read` only, until an operator explicitly grants debug-operator (2026-05-24, instance-debugger, artifact).
- audit:read is a distinct action (separate from event:read because actor identity/IP/user-agent are sensitive) gating GET /audit over the auth.* event rows with filters and cursor pagination; joins read-only, operator, admin roles (2026-05-29, artifact).
- Observability read endpoints gated behind observability:read — no key yields 401/403 through the real gate (2026-06-02, acceptance-coverage-recovery, artifact).
- Debug override: POST /instances/{id}/debug/override with discriminated actions invalidate_node and set_attribute, applied synchronously in the request transaction, 409 when the paused-or-breakpoint gate fails, guarded by a dedicated permission action not granted on standard operator keys, emitting a debug.override.applied audit event; overrides do not persist beyond the running frame (2026-06-14, bfc9febb, transcript). Action name is `instance:debug-override` (two segments, hyphenated verb) (2026-06-15, 91ec93d1, transcript).
- compose: prefix on tag and instance-key namespaces is reserved for the compose machinery, enforced server-side: any non-compose caller is refused with a clear diagnostic even holding tag:create / instance:create (2026-06-08, corpus-bootstrap, artifact).
- Auth scenario tests under test/scenarios/auth/ covering bootstrap, grants/wildcards, dry-run, rotation grace, revoke-last-key guard, anonymous-mode transitions, MCP-vs-HTTP parity, and audit content (2026-05-15, artifact).
- Publisher-service keys are narrow message senders (`[{action: 'message:send'}]`); the messages handler validates publisher_subscription_id against caller identity as an in-handler capability check, not an auth-layer action (2026-05-15, artifact).

## Intentional absences

- node:invalidate action, the operator-invalidate routes (POST /v1/nodes/{id}/invalidate + admin sibling), their CLI subcommand, and the synthetic-envelope frame-creation path: retired entirely. Operators invalidate by posting a template-declared message; ad-hoc force-stale is debug-override only (2026-06-15, 91ec93d1, transcript; hardened 2026-06-15, 4c42fe5b, user: "re-running arbitrary nodes breaks the model. debug feature (with permission) only").
- First-match-wins grant evaluation and the per-entry mode-on-every-action model: removed 2026-05-29 (mode later returned only as the dry_run floor, 2026-06-06).
- The graduated-trust / preview-key-then-promote narrative: removed from dry-run's purpose (2026-05-29, artifact).
- A separate parked-node:wake action: wake is what node:invalidate did on a parked target (2026-05-15, artifact) — and node:invalidate itself is now retired; waking a parked node follows the message/debug paths.
- An mcp:invoke umbrella action or registry gating of the /mcp route itself: initialize and tools/list fall back to anonymous identity; the gate runs on the invoked tool (2026-05-15, artifact).
- Dry-runnable auth mutations in V1 (auth:create/revoke/rotate ignore mode) (2026-05-15, artifact) — later corpus entries treat auth:create dry-run as returning a synthetic envelope (2026-05-29); adjudicators should prefer the later position for the request-flag path.
- V1 declines: rate limits, confirmation gates, escalation channels, operator-defined server-side roles, MCP resources/prompts/subscriptions, MCP stdio transport (2026-05-15, artifact). (Resource scoping, originally on this list, was un-deferred 2026-06-06.)
- No separate sensor:observe / publisher:observe action (2026-05-15, artifact).

## Corrections and restorations (drift-fight record)

- The planned TestRegistryCoversRouter cross-check (every chi route has an action-registry entry) was never implemented — an acknowledged mechanical-enforcement gap against the auth-before-every-handler invariant (2026-05-15, plan-divergences, artifact). Still open per the record.
- Scoped keys silently over-granting platform-wide was ruled a real defect (not a doc problem); scope enforcement was un-deferred and promised with audit attribution (2026-06-06, artifact).
- A review-work cycle had kept the operator-invalidate route as "necessitated"; the user overruled and retired it entirely (2026-06-15, transcript).

## Superseded / historical

- Per-grant-entry mode (execute|dry_run) with first-match-wins (2026-05-15) → set membership, mode dropped (2026-05-29) → grant mode restored solely as a dry_run floor (2026-06-06).
- Request-flag-only dry-run ("the flag is the only source", 2026-05-29) → reversed by identity-bindable floor (2026-06-06).
- V2 deferral of action+resource scoping (2026-05-15) → un-deferred (2026-06-06).
- Spec working name instance:debug:override (three-segment) → instance:debug-override (2026-06-15, transcript).
- Legacy /admin/... route aliases sharing action gates pending a route-consolidation cleanup (2026-05-15) — the operator-invalidate portion of that surface is now retired outright (2026-06-15).
