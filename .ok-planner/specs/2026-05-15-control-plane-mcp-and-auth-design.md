# Control-plane MCP and auth

**Date:** 2026-05-15
**Status:** spec
**Supersedes (within rimsky's evolution):** Parts 1, 6 of `sketch:2026-05-14-agentic-platform`. The sketch's Parts 2 (knowledge store), 4 (executor-level MCP convention), 5 (supervisor templates) are deferred — see "What this does NOT cover" below. Sketch's Part 3 (`ValidateClaimantUserdata`) is superseded by the `Validation` cross-cutting mix-in in `spec:2026-05-15-data-platform-extensions-design`.

## What this covers

The first-class control-plane auth surface and MCP protocol skin for rimsky's control-api. Three deliverables, designed to compose:

1. **API-key authentication** with rimsky-owned permission grants per key, bootstrappable via an implicit anonymous mode that ends the moment the first key is minted.
2. **MCP as a first-class control-api protocol skin** alongside HTTP+JSON, hosted by `rimsky-control-api` directly. Tools-only V1; permission-filtered tool catalog; one action grammar shared across protocol skins so a single enforcement code path applies to both.
3. **Audit and dry-run** as cross-cutting discipline: every authenticated request lands in the event log with structured fields; write actions support a per-key `mode: dry_run` modifier that runs validation without mutating state.

The composition is load-bearing. Auth without MCP doesn't enable agentic supervision; MCP without auth doesn't earn its place beside operator HTTP; dry-run without auth has no per-identity targeting to attach to.

## What this does NOT cover

- **Rate limits, confirmation gates, escalation channels.** Deferred to V2. Documented as patterns that compose with V1 primitives but do not ship as platform surface here.
- **Action+resource scoping** ("this key can invalidate nodes only in instances of template-tag `analytics`"). V1 is action-only with prefix/suffix wildcards. Scoping deferred to V2.
- **Operator-defined roles via control-api.** V1 ships compiled-in role templates in the CLI only. Server has no role concept; advanced operators fork the bundled JSONs locally.
- **MCP resources, prompts, push notifications / subscriptions.** Tools-only V1; other MCP surface types deferred.
- **MCP stdio transport.** V1 is HTTP-transport only; MCP shares control-api's HTTP listener. Stdio would require a separate forwarder process; deferred.
- **External identity providers** (OIDC, SAML, JWT validation, mTLS termination). Out of scope by design — deployment layer handles identity provisioning; rimsky's surface is the API-key floor. Operators wanting enterprise IdP integration terminate the IdP at their edge and inject API keys downstream.
- **`sensor-rimsky-lifecycle` and supervisor templates.** Per the per-project stance: consumer-side patterns built from primitives. Out of scope.
- **Knowledge store, geo bundling, executor-level MCP convention.** Per the per-project stance.
- **Consolidating `cmd:rimsky-migrate` and the conformance binaries under `cmd:rimsky`.** Natural follow-up cleanup; not in this spec.
- **Rate-limit state model** (when V2 lands, will need to decide between in-memory per-replica, DB-backed, or external store). The grant entry shape is forward-compatible (parser ignores unknown fields) so adding a `rate_limit` field later doesn't require a schema migration.

## Vocabulary

### New nouns

- **API key** — high-entropy random token rimsky issues, formatted `rk_<44-char-base64url>`, surfaced once at mint (and once per rotation), hashed (SHA-256) for storage in `rimsky_api_keys`. Operators present it via `Authorization: Bearer <key>`.
- **Permission grant** — JSONB list of entries on an API key. Each entry is `{ action: <string>, mode?: "execute" | "dry_run" }`.
- **Action** — verb-noun string identifying one logical operation, e.g. `instance:create`, `node:read`, `message:send`, `template:register`. Each action maps to at most one HTTP route family and at most one MCP tool.
- **Wildcard** — `*` matches any action; `*:read` matches actions with the `:read` verb; `instance:*` matches actions with the `instance:` noun. No infix wildcards; no regex.
- **Mode** — per-grant-entry modifier; `execute` (default) or `dry_run`. Only meaningful for write actions; ignored on read actions.
- **Role template** — CLI-bundled JSON resource (e.g. `admin.json`, `operator.json`, `read-only.json`, `agent-supervisor.json`, `sensor-service.json`) that expands into a permission grant at key-creation time. Server doesn't know about roles.
- **Anonymous mode** — derived deployment state where `rimsky_api_keys` has zero active rows. Every request gets a synthetic admin identity; audit records reflect this; loud startup warnings.

### Updated nouns

- **`concept:control-api`** — now serves two protocol skins on the same operations: HTTP+JSON (existing) and MCP (new). The standalone module under `mcp-servers/control-api/` folds into `control/controlapi/` as a package. Concept doc updates accordingly.
- **`concept:event-log`** — gains `auth.*` event kinds. Schema unchanged; new kinds layer onto the existing `(kind, payload)` shape.
- **`concept:rimsky-cli`** — slug renames to `concept:rimsky` to reflect the binary rename. Existing verbs preserved; new `auth` subcommand group added.

### Retired framings

- **"Strict pass-through" framing of the standalone `mcp-servers/control-api/` module retires.** The module becomes part of control-api proper; the in-control-api hosting is the canonical shape. The concept doc's "Agentic MCP shim" subsection updates.

## Architectural shape

Control-api is the single long-running binary serving both protocol skins. Auth and permission enforcement happen at control-api ingress, before any handler runs, and apply uniformly to both skins. The MCP surface and the HTTP+JSON surface are protocol-shape views over one set of operations.

Rimsky stays agnostic about identity providers: the deployment layer (reverse proxy, sidecar, mesh) handles whatever identity story it has; rimsky's surface is API keys carried as `Authorization: Bearer <token>`. Operators who want OIDC etc. terminate the IdP at their edge and either inject API keys downstream or run a translation layer.

### Request flow

1. Client (`cmd:rimsky` CLI, MCP client, raw HTTP) sends a request with `Authorization: Bearer <key>` (or no Authorization at all, in anonymous mode).
2. Control-api auth middleware extracts the token. If present, computes SHA-256, looks up `rimsky_api_keys` by hash, applies the **active-status predicate**: `revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now()) AND (revoke_at IS NULL OR revoke_at > now())`. A key in its rotation-grace window (`revoke_at` set but still in the future) authenticates normally; only after the grace expires (whether the sweep has caught up and set `revoked_at` yet or not) does the predicate fail.
3. Lookup fails (no token but keys exist, or token doesn't match an active row) → 401. Audit row `kind: auth.access_denied` with `denial_reason` set; `action`, `request_params`, `mode` may be null because action resolution and body parsing haven't run yet (see Audit section for the exact denial-payload shape).
4. Lookup succeeds, or anonymous-mode predicate is true → identity established (real key id+name, or synthetic `key_id: null, key_name: "anonymous"`).
5. Middleware resolves the request to its action identifier (HTTP route → action lookup, or MCP tool → action lookup). Middleware checks the identity's grant for that action.
6. No grant entry matches → 403. Audit row `kind: auth.access_denied` with `denial_reason: "permission_denied"`; `action`, `request_params` populated.
7. Grant entry matches with `mode: dry_run` → handler runs in dry-run code path.
8. Grant entry matches with `mode: execute` (or unset) → handler runs normally.
9. After the handler, audit row `kind: auth.access_attempted` with full request/response context.

### MCP-as-skin

The MCP endpoint is `POST /mcp` on the same control-api server, the same TCP port, the same auth middleware, the same audit pipeline. An MCP `tools/call { name: "node_get" }` runs the same handler as `GET /instances/{id}/nodes/{name}`. Only the protocol envelope (JSON-RPC) and the audit `protocol_skin` field differ.

**Status of the existing standalone module.** A standalone Go module at `mcp-servers/control-api/` exists in the codebase today as scaffolding (~650 lines, a hand-curated tool catalog at `mcp-servers/control-api/tools.go`, a pass-through JSON-RPC server at `mcp-servers/control-api/server.go`, a thin config at `mcp-servers/control-api/config.go`). Its tool catalog and JSON-RPC envelope handling are reused in this spec by being moved into a package at `control/controlapi/mcp/`. The standalone `mcp-servers/control-api/` Go module is deleted as part of the cutover — no aliases, no compat shim, no parallel binary. The concept-doc subsection that describes it as "the standalone Go module under `mcp-servers/control-api/`" updates accordingly (see Concept catalog impacts below).

## Persistence schema

### `rimsky_api_keys` (new)

| column | type | notes |
|---|---|---|
| `id` | UUID NOT NULL | primary key |
| `key_hash` | BYTEA NOT NULL | SHA-256 of the plaintext; 32 bytes |
| `name` | TEXT NOT NULL | operator-friendly label |
| `permissions` | JSONB NOT NULL | array of grant entries |
| `created_at` | TIMESTAMPTZ NOT NULL | |
| `created_by_key_id` | UUID NULL | self-FK; NULL for keys minted in anonymous mode |
| `last_used_at` | TIMESTAMPTZ NULL | updated best-effort on each successful auth |
| `expires_at` | TIMESTAMPTZ NULL | hard expiration; NULL = never |
| `revoke_at` | TIMESTAMPTZ NULL | scheduled revocation (rotation grace) |
| `revoked_at` | TIMESTAMPTZ NULL | actual revocation timestamp |

Indexes:
- `UNIQUE (key_hash)`
- `UNIQUE (name) WHERE revoked_at IS NULL AND revoke_at IS NULL` — partial index for name uniqueness among currently-canonical keys; during a rotation grace window the old row's `revoke_at` is set and it drops out of the uniqueness index, allowing the new row to share the same name.
- `(revoke_at) WHERE revoke_at IS NOT NULL AND revoked_at IS NULL` — for the grace-expiry sweep.

### `rimsky_events`

No schema change. Gains new `kind` values; each row's `payload` carries the structured fields:

- `auth.access_attempted` — every authenticated request (including dry-runs).
- `auth.access_denied` — auth failure (401 or 403).
- `auth.key_created` — key minted.
- `auth.key_revoked` — key revoked (manual or scheduled-grace expiry).
- `auth.key_rotated` — rotation initiated.

Payload shapes documented in the Audit section below.

### Migration shape

Pre-v1 baseline: no compatibility shim. The migration creates `rimsky_api_keys` and its indexes. Dev DB nuke is the documented operator action for migration-from-pre-spec deployments.

## Authentication model

### Bearer tokens

API keys are presented via `Authorization: Bearer <key>`. The plaintext format is `rk_<44-char-base64url>`; the suffix encodes 33 bytes of CSPRNG entropy (264 bits). Server hashes via SHA-256 and stores 32 bytes.

**Why SHA-256, not argon2id/bcrypt.** API keys are random tokens with full entropy from a CSPRNG, not user-chosen secrets vulnerable to dictionary attacks. Slow hashing protects against offline dictionary attacks where the attacker has the hash and is guessing the plaintext — irrelevant here because there's no dictionary to guess from. SHA-256 is fast enough to run on every authenticated request without becoming a bottleneck.

### Key lifecycle

- **Mint** via `POST /auth/keys`. Body: `{ name, permissions, expires_at? }`. Response: `{ id, name, plaintext, permissions, created_at, expires_at }`. `plaintext` is surfaced once and only once; the server retains only `key_hash`.
- **List** via `GET /auth/keys`. Response: array of key records minus plaintext. Filterable via query params.
- **Show** via `GET /auth/keys/{name-or-id}`. Single key record minus plaintext.
- **Revoke** via `DELETE /auth/keys/{name-or-id}`. Sets `revoked_at = now()`. Refuses if revocation would leave zero active keys (returning the deployment to anonymous mode) unless `?force_leave_anonymous=true`.
- **Rotate** via `POST /auth/keys/{name-or-id}/rotate`. Body: `{ grace?: <duration> }` (default `24h`). Atomic: mints a new key with same name and same permissions; sets `revoke_at = now() + grace` on the old key; returns the new plaintext. Both keys valid until the grace expires.
- **Scheduled revocation sweep.** Periodic task (1m cadence, runs in `cmd:rimsky-scheduler` alongside other sweeps) sets `revoked_at = now()` on rows where `revoke_at <= now() AND revoked_at IS NULL`. Emits `kind: auth.key_revoked` with `reason: "rotation_grace"`.

### Implicit anonymous mode

Anonymous mode is derived from data via the predicate:

```sql
SELECT EXISTS (
  SELECT 1 FROM rimsky_api_keys
  WHERE revoked_at IS NULL
    AND (expires_at IS NULL OR expires_at > now())
    AND (revoke_at IS NULL OR revoke_at > now())
)
```

False → anonymous mode. True → authenticated mode. The middleware evaluates the predicate when a request arrives without a valid Bearer token (no Authorization header, or token not matching an active key).

When anonymous: request proceeds with synthetic identity:
- `identity_kind: "anonymous"`
- `key_id: null`
- `key_name: "anonymous"`
- Effective permissions: `[{ "action": "*" }]`

**Predicate caching.** Per-request DB hit is cheap (1-row EXISTS), but control-api may cache the result for a short TTL (default 1s) to avoid a hot-path DB read on every unauthenticated request. The TTL bounds the staleness window — under 1s, anonymous-mode transitions can be briefly stale per replica. The grace-revocation sweep runs in a separate process (`cmd:rimsky-scheduler`) and updates `rimsky_api_keys` directly; the TTL is the only freshness mechanism. No cross-process cache-invalidation channel is required because the staleness window is bounded by the TTL itself. Each control-api replica refreshes independently on its own clock.

**Loud startup warning.** When control-api starts and the predicate is true, it logs at WARN at startup and then every 5 minutes thereafter while in anonymous mode: `"ANONYMOUS MODE: no API keys provisioned; all requests treated as admin. Run 'rimsky auth init' to enable authentication."` The banner stops once any key exists.

**Revoke-the-last-key guard.** `DELETE /auth/keys/{name-or-id}` refuses if the operation would reduce the active-key count to zero, returning 409 Conflict with a body explaining the issue and the override flag. Override via `?force_leave_anonymous=true` (operator-explicit, never default).

## Permissions model

### Grant entry shape

Each entry is a JSON object:

- `action` (required, string) — verb-noun action identifier or wildcard.
- `mode` (optional, enum) — `execute` (default) or `dry_run`. Only meaningful for write actions; ignored on read actions.

Future entries may add fields (`scope`, `rate_limit`, etc.); the parser is forward-compatible — unknown fields are ignored. This is what lets V2 add rate-limit and scoping without a schema migration.

### Action grammar

Actions are `<noun>:<verb>` strings. The canonical set for V1 (each maps to one HTTP route family and one MCP tool name):

| Action | HTTP route | MCP tool | Read/Write |
|---|---|---|---|
| `instance:read` | `GET /instances`, `GET /instances/{idOrKey}` | `instance_list`, `instance_get` | read |
| `instance:create` | `POST /instances` | `instance_create` | write |
| `instance:terminate` | `DELETE /instances/{idOrKey}` | `instance_terminate` | write |
| `template:read` | `GET /templates`, `GET /templates/{id}` | `template_list`, `template_get` | read |
| `template:register` | `POST /templates` | `template_register` | write |
| `template:deploy` | `POST /templates/{id}/deploy` | `template_deploy` | write |
| `template:undeploy` | `POST /templates/{id}/undeploy` | `template_undeploy` | write |
| `template:deregister` | `DELETE /templates/{id}` | `template_deregister` | write |
| `tag:read` | `GET /tags` | `tag_list` | read |
| `tag:create` | `POST /tags` | `tag_create` | write |
| `tag:set` | `PUT /tags/{tag}` | `tag_set` | write |
| `tag:delete` | `DELETE /tags/{tag}` | `tag_delete` | write |
| `node:read` | `GET /instances/{idOrKey}/nodes`, `GET /nodes/{id}` | `node_list`, `node_get` | read |
| `node:invalidate` | `POST /nodes/{id}/invalidate`, `POST /admin/instances/{instance}/nodes/{node_id}/invalidate` | `node_invalidate` | write |
| `node:reset` | `POST /nodes/{id}/reset` | `node_reset` | write |
| `message:send` | `POST /instances/{id}/messages` | `message_send` | write |
| `message:read` | `GET /instances/{id}/messages`, `GET /messages/{id}` | `message_list`, `message_get` | read |
| `event:read` | `GET /events` | `event_list` | read |
| `lineage:read` | `GET /lineage/*` | `lineage_get` | read |
| `lineage:prune` | `POST /admin/lineage/prune` | `lineage_prune` | write |
| `parked-node:read` | `GET /diagnostics/parked`, `GET /admin/diagnostics/parked-nodes` | `parked_node_list` | read |
| `waitset:read` | `GET /admin/diagnostics/wait-sets` | `waitset_list` | read |
| `claim-holders:read` | `GET /lock-holders/{claim_handle_id}/claim-holders` | `claim_holders_list` | read |
| `backfill:create` | `POST /instances/{id}/backfills` | `backfill_create` | write |
| `backfill:read` | `GET /instances/{id}/backfills`, `GET /backfills/{op_id}`, `GET /backfills/{op_id}/partitions` | `backfill_list`, `backfill_get`, `backfill_partitions` | read |
| `backfill:cancel` | `POST /backfills/{op_id}/cancel` | `backfill_cancel` | write |
| `asset:read` | `GET /instances/{id}/assets`, `GET /instances/{id}/assets/{alias}`, `GET /instances/{id}/assets/{alias}/versions`, `GET /instances/{id}/assets/{alias}/materialization-history` | `asset_list`, `asset_get`, `asset_versions`, `asset_materialization_history` | read |
| `asset:materialize` | `POST /instances/{id}/assets/{alias}/materialize` | `asset_materialize` | write |
| `asset:delete` | `DELETE /instances/{id}/assets/{alias}` | `asset_delete` | write |
| `sensor:observe` | `POST /sensors/{watch_id}/observations` | (none in V1; service-to-service callback) | write |
| `diagnostics:read` | `GET /admin/diagnostics/held-frames` | `held_frames_list` | read |
| `auth:read` | `GET /auth/keys`, `GET /auth/keys/{name-or-id}`, `GET /auth/status` | `auth_list`, `auth_get`, `auth_status` | read |
| `auth:create` | `POST /auth/keys` | `auth_create_key` | write |
| `auth:revoke` | `DELETE /auth/keys/{name-or-id}` | `auth_revoke_key` | write |
| `auth:rotate` | `POST /auth/keys/{name-or-id}/rotate` | `auth_rotate_key` | write |

**Wake-parked is `node:invalidate`.** Invalidating a parked node resumes it (the handler at `code:control/controlapi/nodes.go::handleInvalidateNode` documents: "Invalidate a node (resumes if parked, fresh-invalidates otherwise)"). There is no separate `parked-node:wake` action; wake is what `node:invalidate` already does on a parked target.

**Node-reset is distinct from invalidate.** `node:reset` (`POST /nodes/{id}/reset`) is a recovery verb for failed nodes: it drives the failed→stale transition through the frame engine and is rejected if the node is not in `failed`. Different from invalidate (which is the general "mark stale and re-fire" verb). Both are operator/agent-shaped writes.

**Sensor observations are service-to-service.** `sensor:observe` exists to gate `POST /sensors/{watch_id}/observations`, which sensor services push to. Bundled sensors (cron, http, object-store, webhook) get keys whose grant is `[{ "action": "sensor:observe" }]` — narrow by design. Operator-facing roles don't need this action.

**Multiple routes for one action.** Some actions map to multiple HTTP routes. Where legacy paths coexist with canonical ones (e.g. `POST /admin/instances/{instance}/nodes/{node_id}/invalidate` alongside `POST /nodes/{id}/invalidate`; `GET /admin/diagnostics/parked-nodes` alongside `GET /diagnostics/parked`), both routes share the action gate. Route-consolidation (retiring legacy aliases) is a separate cleanup not covered by this spec; until that cleanup lands, the action registry lists every live route alongside the action.

The canonical action registry lives in code at `control/controlapi/actions.go` (the file name is illustrative; placement is implementation detail). Each action declares its HTTP routes and MCP tool name; the auth middleware looks up actions by route or tool. The same registry validates `POST /auth/keys` request bodies — unknown action strings are rejected with a 400.

### Wildcard semantics

Two precise rules — the colon is always part of the match boundary:

- **Noun-prefix wildcard.** An entry of form `<noun>:*` matches any request action that **starts with `<noun>:`** (the colon is part of the prefix). Example: entry `auth:*` matches request `auth:create` and `auth:rotate`, but does NOT match `authority:create` (no colon at position 4 of the request).
- **Verb-suffix wildcard.** An entry of form `*:<verb>` matches any request action that **ends with `:<verb>`** (the colon is part of the suffix). Example: entry `*:read` matches request `node:read` and `instance:read`, but does NOT match `node:readwrite` (no `:read` at the end).

Additional rules:

- `*` (alone) matches any action.
- An exact action string matches only itself.
- No infix wildcards (`instance:*:thing` is invalid; the parser rejects it at key-creation time).
- No regex.

A grant containing `[{ "action": "*" }]` is the admin grant; nothing is denied.

### Permission check algorithm

Given an incoming request resolved to an action `R` and a key with grant entries `E[0..n]`:

```
for i in 0..n:
  E_i.action == "*"                      -> matches
  E_i.action == R                        -> matches  (exact match)
  E_i.action ends with ":*" AND
    R starts with E_i.action[:-1]        -> matches  (noun-prefix; trailing colon retained)
  E_i.action starts with "*:" AND
    R ends with E_i.action[1:]           -> matches  (verb-suffix; leading colon retained)
  otherwise                              -> entry does not match

if no entry matches:
  403

mode = first matching entry's `mode` (or "execute" if unset)
proceed with handler dispatch under that mode
```

"First match wins" is by iteration order over the grant array. Effectively, "more specific" entries should appear before "more general" entries when an operator wants a specific mode override to apply (e.g., putting `{ "action": "instance:create", "mode": "dry_run" }` before `{ "action": "*" }` to dry-run only that one action while admin-equivalent on everything else). In practice, V1 grants are small and rarely require ordering nuance.

### Bundled role templates (CLI-side)

The `cmd:rimsky` binary embeds JSON resources at `cmd/rimsky/roles/*.json`. The CLI loads them at expand time. V1 ships:

**`admin.json`**:
```json
{
  "name": "admin",
  "description": "Full administrative access including auth management.",
  "permissions": [{ "action": "*" }]
}
```

**`operator.json`** — full operational access; can read auth state but cannot mint, revoke, or rotate keys (those are admin-only in V1). Sensor observation is not included (operators are not sensors). Self-rotation as a separate gate is a V2 consideration if it earns its place.
```json
{
  "name": "operator",
  "description": "Full operational access; can read auth state but cannot mutate keys.",
  "permissions": [
    { "action": "instance:*" },
    { "action": "template:*" },
    { "action": "tag:*" },
    { "action": "node:*" },
    { "action": "message:*" },
    { "action": "event:read" },
    { "action": "lineage:*" },
    { "action": "parked-node:*" },
    { "action": "waitset:read" },
    { "action": "claim-holders:read" },
    { "action": "backfill:*" },
    { "action": "asset:*" },
    { "action": "diagnostics:read" },
    { "action": "auth:read" }
  ]
}
```

**`read-only.json`**:
```json
{
  "name": "read-only",
  "description": "Read access to all resources; cannot mutate state.",
  "permissions": [{ "action": "*:read" }]
}
```

**`agent-supervisor.json`**:
```json
{
  "name": "agent-supervisor",
  "description": "Read access across the platform plus the writes a supervisor agent realistically needs: invalidate (covers wake-parked), reset (recover failed nodes), and message-send (backfills and other coordination kinds).",
  "permissions": [
    { "action": "*:read" },
    { "action": "node:invalidate" },
    { "action": "node:reset" },
    { "action": "message:send" }
  ]
}
```

**`sensor-service.json`** — for keys minted for bundled sensor services (cron, http, object-store, webhook). Narrow by design: a sensor's only job is to push observations; it has no need to read platform state or invoke other endpoints.
```json
{
  "name": "sensor-service",
  "description": "Minimal grant for a bundled sensor service: push observations only.",
  "permissions": [
    { "action": "sensor:observe" }
  ]
}
```

The CLI expands a role to its permissions array, applies `--add=<action>` and `--remove=<action>` patches, and POSTs the resulting grant to `POST /auth/keys`. The server stores the raw grant; no role identifier is recorded server-side. `rimsky auth show <name>` displays the raw grant; if the grant matches a known role's expansion modulo a small delta, the CLI may render it as `role:operator + 1 override` for ergonomics, but this is display-only — the server doesn't know.

Operators wanting custom roles drop additional JSON files into a CLI-resolved config directory (e.g. `~/.rimsky/roles/`) or pass `--role-file=<path>`; the CLI loads them like the bundled set. No server-side surface for operator-defined roles in V1.

## MCP protocol surface

### Hosting

The MCP endpoint is `POST /mcp` on the same control-api server. The same TCP port serves HTTP+JSON and MCP. No separate process or port.

The existing standalone module `mcp-servers/control-api/` folds into `control/controlapi/mcp/` as a package. Tool definitions, the JSON-RPC envelope handler, and the protocol dispatch logic are reused where applicable. The standalone module's `cmd/...` entry point (if it has one) retires.

### Protocol methods

V1 implements MCP's "tools" capability only:

- `initialize` — handshake; advertises only `tools` capability; declares no `resources`, no `prompts`, no subscription support.
- `tools/list` — returns the catalog filtered by the requesting key's permission grant. A tool is included if the requesting key has any permission entry matching the tool's action.
- `tools/call` — invokes a tool. Re-evaluates the permission check (same code path as the equivalent HTTP route).

Other MCP methods (`resources/list`, `resources/read`, `prompts/list`, `prompts/get`, subscription methods) return the standard JSON-RPC "method not found" error.

### Tool naming convention

Tools are lowercase, underscored, and mirror their action: `instance:read` → `instance_get` (single) and `instance_list` (collection); `message:send` → `message_send`; `template:register` → `template_register`. The mapping is one-to-one for each leaf action; the registry in `control/controlapi/actions.go` carries both.

One deviation preserved from the pre-existing tool catalog: `diagnostics:read` maps to `held_frames_list` (a content-descriptive name reflecting the route `GET /admin/diagnostics/held-frames`) rather than a noun-mirroring `diagnostics_get`. This name predates the spec in `mcp-servers/control-api/tools.go` and stays for V1 to avoid renaming an LLM-facing tool; future tools under the `diagnostics:*` namespace (if any) are free to use the noun-mirroring convention.

### Filtered catalog

`tools/list` runs the same wildcard-matching algorithm against the requesting key's grant for each candidate tool. A read-only key sees only `*_list` / `*_get` / `*_read` shaped tools. An agent-supervisor key sees those plus `message_send`. The catalog is computed per-request; cache opportunity (cache-by-grant-hash) noted for V2 if it becomes a hot path.

### Tool invocation

`tools/call { name: "node_get", arguments: { instance_id, node_alias } }`:

1. Look up `node_get` in the tool registry → action `node:read`.
2. Run the auth+permission check (same code path as HTTP).
3. Dispatch to the same handler as `GET /instances/{instance_id}/nodes/{node_alias}` would. Arguments are mapped from MCP `arguments` to HTTP-handler input shape.
4. Wrap the handler response in MCP's `tools/call` result envelope.
5. Emit `auth.access_attempted` with `protocol_skin: "mcp"`.

For write actions in dry-run mode: the synthetic-response shape (see Dry-run section) is wrapped in the MCP result envelope verbatim. MCP clients see `{ dry_run: true, ... }` in the result and can communicate that to the agent.

### Auth on MCP

MCP clients carry `Authorization: Bearer <key>` in the HTTP layer that transports JSON-RPC. Most MCP client implementations (Claude Desktop, custom clients) support per-server header configuration; operators put the API key in the client's MCP config. No MCP-protocol-level auth mechanism; this rides on HTTP-layer Bearer authentication.

Stdio transport for MCP is out of scope for V1 — it would require a separate forwarder binary that reads stdin/stdout JSON-RPC and forwards over HTTP with the key injected. Documented as deferred.

## Control-api endpoints

### Auth endpoints (new)

All new endpoints live under `/auth/keys`. They themselves require `auth:*` permissions (except when in anonymous mode, where they're admin-equivalent like every other endpoint).

- **`POST /auth/keys`** — mint. Body: `{ name, permissions, expires_at? }`. Server validates the action strings against the registry, the name uniqueness via the partial index, the permissions schema. Returns `{ id, name, plaintext, permissions, created_at, expires_at }`. Emits `auth.key_created`. Required permission: `auth:create`.
- **`GET /auth/keys`** — list. Query params: `name_filter?`, `include_revoked?` (default false). Returns array of key records minus plaintext. Required permission: `auth:read`.
- **`GET /auth/keys/{name-or-id}`** — show. Returns one record. Required permission: `auth:read`.
- **`DELETE /auth/keys/{name-or-id}`** — revoke. Query param: `force_leave_anonymous?` (default false). Refuses with 409 if would leave zero active keys unless flag set. Emits `auth.key_revoked` with `reason: "manual"`. Required permission: `auth:revoke`.
- **`POST /auth/keys/{name-or-id}/rotate`** — rotate. Body: `{ grace?: <duration> }`. Returns `{ old_key_id, new_key_id, name, plaintext, revoke_at }`. Emits `auth.key_rotated`. Required permission: `auth:rotate`.
- **`GET /auth/status`** — diagnostic. Returns `{ mode: "anonymous" | "authenticated", active_key_count: <int>, admin_count: <int> }`. Gated by `auth:read`: in anonymous mode the synthetic admin identity holds `*` so the call succeeds; in authenticated mode the caller's key must include `auth:read`. This composes naturally with the action registry — `auth:status` is not a separate action.

### Existing endpoints — gating

All existing control-api endpoints come under the auth middleware. Each endpoint declares its required action; the registry maps routes to actions. The middleware runs before any handler. No endpoint is exempt (except `auth:status` per above, and `auth:create` in anonymous mode by virtue of anonymous-mode allowing all actions).

Health/readiness endpoints (`GET /health`, `GET /ready`) are not auth-gated — they predate auth and are operator-infrastructure surface, not control-plane surface.

## CLI

The unified `cmd:rimsky` binary, renamed from `cmd:rimsky-cli`. All existing verb groups (`template`, `instance`, `node`, `tag`, `compose`, etc.) preserved. The new `auth` subcommand group is added.

**Rename cutover.** Pre-v1 stance: the `rimsky-cli` binary is renamed to `rimsky`; no alias shim, no compat symlink. Operator scripts and CI invocations update to the new name. Documented in the CHANGELOG.

### New subcommands

- **`rimsky auth init`** — bootstrap admin key. Calls `POST /auth/keys` with body derived from the bundled `admin.json` role and `name: "admin"`. Requires anonymous mode (refuses if any active key exists, returning a non-zero exit with directions to use `auth create-key` instead). Prints the plaintext to stdout with a "save this now, won't be shown again" banner.
- **`rimsky auth create-key`** — flags:
  - `--name=<name>` (required)
  - `--role=<role-name>` (required; the bundled-role expansion target)
  - `--role-file=<path>` (alternative: load role from path instead of bundled)
  - `--add=<action>` (repeatable; adds an entry with `mode: execute`)
  - `--remove=<action>` (repeatable; removes matching entries by exact action string)
  - `--dry-run=<action>` (repeatable; adds an entry with `mode: dry_run`). CLI-side validation: rejects read actions (dry-run is meaningless for reads) and rejects auth-mutation actions `auth:create`, `auth:revoke`, `auth:rotate` (dry-run semantics interact badly with the implicit-anonymous predicate; see the Dry-run "What's not dry-runnable" section).
  - `--expires=<duration>` (optional; relative to now)
- **`rimsky auth list [--name-filter=<glob>] [--include-revoked]`** — list keys.
- **`rimsky auth show <name-or-id>`** — show one key. Fuzzy-matches the grant against bundled roles for display.
- **`rimsky auth revoke <name-or-id> [--force-leave-anonymous]`** — immediate revoke.
- **`rimsky auth rotate <name-or-id> [--grace=<duration>]`** — rotate; prints new plaintext.
- **`rimsky auth status`** — calls `GET /auth/status`; reports anonymous vs authenticated and key counts.

### Endpoint and key resolution

All `rimsky auth` subcommands resolve:
- Control-api endpoint from `--endpoint=<url>` flag or `RIMSKY_CONTROL_API` env var.
- API key from `--key=<key>` flag or `RIMSKY_API_KEY` env var.

In anonymous mode, the key is unnecessary; the CLI tolerates a missing key for `auth init` and `auth status` specifically. Other auth subcommands and all non-auth subcommands require a key once anonymous mode has ended.

## Audit

Audit records are rows in `table:rimsky_events`. The `kind` column carries `auth.<event>`; the `payload` is JSONB with structured fields. No schema change to `rimsky_events`.

### Payload shapes

**`auth.access_attempted`**:
```json
{
  "key_id": "<uuid-or-null>",
  "key_name": "<name-or-anonymous>",
  "identity_kind": "api_key" | "anonymous",
  "protocol_skin": "http" | "mcp",
  "action": "message:send",
  "request_path": "/instances/<id>/messages",
  "request_method": "POST",
  "request_params": { /* verbatim */ },
  "response_status": 200,
  "mode": "execute" | "dry_run",
  "executed": true,
  "duration_ms": 42,
  "client_ip": "10.0.1.5",
  "user_agent": "rimsky-cli/<version>"
}
```

**`auth.access_denied`** — same shape as `auth.access_attempted`, plus a `denial_reason` field. Field-by-field semantics differ depending on whether the denial happened pre- or post-action-resolution:

```json
{
  "key_id": "<uuid-or-null>",
  "key_name": "<name-or-null>",
  "identity_kind": "api_key" | "anonymous" | null,
  "protocol_skin": "http" | "mcp",
  "action": "<string-or-null>",
  "request_path": "/instances/<id>/messages",
  "request_method": "POST",
  "request_params": null | { /* verbatim if parsed */ },
  "response_status": 401 | 403,
  "mode": null,
  "executed": false,
  "duration_ms": <int>,
  "client_ip": "<string>",
  "user_agent": "<string>",
  "denial_reason": "no_token" | "invalid_token" | "expired_token" | "revoked_token" | "permission_denied"
}
```

Population rules for denial rows:

- For `denial_reason` ∈ `{no_token, invalid_token, expired_token, revoked_token}` (early denials, before action resolution): `action`, `request_params`, `mode` are `null`; `executed` is `false`; `key_id` / `key_name` / `identity_kind` are populated only if the token was structurally well-formed and matched a row (in which case the row may have been revoked or expired — those fields are filled from the matched row).
- For `denial_reason` = `permission_denied` (post-action-resolution): `action` and `request_params` are populated; `mode` is `null` (no matching grant entry, so no mode determined); `executed` is `false`.

**`auth.key_created`**:
```json
{
  "key_id": "<uuid>",
  "key_name": "<name>",
  "permissions": [ /* verbatim grant */ ],
  "created_by_key_id": "<uuid-or-null>",
  "expires_at": "<timestamp-or-null>"
}
```

**`auth.key_revoked`**:
```json
{
  "key_id": "<uuid>",
  "key_name": "<name>",
  "revoked_by_key_id": "<uuid-or-null>",
  "reason": "manual" | "rotation_grace" | "expired"
}
```

**`auth.key_rotated`**:
```json
{
  "old_key_id": "<uuid>",
  "new_key_id": "<uuid>",
  "name": "<name>",
  "revoke_at": "<timestamp>"
}
```

### Verbatim request_params

Audit records store `request_params` verbatim, not hashed. Rationale: rimsky's userdata-inert invariant (`@blessed-invariant 11`, `20`, `21`) ensures the request body never carries sensitive content; the only sensitive value in an auth-relevant exchange is the API key itself, which is in the `Authorization` header (not stored in any audit record). Verbatim params make the audit log far more useful for forensic queries ("show me everything `agent:supervisor:prod` did with `template_hash X`").

### Retention

Auth audit rows follow the existing `rimsky_events` retention policy (operator-configurable, default keep-while-referenced + trailing window). Operators with regulatory retention requirements extend via existing `rimsky.yml` knobs (`retention.events: <duration>`). No new retention knob specifically for auth.

## Dry-run mode

### Handler refactoring

Each write handler factors into two functions:

- `validate(req) -> (validation_result, errors[])` — pure; no side effects beyond external-service calls that are themselves side-effect-free reads (e.g. the `Validation` mix-in on producers).
- `execute(req, validation_result) -> response` — applies the mutation using the cached validation result.

The auth middleware passes a `dry_run: bool` to the handler. Handlers check it:
- `dry_run: true` → call `validate(req)`; on errors, return as in normal flow (400 or similar); on success, build a synthetic response per the rules below and return.
- `dry_run: false` → call `validate(req)`; on success, call `execute(req, validation_result)`; return the response.

### Synthetic response shape

Dry-run responses carry an envelope field `dry_run: true`. Placeholder IDs for creates are clearly marked:

```json
{
  "dry_run": true,
  "would_have_created": {
    "instance_id": "dry-run-not-persisted",
    "template_hash": "<actual>",
    "params": { /* verbatim */ }
  }
}
```

For non-create writes:

```json
{
  "dry_run": true,
  "would_have_invalidated": {
    "instance_id": "<actual>",
    "target": "<node-alias>"
  }
}
```

For `template:register` with `Validation` mix-in calls: the dry-run runs the validation RPCs faithfully (they're side-effect-free); skips only the DB insert. Errors from validation surface as in normal flow. This makes dry-run a real precursor to live invocation rather than a stub.

### Audit

Dry-run requests fire `auth.access_attempted` with `executed: false`. The audit row is the canonical evidence of "the agent attempted this with these params; we didn't apply it." Operators reviewing agent behavior pre-promotion read these rows to validate intent.

### What's not dry-runnable

Read actions don't have a dry-run variant; `mode: dry_run` on a read action is ignored. (The CLI rejects this at create-key time as a UX nicety; the server tolerates it for forward-compatibility.)

Auth mutations (`auth:create`, `auth:revoke`, `auth:rotate`) are not dry-runnable in V1 — the `mode` field on grant entries for these actions is ignored. Rationale: dry-running auth mutations doesn't compose well with the implicit-anonymous logic (a dry-run create wouldn't change the active-key count, leading to confusing audit trails). Documented limitation.

## Bootstrap

The operational sequence by which the first API key gets created. Exploits implicit anonymous mode; no separate bootstrap mechanism.

### Sequence

1. Operator deploys rimsky. Schema is created via `cmd:rimsky-migrate`. `rimsky_api_keys` is empty.
2. Control-api starts. Anonymous-mode predicate is true. Startup banner warns.
3. Operator runs `rimsky auth init`. The CLI:
   - Reads the `admin.json` bundled role.
   - Calls `POST /auth/keys` with `{ name: "admin", permissions: <expanded admin grant> }`.
   - Request is anonymous; auth middleware grants admin-equivalent permissions; the call succeeds.
   - Server mints the key, returns `{ id, name, plaintext, ... }`.
   - CLI prints the plaintext with a banner: `"Save this admin key now — it will not be shown again: rk_..."`
4. Operator captures the plaintext, stores it securely, configures their tooling (`RIMSKY_API_KEY` env var or `--key` flag).
5. Anonymous mode ends — `rimsky_api_keys` now has one active row. Subsequent requests require Bearer auth.

### Bootstrap idempotency

`rimsky auth init` refuses to run if any active key exists. Operator gets a non-zero exit with text directing them to `rimsky auth create-key --name=... --role=admin` (which itself requires an existing admin key, so the operator must already have one).

The CLI's "refuse if any active key exists" check is a UX nicety, not the authoritative gate. The authoritative gate is the server's anonymous-mode predicate: `POST /auth/keys` succeeds unauthenticated only when the predicate is true. In a race where another caller mints a key between the CLI's check and the CLI's `POST`, the server returns 401 (authenticated mode reached; no Bearer token) and the CLI surfaces that error rather than its own friendlier one.

### Break-glass: lost admin key

If all admin keys are lost: the operator opens a `psql` session and either deletes the rows (`DELETE FROM rimsky_api_keys`) or sets them as revoked (`UPDATE rimsky_api_keys SET revoked_at = now() WHERE revoked_at IS NULL`). Anonymous mode resumes (predicate becomes false). `rimsky auth init` works again. Audit log retains the gap (the revoked rows persist if `UPDATE` was used; if `DELETE` was used, the gap is implicit).

This is documented as a known operator-recoverable scenario; no CLI verb is required because by definition the operator has DB access in this scenario.

## Key rotation

### Flow

`rimsky auth rotate <name-or-id> [--grace=<duration>]`:

1. CLI calls `POST /auth/keys/{name-or-id}/rotate` with body `{ grace: "24h" }` (or operator-specified).
2. Server, in one transaction:
   - Reads the existing key (target row).
   - Mints a new key with same `name`, same `permissions`, same `expires_at` (or none, if the existing key has none).
   - Sets `revoke_at = now() + grace` on the existing key.
   - Inserts the new key row.
   - Emits `auth.key_rotated`.

   Rotation preserves identity: name, permissions, and `expires_at` carry forward unchanged. The rotation request body has no field for adjusting `expires_at` — operators wanting to extend or change the expiration mint a new key (with a different name) rather than rotating. This keeps rotation cleanly scoped to "swap the secret while preserving the identity."
3. Response: `{ old_key_id, new_key_id, name, plaintext, revoke_at }`.
4. CLI prints the new plaintext with a "save this now" banner.
5. Operator updates clients to use the new key.
6. Periodic sweep (`rimsky-scheduler`, 1m cadence) revokes keys past `revoke_at`.

### Unique-name handling during rotation

The active-name uniqueness constraint is `UNIQUE (name) WHERE revoked_at IS NULL AND revoke_at IS NULL`. During the grace window:

- Old key row: `revoked_at IS NULL`, `revoke_at = now() + grace` → not in the index.
- New key row: `revoked_at IS NULL`, `revoke_at IS NULL` → in the index.

Both rows share `name`, but only one is in the active-uniqueness index at any moment. Constraint satisfied.

### Sweep

```sql
UPDATE rimsky_api_keys
SET revoked_at = now()
WHERE revoke_at IS NOT NULL
  AND revoke_at <= now()
  AND revoked_at IS NULL
RETURNING id, name;
```

For each returned row, emit `kind: auth.key_revoked` with `reason: "rotation_grace"`. The sweep is idempotent (re-running selects no rows on the second pass).

## Concept catalog impacts

**Application timing.** The design-log entries below are NOT written when this spec is approved. They're applied during `execute-plan` in lockstep with the code that realizes each piece. Execute-plan derives full entry content from this spec's body.

### New concept entries

- **`concept:api-key`** — high-entropy Bearer-token credential issued by rimsky; SHA-256-hashed at rest; required immutable unique `name`; carries a permission grant. Lifecycle: mint → optional rotation (mints a new key, sets `revoke_at = now + grace` on the old key) → revoke. A periodic rotation-grace sweep runs in `cmd:rimsky-scheduler` alongside the existing sweeps, revoking keys whose grace has expired; it is documented here under the key-lifecycle concept rather than as its own sweep concept.
- **`concept:permission`** — verb-noun action grammar; per-key JSONB grant; prefix/suffix wildcards; `mode: execute | dry_run` modifier.
- **`concept:anonymous-mode`** — data-derived deployment state; "no active keys" → synthetic admin identity on every request; the bootstrap path.
- **`concept:role-template`** — CLI-bundled JSON resource for permission-grant expansion; server-side has no role concept.
- **`concept:dry-run`** — per-grant write-action modifier; validate-without-mutate semantics; audit-trail evidence of attempted action.

### Updated concept entries

- **`concept:control-api`** — now hosts both HTTP+JSON and MCP protocol skins on the same operations. "Agentic MCP shim" subsection updates to reflect in-control-api hosting; standalone `mcp-servers/control-api/` framing retires.
- **`concept:event-log`** — gains `auth.*` event kinds (`auth.access_attempted`, `auth.access_denied`, `auth.key_created`, `auth.key_revoked`, `auth.key_rotated`); payload schemas documented.
- **`concept:rimsky-cli`** — concept slug renames to `concept:rimsky` to reflect the binary rename; existing verbs preserved; new `auth` subcommand group.
- **`concept:inertness`** — clarifying addition: audit records store `request_params` verbatim, justified by the userdata-inert and claim/payload-inert invariants — no secrets present in any request body to a control-plane endpoint.
- **`concept:rimsky-yml`** — clarifying addition: `rimsky.yml` carries no auth-related keys. Auth is data-derived (the active-status predicate on `table:rimsky_api_keys`) and not yml-config-derived. Operators do not configure an auth mode, a bootstrap key, or any other auth knob in the yml file; the data state of `rimsky_api_keys` is the sole source of truth.

### Retired concept entries

None. The standalone `mcp-servers/control-api/` module's framing retires, but it didn't have its own concept slug.

## Blessed-invariant updates

- **New invariant: API keys are revoked, not deleted.** The `revoked_at` column is set; rows persist. Preserves the audit trail (audit records reference `key_id`; the row must remain queryable for joins/lookups). The orphan reaper does not touch `rimsky_api_keys`.
- **New invariant: Plaintext API keys are surfaced exactly once.** At mint and at each rotation. The server retains only `key_hash`. Lost plaintext is unrecoverable; recovery requires rotation (which mints a new plaintext for the same logical identity).
- **New invariant: Anonymous-mode state is data-derived, not config-derived.** Anonymous mode is computed from `rimsky_api_keys` row counts; there is no separate config bit. Toggling between anonymous and authenticated is automatic on the first/last active key. `rimsky.yml` carries no auth-mode knob.
- **New invariant: Auth middleware runs before every control-api handler.** No control-api endpoint bypasses the auth middleware except `/health` and `/ready` (deployment-infrastructure paths predating control-plane semantics).

## Testing strategy

### Scenario tests (`test/scenarios/auth/`)

- **Bootstrap scenarios.** Fresh database; control-api in anonymous mode; `rimsky auth init` mints admin key; anonymous mode ends; subsequent unauthenticated requests get 401.
- **Permission grant scenarios.**
  - Key with `*:read` can call all read tools/routes; 403 on writes.
  - Key with `instance:*` can create + terminate instances; 403 on `template:*` operations.
  - Wildcard semantics — prefix, suffix, full — match the expected actions.
  - First-match-wins ordering for entries with conflicting modes.
- **Dry-run scenarios.**
  - Key with `instance:create` and `mode: dry_run` POSTs to create; receives `dry_run: true` envelope; no row inserted; audit row has `executed: false`.
  - Same key with no `mode` actually creates.
  - Dry-run of `template:register` invokes `Validation` mix-in calls faithfully (side-effect-free); skips only the DB insert.
- **Rotation scenarios.**
  - Key in active use; rotate with 1h grace; both old and new keys work during the grace window; sweep revokes old; old key returns 401 with `denial_reason: revoked_token`; new key still works.
  - Rotate with 0s grace; old key revoked immediately by next sweep cycle.
- **Revoke guard scenarios.**
  - Single active admin key; attempt revoke → 409.
  - `force_leave_anonymous=true` → success; anonymous mode resumes.
- **Anonymous-mode transition scenarios.**
  - Anonymous → mint key → authenticated; revoke-all-with-force → anonymous; second mint → authenticated.
  - Predicate caching: cache invalidates correctly on mutations.
- **MCP-skin scenarios.**
  - Same logical operation via HTTP and via MCP gets the same permission check and same dry-run behavior.
  - Audit rows differ only in `protocol_skin`.
  - `tools/list` filters correctly per requesting key's grant.
- **Audit content scenarios.**
  - All required fields present per kind.
  - `request_params` stored verbatim.
  - `key_name` denormalized correctly even after revocation.

### Conformance / integration

- Existing `cmd:rimsky-control-api` integration tests extended to exercise the auth middleware on every HTTP endpoint.
- New MCP-protocol conformance check: extends `cmd:rimsky-conformance-probe` with cases that exercise `initialize` → `tools/list` → `tools/call` against a running control-api with a sample admin key, plus filtered-catalog cases under a non-admin key. (Adding a sibling binary `cmd:rimsky-mcp-conformance` is unnecessary; the probe already exercises adjacent surfaces.)

### Migration tests

The migration creating `rimsky_api_keys` runs cleanly against an empty Postgres and against a Postgres carrying existing data-platform-extensions schema. Idempotent re-runs. Indexes verified.

### Smoke test extension

`test/smoke/setup.go` extended to:
- Bring up control-api in anonymous mode.
- Run `rimsky auth init`.
- Verify subsequent operations require the minted key.
- Exercise `rimsky auth create-key`, `rimsky auth rotate`, `rimsky auth revoke` end-to-end.
- Run a sample MCP `tools/list` and `tools/call` with the minted key.

## Cross-references

- **Supersedes (Parts 1, 6 of):** `sketch:2026-05-14-agentic-platform`.
- **Resolves (already resolved upstream):** sketch's Part 3 — `ValidateClaimantUserdata` is delivered by the `Validation` mix-in in `spec:2026-05-15-data-platform-extensions-design`; this spec does not re-resolve it.
- **Defers (per per-project stance):** sketch's Parts 2 (knowledge store), 4 (executor-level MCP convention), 5 (supervisor templates), and the `sensor-rimsky-lifecycle` bundled sensor. Consumer-built; not platform.
- **Composes with:** `spec:2026-05-15-data-platform-extensions-design` (message bus, action set for `backfill:*` / `asset:*` / `lineage:read`, `Validation` mix-in for `template:register` dry-run).
- **Updates concept doc:** `concept:control-api` (MCP-as-skin replaces standalone module framing).
- **Existing contracts built on:** `spec:2026-05-04-foundation-contract`, `spec:2026-05-04-modeling-layer-contract`, `spec:2026-05-04-service-protocol-contract`.
- **Public docs successor homes** (out of scope to write here): `docs/concepts/api-key.md`, `docs/concepts/permission.md`, `docs/concepts/anonymous-mode.md`, `docs/protocols/control-api-mcp.md`, `docs/agents/getting-started.md`, `docs/agents/examples/mcp-operator-dialogue.md`.
