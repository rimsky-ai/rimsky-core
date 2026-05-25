# Control-plane MCP and auth implementation plan

**Spec:** `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md`
**Goal:** Ship API-key auth (Bearer tokens, hashed at rest, per-key JSONB permission grants), implicit anonymous mode, dry-run, structured audit, MCP as a first-class control-api protocol skin, plus CLI auth subcommands under a renamed `rimsky` binary.
**Architecture:** Auth and permissions live in `control/controlapi/` and a new `rimsky_api_keys` table; MCP is hosted by `control/controlapi/mcp/` as a package on the same TCP port as HTTP+JSON; the existing standalone `mcp-servers/control-api/` Go module folds in and is deleted; dry-run is implemented per write-handler via a `dry_run` context flag and a `validate/execute` factoring; audit rides on the existing `rimsky_events` table with new `auth.*` kinds; rotation-grace revocation runs as a new sweep in `cmd/rimsky-scheduler/`.
**Tech Stack:** Go (root module + `foundation` module), `jackc/pgx/v5`, `modernc.org/sqlite`, `go-chi/chi/v5`, stdlib `log/slog`, stdlib `crypto/sha256`, stdlib `crypto/rand`, stdlib `encoding/base64`. No new third-party deps.

---

## Reference materials

The implementing agent works from a clean context. Before executing tasks, read these files in full:

- `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md` — the spec this plan implements. Single source of truth for everything ambiguous in this plan.
- `CLAUDE.md` (project root) — module layout, depguard rules, vocabulary, gotchas. **Required reading.**
- `.claude/rules/rules.md` and `.claude/rules/cold-read-cheatsheet.md` — coding conventions.
- `.claude/rules/citation-grammar.md` — applies to the agent's user-facing prose, not source code; useful when writing implementation-notes entries.
- `control/controlapi/app.go` — chi router, middleware wiring, `AppDeps` struct (already has `Auth Authenticator` field for this work to fill in).
- `control/controlapi/auth.go` — current scaffolding `Authenticator` interface and `authMiddleware` to be replaced (44 lines today).
- `control/config/controlapi.go` — `StartControlAPI` wiring; the place to construct the real `Authenticator` from the persistence handle.
- `cmd/rimsky-control-api/main.go` — control-api binary entry.
- `cmd/rimsky-cli/main.go` — current CLI binary entry (to be renamed).
- `mcp-servers/control-api/{config,server,tools}.go` and `mcp-servers/control-api/cmd/rimsky-mcp-control-api/` — the standalone module being folded in.
- `foundation/persistence/postgres/migrations/` and `foundation/persistence/sqlite/migrations/` — the per-driver migration directories. Post-flatten (2026-05-17), both driver dirs contain only `001-baseline.sql` + `embed.go`. New schema in pre-v1 lands by editing the baseline directly rather than adding numbered migrations.
- `foundation/persistence/tables.go` — the `Tables` umbrella interface where the new `APIKeyTable` accessor is added.
- `foundation/persistence/postgres/migrations/001-baseline.sql` — the post-flatten baseline; section headers (`===== <table> =====`) show the per-table-section style to mirror when adding the new `rimsky_api_keys` section.
- The full list of control-api routes is in the spec's "Action grammar" table; spec is authoritative.

Do not assume the implementer has read the brainstorm conversation. They have not. Everything they need is on the page, in the spec, or in the files above.

## Pre-resolved design decisions

These were settled during brainstorm. Do not re-litigate.

- **Auth model:** API keys via `Authorization: Bearer rk_<44-char-base64url>`; SHA-256 hashed at rest; required immutable unique `name`; per-key JSONB permission grant. No external IdP integration.
- **Permission grammar:** verb-noun action strings; wildcards `*`, `<noun>:*`, `*:<verb>` with the colon retained as part of the match boundary; first-match-wins evaluation; `mode: execute | dry_run` per entry.
- **Bundled roles:** CLI-side JSON templates only; server has no role concept. V1 ships five: `admin`, `operator`, `read-only`, `agent-supervisor`, `publisher-service`.
- **Implicit anonymous mode:** derived from "zero active rows in `rimsky_api_keys`"; no config knob.
- **Bootstrap:** `rimsky auth init` is a thin HTTP call to `POST /auth/keys` while in anonymous mode. No direct-DB CLI subcommands in this plan; break-glass via `psql` is documented.
- **MCP hosting:** in-control-api at `POST /mcp`; standalone module folds in and is deleted; tools-only V1 (no resources, prompts, subscriptions); HTTP transport only (no stdio).
- **Rotation:** `rimsky auth rotate` mints a new key with same name + permissions, sets `revoke_at = now() + grace` on old key. Sweep in `cmd:rimsky-scheduler` revokes past-grace keys.
- **Audit:** rows in `rimsky_events` with `auth.*` kinds. No new tables. `request_params` stored verbatim.
- **Out of scope:** rate limits, action+resource scoping, confirmation gates, escalation channels, operator-defined-roles-via-API, MCP resources/prompts/stdio, IdP integration, `sensor-rimsky-lifecycle`, consolidation of `rimsky-migrate` / conformance binaries.

## Conventions used in this plan

- File paths are absolute relative to the rimsky repo root (`submodules/rimsky/` if you're inside zonebase; the rimsky repo root is the project root for everything here).
- Each task lists **Files** (existing or new) and **Steps**. Steps are concrete edits.
- Each task ends with **Verify** — a command the implementer runs whose output settles whether the task is done.
- "Test scaffolding before implementation" is the default rhythm: write the failing test, run it, implement, run again.
- Tests use the existing `internal/pgtest/` testcontainers fixture (root module) and `foundation/internal/pgtest/` (foundation module) for Postgres-backed scenarios; in-memory sqlite is fine for unit tests.
- After any Go change: run `make tidy && make build-all && make lint && make test-all`. After race-sensitive paths: `go test ... -race -count=3`.
- New files declare the standard licensing header used elsewhere in the package (copy from a sibling file).

## Critical path and dependencies

The plan executes start-to-finish in one run. Sections roughly sequence as follows; inside each section tasks generally have a linear data dependency.

1. Section A (schema + persistence) — foundation; everything else depends on it.
2. Section B (foundation types) — pure types; depends only on stdlib.
3. Section C (action registry) — pure types; depends on B (for action-string constants).
4. Section D (auth middleware) — depends on A, B, C.
5. Section E (audit emission) — depends on B (payload types), independent of D.
6. Section F (auth endpoints) — depends on A, B, C, D, E.
7. Section G (rotation sweep) — depends on A.
8. Section H (MCP fold-in) — depends on C, D, E (and the action registry being populated against existing handler dispatchers).
9. Section I (CLI rename) — independent of A-H mechanically, but the new auth subcommands in J depend on the rename.
10. Section J (CLI auth subcommands) — depends on F (endpoints), I (binary rename).
11. Section K (per-handler dry-run wiring) — depends on D (middleware passes `dry_run` through context). Each handler edited independently.
12. Section L (scenario tests) — depends on everything above.
13. Section M (smoke + conformance) — depends on everything above.
14. Section N (docs + concept catalog) — final pass; everything above must be in place.

---

# Section A — Schema and persistence

### A1. Postgres schema: append `rimsky_api_keys` to `001-baseline.sql`

**Files:**
- `foundation/persistence/postgres/migrations/001-baseline.sql` (modified)

**Background.** Per the 2026-05-17 migration-flatten housekeeping pass, schema changes in pre-v1 land by direct edits to the baseline migration rather than enumerated `00N-…` migrations. Add the new table at an appropriate position in the baseline (near `rimsky_events`, since it shares the audit/event surface).

**Steps:**

1. Open `foundation/persistence/postgres/migrations/001-baseline.sql`. Locate a logical insertion point — the file is structured as `===== <table_name> =====` sections; insert a new `===== rimsky_api_keys =====` section after `rimsky_events`.
2. Add the new section with a comment block explaining: what the table is for, why each index exists (especially the partial unique-name index — call out the rotation-grace mechanic), reference to `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md` "Persistence schema" section.
3. DDL:

```sql
-- =====  rimsky_api_keys  =====
-- API keys for Bearer-token auth. Hashed at rest (SHA-256). The
-- partial unique-name index excludes revoked + rotation-grace rows
-- so a rotation can mint a new row with the same name while the old
-- one is still active during its grace window. See spec
-- .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md
-- "Persistence schema".

CREATE TABLE rimsky_api_keys (
    id                 UUID         NOT NULL PRIMARY KEY,
    key_hash           BYTEA        NOT NULL,
    name               TEXT         NOT NULL,
    permissions        JSONB        NOT NULL,
    created_at         TIMESTAMPTZ  NOT NULL,
    created_by_key_id  UUID         NULL,
    last_used_at       TIMESTAMPTZ  NULL,
    expires_at         TIMESTAMPTZ  NULL,
    revoke_at          TIMESTAMPTZ  NULL,
    revoked_at         TIMESTAMPTZ  NULL,
    CONSTRAINT rimsky_api_keys_key_hash_unique UNIQUE (key_hash),
    CONSTRAINT rimsky_api_keys_created_by_fk
        FOREIGN KEY (created_by_key_id) REFERENCES rimsky_api_keys(id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX rimsky_api_keys_active_name_idx
    ON rimsky_api_keys (name)
    WHERE revoked_at IS NULL AND revoke_at IS NULL;

CREATE INDEX rimsky_api_keys_revoke_at_pending_idx
    ON rimsky_api_keys (revoke_at)
    WHERE revoke_at IS NOT NULL AND revoked_at IS NULL;

CREATE INDEX rimsky_api_keys_active_status_idx
    ON rimsky_api_keys (revoked_at, expires_at, revoke_at);
```

**Verify:** `go test ./foundation/persistence/postgres/... -run TestMigrations -count=1` (or whatever the migration-runner integration test is named — grep `func TestMigration` under `foundation/persistence/postgres/`).

---

### A2. SQLite schema: append `rimsky_api_keys` to `001-baseline.sql`

**Files:**
- `foundation/persistence/sqlite/migrations/001-baseline.sql` (modified)

**Steps:**

1. Open the sqlite baseline. Find the analogous insertion point (the file mirrors the postgres baseline section-for-section; insert after the `rimsky_events` section).
2. Add the `===== rimsky_api_keys =====` section in sqlite dialect:
   - `UUID` → `TEXT NOT NULL` (sqlite stores UUIDs as TEXT in this codebase).
   - `BYTEA` → `BLOB NOT NULL`.
   - `TIMESTAMPTZ` → `TIMESTAMP` (sqlite uses TEXT-encoded RFC3339 in this codebase; grep the postgres baseline's `rimsky_events.received_at` mapping for the convention).
   - `JSONB` → match what other JSONB columns in the sqlite baseline use (grep `rimsky_node_runs.aggregation_policy` for the convention).
   - Partial unique index: `CREATE UNIQUE INDEX ... WHERE ...` — sqlite supports partial indexes.
   - FK: sqlite requires `PRAGMA foreign_keys=ON` at session level; the migration runner already handles that.

**Verify:** `go test ./foundation/persistence/sqlite/... -run TestMigrations -count=1`.

---

### A3. `APIKey` row type + `APIKeyTable` interface

**Files:**
- `foundation/persistence/api_keys.go` (new — driver-agnostic types and interface)

**Steps:**

1. Declare the row struct:

```go
package persistence

import (
    "context"
    "encoding/json"
    "time"

    "github.com/fallguyconsulting/rimsky/foundation/shared"
)

// APIKey is one row of rimsky_api_keys.
type APIKey struct {
    ID              shared.UUID
    KeyHash         []byte           // SHA-256(plaintext)
    Name            string
    Permissions     json.RawMessage  // grant entries; opaque to persistence
    CreatedAt       time.Time
    CreatedByKeyID  *shared.UUID     // nil for anonymous-mode mints
    LastUsedAt      *time.Time
    ExpiresAt       *time.Time
    RevokeAt        *time.Time
    RevokedAt       *time.Time
}
```

2. Declare the table interface — read/write operations only; semantics intentionally live in the runtime layer:

```go
// APIKeyTable is the per-row-type accessor for rimsky_api_keys.
// All methods return zero-value+nil when the row is not found; callers
// distinguish via the returned bool or by err == ErrAPIKeyNotFound.
type APIKeyTable interface {
    // Insert adds a new row. Returns ErrAPIKeyNameTaken if the active
    // unique-name index conflicts.
    Insert(ctx context.Context, k APIKey) error

    // GetByID fetches by primary key (revoked + expired rows included).
    GetByID(ctx context.Context, id shared.UUID) (APIKey, bool, error)

    // GetByName fetches the active row for this name (the row that's in
    // the partial unique-name index — i.e., revoked_at IS NULL AND
    // revoke_at IS NULL). Returns ok=false if no active row exists.
    GetByName(ctx context.Context, name string) (APIKey, bool, error)

    // GetByHash fetches by SHA-256 hash. Returns ok=false on no match.
    // Includes rows with revoke_at set (in-grace) and rows that may have
    // expired — the caller applies the active-status predicate.
    GetByHash(ctx context.Context, hash []byte) (APIKey, bool, error)

    // List enumerates rows. If includeRevoked=false, filters
    // revoked_at IS NULL. nameFilter is an optional glob applied to
    // name; "" means no filter.
    List(ctx context.Context, includeRevoked bool, nameFilter string) ([]APIKey, error)

    // ActiveCount returns the count of rows matching the active-status
    // predicate: revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now)
    // AND (revoke_at IS NULL OR revoke_at > now). Used by the
    // anonymous-mode predicate and the revoke-the-last-key guard.
    ActiveCount(ctx context.Context) (int, error)

    // MarkRevoked sets revoked_at = now on the given id. Idempotent; if
    // the row is already revoked, returns nil with no row mutation.
    MarkRevoked(ctx context.Context, id shared.UUID, now time.Time) error

    // SetRevokeAt sets revoke_at = at on the given id. Used by rotation.
    SetRevokeAt(ctx context.Context, id shared.UUID, at time.Time) error

    // SweepRotationGrace sets revoked_at = now on all rows where
    // revoke_at <= now AND revoked_at IS NULL. Returns the swept rows
    // (id + name) so the caller can emit audit events.
    SweepRotationGrace(ctx context.Context, now time.Time) ([]APIKey, error)

    // UpdateLastUsed best-effort updates last_used_at = now on the row.
    // Best-effort: implementations may swallow errors to avoid stalling
    // the auth path; callers should not branch on the returned error.
    UpdateLastUsed(ctx context.Context, id shared.UUID, now time.Time) error

    // WithTx runs fn inside a database transaction, passing a
    // transactional APIKeyTable view that scopes every method to
    // that tx. Used by handleRotateKey (F5) to atomically (a) set
    // revoke_at on the existing row and (b) insert the new row;
    // the partial unique-name index requires step (a) to happen
    // before step (b) in the same transaction so the new row can
    // share the name.
    WithTx(ctx context.Context, fn func(tx APIKeyTable) error) error
}

// Sentinel errors for APIKeyTable.
var (
    ErrAPIKeyNotFound  = errAPIKeyNotFound
    ErrAPIKeyNameTaken = errAPIKeyNameTaken
)
```

3. Declare the sentinel error values in a private block beside the exported aliases; copy the style from `foundation/persistence/claim_handles.go` if it has comparable sentinels (grep `var Err`).

**Verify:** `cd foundation && go build ./...` — interface compiles.

---

### A4. Wire `APIKeyTable` into the `Tables` umbrella

**Files:**
- `foundation/persistence/tables.go` (modified — add accessor method)

**Steps:**

1. Open `foundation/persistence/tables.go`. Locate the `Tables` interface declaration.
2. Add a method `APIKeys() APIKeyTable` alongside the existing per-row-type accessors (e.g., `ClaimHandles() ClaimHandleTable`).
3. If the `Tables` interface has a doc-comment listing the accessors, update it.

**Verify:** `cd foundation && go build ./...`. Compilation will fail for the postgres and sqlite drivers because they don't implement `APIKeys()` yet — that's expected; A5 and A6 fix it.

---

### A5. Postgres impl of `APIKeyTable`

**Files:**
- `foundation/persistence/postgres/api_keys.go` (new)
- `foundation/persistence/postgres/backend.go` (modified — register the accessor)

**Steps:**

1. Create `foundation/persistence/postgres/api_keys.go` with package header and imports (`pgx/v5`, `context`, `encoding/json`, `time`, `github.com/fallguyconsulting/rimsky/foundation/persistence`, `github.com/fallguyconsulting/rimsky/foundation/shared`).
2. Implement a `pgAPIKeys` struct that holds the `*pgxpool.Pool` (or whatever the existing accessors use — copy the pattern from `foundation/persistence/postgres/claim_handles.go`).
3. Implement each method on the interface. Specifically:
   - `Insert` — `INSERT INTO rimsky_api_keys (id, key_hash, name, permissions, created_at, created_by_key_id, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`. On unique-violation error (`pgerrcode.UniqueViolation`), inspect the constraint name:
     - `rimsky_api_keys_active_name_idx` → return `ErrAPIKeyNameTaken` (the active-name partial unique index collided; an active key already has this name).
     - `rimsky_api_keys_key_hash_unique` → this is a hash collision, which is essentially impossible (SHA-256 over 264-bit random input); log loudly and return a generic wrapped error rather than `ErrAPIKeyNameTaken` (the latter would mislead the caller).
     - Any other constraint name → wrap and return as a generic error.
   - `GetByID` — `SELECT * FROM rimsky_api_keys WHERE id = $1`. Scan into struct.
   - `GetByName` — `SELECT * FROM rimsky_api_keys WHERE name = $1 AND revoked_at IS NULL AND revoke_at IS NULL`.
   - `GetByHash` — `SELECT * FROM rimsky_api_keys WHERE key_hash = $1`. Note: does NOT filter on active status — the caller does (so it can distinguish "expired vs revoked vs in-grace" for audit `denial_reason`).
   - `List` — `SELECT * FROM rimsky_api_keys ORDER BY created_at DESC` plus optional `WHERE revoked_at IS NULL` and `WHERE name LIKE $1` (translate `*` glob → SQL `%` wildcards).
   - `ActiveCount` — `SELECT COUNT(*) FROM rimsky_api_keys WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now()) AND (revoke_at IS NULL OR revoke_at > now())`.
   - `MarkRevoked` — `UPDATE rimsky_api_keys SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`. Idempotent — no error if zero rows affected.
   - `SetRevokeAt` — `UPDATE rimsky_api_keys SET revoke_at = $2 WHERE id = $1`.
   - `SweepRotationGrace` — `UPDATE rimsky_api_keys SET revoked_at = $1 WHERE revoke_at IS NOT NULL AND revoke_at <= $1 AND revoked_at IS NULL RETURNING id, name`. Scan the returned rows into `[]APIKey` with only `ID` and `Name` populated; this is sufficient for the audit emit.
   - `UpdateLastUsed` — `UPDATE rimsky_api_keys SET last_used_at = $2 WHERE id = $1`. Swallow errors; log at debug.
4. Add a constructor `NewAPIKeys(pool ...) APIKeyTable` matching the sibling-file pattern.
5. In `backend.go`, locate the `Tables()` struct/method. Add a `apiKeys` field and the `APIKeys()` accessor method that returns it.

**Verify:** `cd foundation && go build ./...` is clean. `go test ./foundation/persistence/postgres/... -run TestAPIKeys -count=1` — but this test doesn't exist yet; A7 writes it.

---

### A6. SQLite impl of `APIKeyTable`

**Files:**
- `foundation/persistence/sqlite/api_keys.go` (new)
- `foundation/persistence/sqlite/backend.go` (modified — register the accessor)

**Steps:**

1. Mirror A5 in sqlite. Differences:
   - Use `database/sql` style with the existing sqlite driver wiring (grep `foundation/persistence/sqlite/claim_handles.go` for the pattern).
   - UUIDs encoded as TEXT.
   - Timestamps as RFC3339 TEXT (use `time.RFC3339Nano`).
   - Glob translation same as Postgres (`*` → `%`).
   - `now()` in SQL becomes `CURRENT_TIMESTAMP` or the caller passes `now` explicitly — match what the sqlite baseline + sibling accessors do (grep `foundation/persistence/sqlite/claim_handles.go` and adjacent `*.go` files for the convention; the post-flatten codebase has the answer in baseline plus existing accessors).
   - Sentinel-error mapping: SQLite returns `constraint failed` strings; grep `errors.Is(err, ...)` patterns in `foundation/persistence/sqlite/claim_handles.go` for how to distinguish unique-violation by constraint name (sqlite doesn't expose the constraint name directly; the existing code parses the error message — use the same mechanism).
2. In `backend.go`, mirror the A5 backend wiring.

**Verify:** `cd foundation && go build ./...` is clean.

---

### A7. Conformance test for `APIKeyTable`

**Files:**
- `foundation/persistence/conformance/api_keys.go` (new — driver-agnostic test suite)

**Steps:**

1. Look at `foundation/persistence/conformance/queue_in_tx.go` or another sibling for the test-style pattern. The convention is: one exported function `TestAPIKeys(t *testing.T, mk func() Tables)` that the postgres and sqlite test packages each invoke with their fixture's `Tables` constructor.
2. The conformance test should cover:
   - Insert + GetByID round-trip.
   - GetByName returns only active rows; inactive (revoked or in-grace-expired) rows don't surface.
   - GetByHash returns rows regardless of active status (so caller can apply predicate).
   - List with and without `includeRevoked`; name glob filter.
   - ActiveCount under: empty table → 0; one active row → 1; one active + one revoked → 1; one row with `revoke_at > now` → 1 (still active during grace); one row with `revoke_at <= now AND revoked_at IS NULL` → 0 (grace expired, sweep pending).
   - MarkRevoked is idempotent (call twice; second is no-op).
   - SetRevokeAt + SweepRotationGrace: row with `revoke_at = past` and `revoked_at IS NULL` gets swept, returns the row in the result.
   - Unique-name index: two active rows with the same name → second insert returns `ErrAPIKeyNameTaken`. Insert a row, set `revoke_at` on it, insert another row with the same name → succeeds (because the first row dropped out of the partial unique index).
   - **WithTx** atomicity: inside `WithTx(ctx, func(tx APIKeyTable) error { ... })`, calling `SetRevokeAt` then `Insert` with the same name succeeds (mirrors the rotation flow). Returning an error from the callback rolls back: post-rollback `GetByID` on the would-be-inserted row returns ok=false, and the original row's `revoke_at` is unchanged. A panic inside the callback also rolls back (defer/recover semantics; the implementation should re-panic after rollback).
3. Add invocations:
   - `foundation/persistence/postgres/api_keys_test.go` — `func TestAPIKeysPostgres(t *testing.T)` invoking the conformance suite with a pgtest fixture.
   - `foundation/persistence/sqlite/api_keys_test.go` — `func TestAPIKeysSQLite(t *testing.T)` invoking with the sqlite fixture.

**Verify:** `go test ./foundation/persistence/postgres/... -run TestAPIKeys -count=1` and `go test ./foundation/persistence/sqlite/... -run TestAPIKeys -count=1`. Both pass.

---

# Section B — Foundation types: keys, grants, permissions

### B1. Plaintext format + hashing helpers

**Files:**
- `foundation/auth/plaintext.go` (new)
- `foundation/auth/plaintext_test.go` (new)

**Steps:**

1. Create the `foundation/auth/` package. It is part of the foundation Go module; imports only stdlib.
2. Implement:

```go
// Package auth provides API-key plaintext format helpers (mint, hash, parse)
// shared by control-api auth middleware and the rimsky CLI bootstrap path.
// Pure functions; no I/O.
package auth

import (
    "crypto/rand"
    "crypto/sha256"
    "encoding/base64"
    "errors"
    "strings"
)

const (
    // Prefix on every plaintext API key. Stable; do not change.
    Prefix = "rk_"

    // EntropyBytes is the size of the random suffix in bytes before
    // base64url encoding. 33 bytes encodes to 44 base64url chars.
    EntropyBytes = 33

    // HashSize is the SHA-256 digest size (32 bytes).
    HashSize = sha256.Size
)

var ErrInvalidPlaintext = errors.New("auth: invalid api-key plaintext")

// Mint generates a fresh plaintext key. Returns the plaintext and its
// SHA-256 hash. The plaintext is the only artifact that ever leaves
// rimsky in a form the operator can re-present.
func Mint() (plaintext string, hash [HashSize]byte, err error) {
    var buf [EntropyBytes]byte
    if _, err = rand.Read(buf[:]); err != nil {
        return "", [HashSize]byte{}, err
    }
    suffix := base64.RawURLEncoding.EncodeToString(buf[:])
    plaintext = Prefix + suffix
    hash = sha256.Sum256([]byte(plaintext))
    return plaintext, hash, nil
}

// Hash returns SHA-256(plaintext). Used at auth-middleware lookup time.
func Hash(plaintext string) [HashSize]byte {
    return sha256.Sum256([]byte(plaintext))
}

// ValidatePlaintext returns nil if the string is a structurally
// well-formed rimsky API-key plaintext (prefix + correctly-sized
// base64url suffix). Used by the auth middleware to short-circuit
// obviously-malformed tokens without a DB lookup.
func ValidatePlaintext(s string) error {
    if !strings.HasPrefix(s, Prefix) {
        return ErrInvalidPlaintext
    }
    suffix := strings.TrimPrefix(s, Prefix)
    raw, err := base64.RawURLEncoding.DecodeString(suffix)
    if err != nil || len(raw) != EntropyBytes {
        return ErrInvalidPlaintext
    }
    return nil
}
```

3. Tests in `plaintext_test.go`:
   - `Mint()` produces distinct plaintexts on successive calls.
   - `Mint()` plaintexts pass `ValidatePlaintext`.
   - `Hash(plaintext)` matches the hash returned by `Mint()`.
   - `ValidatePlaintext("not-a-key")` returns `ErrInvalidPlaintext`.
   - `ValidatePlaintext("rk_short")` returns `ErrInvalidPlaintext` (wrong length).

**Verify:** `cd foundation && go test ./auth/... -count=1`.

---

### B2. Grant entry data types and JSON parsing

**Files:**
- `foundation/auth/grant.go` (new)
- `foundation/auth/grant_test.go` (new)

**Steps:**

1. Implement:

```go
package auth

import (
    "encoding/json"
    "errors"
    "fmt"
)

// Mode is the per-grant-entry modifier.
type Mode string

const (
    ModeExecute Mode = "execute"
    ModeDryRun  Mode = "dry_run"
)

// GrantEntry is one entry in an API-key's permission grant.
// Forward-compatible: unknown fields are preserved in Extras so a
// future server reading a key minted by this server doesn't lose data.
// Today's parser ignores Extras for matching.
type GrantEntry struct {
    Action string `json:"action"`
    Mode   Mode   `json:"mode,omitempty"`

    // Extras carries any unknown JSON fields encountered during
    // unmarshal. Preserved on the wire; ignored by the permission
    // matcher. Lets V2 add `scope` / `rate_limit` etc. without a
    // schema migration.
    Extras map[string]json.RawMessage `json:"-"`
}

// Grant is the full grant on a key — an ordered list of entries.
// First-match-wins; ordering is significant for mode resolution.
type Grant []GrantEntry

var ErrInvalidGrant = errors.New("auth: invalid grant")

// UnmarshalJSON preserves unknown fields in Extras and validates
// the basic shape (action is a non-empty string; mode if set is a
// known value).
func (g *GrantEntry) UnmarshalJSON(data []byte) error {
    var raw map[string]json.RawMessage
    if err := json.Unmarshal(data, &raw); err != nil {
        return fmt.Errorf("grant entry: %w", err)
    }
    var actionStr string
    if v, ok := raw["action"]; ok {
        if err := json.Unmarshal(v, &actionStr); err != nil {
            return fmt.Errorf("grant entry: action: %w", err)
        }
    }
    if actionStr == "" {
        return fmt.Errorf("%w: action is required and must be non-empty", ErrInvalidGrant)
    }
    g.Action = actionStr
    delete(raw, "action")
    if v, ok := raw["mode"]; ok {
        var modeStr string
        if err := json.Unmarshal(v, &modeStr); err != nil {
            return fmt.Errorf("grant entry: mode: %w", err)
        }
        switch Mode(modeStr) {
        case ModeExecute, ModeDryRun:
            g.Mode = Mode(modeStr)
        default:
            return fmt.Errorf("%w: mode must be %q or %q (got %q)", ErrInvalidGrant, ModeExecute, ModeDryRun, modeStr)
        }
        delete(raw, "mode")
    }
    if len(raw) > 0 {
        g.Extras = raw
    }
    return nil
}

// MarshalJSON omits Mode if zero, omits Extras if empty.
func (g GrantEntry) MarshalJSON() ([]byte, error) {
    m := map[string]any{"action": g.Action}
    if g.Mode != "" {
        m["mode"] = g.Mode
    }
    for k, v := range g.Extras {
        m[k] = v
    }
    return json.Marshal(m)
}
```

2. Tests:
   - Round-trip: `{"action": "instance:read"}` → struct → JSON → struct, equal.
   - `{"action": "instance:read", "mode": "dry_run"}` round-trips.
   - `{"action": ""}` → `ErrInvalidGrant`.
   - `{}` (no action) → `ErrInvalidGrant`.
   - `{"action": "x", "mode": "weird"}` → `ErrInvalidGrant`.
   - `{"action": "x", "scope": {"template_tag": "y"}}` → succeeds; `Extras["scope"]` round-trips through MarshalJSON.

**Verify:** `cd foundation && go test ./auth/... -count=1 -run TestGrant`.

---

### B3. Action grammar: wildcard matcher and validation

**Files:**
- `foundation/auth/action.go` (new)
- `foundation/auth/action_test.go` (new)

**Steps:**

1. Implement:

```go
package auth

import (
    "errors"
    "fmt"
    "strings"
)

// ActionMatches returns true if entryAction matches requestAction per
// the wildcard rules:
//   - "*" matches anything
//   - "<noun>:*" matches any requestAction starting with "<noun>:"
//   - "*:<verb>" matches any requestAction ending with ":<verb>"
//   - otherwise requires exact-string match
//
// The colon is always part of the match boundary — "auth:*" does NOT
// match "authority:create".
func ActionMatches(entryAction, requestAction string) bool {
    if entryAction == "*" {
        return true
    }
    if entryAction == requestAction {
        return true
    }
    if strings.HasSuffix(entryAction, ":*") {
        prefix := entryAction[:len(entryAction)-1] // keeps trailing ":"
        return strings.HasPrefix(requestAction, prefix)
    }
    if strings.HasPrefix(entryAction, "*:") {
        suffix := entryAction[1:] // keeps leading ":"
        return strings.HasSuffix(requestAction, suffix)
    }
    return false
}

// ValidateActionString returns nil if entryAction is well-formed:
// exact string, "*", "<noun>:*", or "*:<verb>". Infix wildcards
// ("foo:*:bar") and embedded asterisks ("foo*bar") are rejected.
func ValidateActionString(entryAction string) error {
    if entryAction == "" {
        return errors.New("action string is empty")
    }
    if entryAction == "*" {
        return nil
    }
    // Exact strings: no '*' anywhere.
    if !strings.Contains(entryAction, "*") {
        if !strings.Contains(entryAction, ":") {
            return fmt.Errorf("action %q must contain a ':' separator", entryAction)
        }
        return nil
    }
    // Prefix wildcard: "<noun>:*"
    if strings.HasSuffix(entryAction, ":*") {
        prefix := entryAction[:len(entryAction)-2]
        if prefix == "" || strings.Contains(prefix, "*") || strings.Contains(prefix, ":") {
            return fmt.Errorf("action %q: noun-prefix wildcard must be <noun>:* with no embedded ':' or '*'", entryAction)
        }
        return nil
    }
    // Suffix wildcard: "*:<verb>"
    if strings.HasPrefix(entryAction, "*:") {
        suffix := entryAction[2:]
        if suffix == "" || strings.Contains(suffix, "*") || strings.Contains(suffix, ":") {
            return fmt.Errorf("action %q: verb-suffix wildcard must be *:<verb> with no embedded ':' or '*'", entryAction)
        }
        return nil
    }
    return fmt.Errorf("action %q: unsupported wildcard shape (only '*', '<noun>:*', '*:<verb>' allowed)", entryAction)
}
```

2. Tests — extensive table-driven:
   - `ActionMatches("*", "node:read")` → true.
   - `ActionMatches("node:read", "node:read")` → true.
   - `ActionMatches("node:read", "node:write")` → false.
   - `ActionMatches("auth:*", "auth:create")` → true.
   - `ActionMatches("auth:*", "authority:create")` → false (colon-boundary).
   - `ActionMatches("auth:*", "auth:rotate")` → true.
   - `ActionMatches("*:read", "node:read")` → true.
   - `ActionMatches("*:read", "node:readwrite")` → false.
   - `ActionMatches("*:read", "node:read:extra")` → false.
   - `ValidateActionString("instance:create")` → nil.
   - `ValidateActionString("*")` → nil.
   - `ValidateActionString("instance:*")` → nil.
   - `ValidateActionString("*:read")` → nil.
   - `ValidateActionString("")` → err.
   - `ValidateActionString("nocolon")` → err.
   - `ValidateActionString("instance:*:thing")` → err.
   - `ValidateActionString("ins*ance:read")` → err.
   - `ValidateActionString("foo:bar:*")` → err (the prefix part has a colon).

**Verify:** `cd foundation && go test ./auth/... -count=1 -run TestAction`.

---

### B4. Permission check + grant validation

**Files:**
- `foundation/auth/check.go` (new)
- `foundation/auth/check_test.go` (new)

**Steps:**

1. Implement:

```go
package auth

import "fmt"

// CheckResult describes the outcome of CheckGrant.
type CheckResult struct {
    Allowed    bool
    Mode       Mode  // ModeExecute if entry didn't set mode and the request matched
    MatchedIdx int   // index into the grant of the matching entry; -1 if not allowed
}

// CheckGrant runs the first-match-wins algorithm against the grant
// for the given requestAction. Returns Allowed=false when no entry
// matches.
func CheckGrant(grant Grant, requestAction string) CheckResult {
    for i, e := range grant {
        if ActionMatches(e.Action, requestAction) {
            mode := e.Mode
            if mode == "" {
                mode = ModeExecute
            }
            return CheckResult{Allowed: true, Mode: mode, MatchedIdx: i}
        }
    }
    return CheckResult{Allowed: false, MatchedIdx: -1}
}

// ValidateGrant runs ValidateActionString over every entry. Used at
// POST /auth/keys time to reject grants referencing unknown action
// patterns. The known-action-string check happens against the
// per-process action registry, not in foundation/auth.
func ValidateGrant(grant Grant) error {
    if len(grant) == 0 {
        return fmt.Errorf("grant is empty")
    }
    for i, e := range grant {
        if err := ValidateActionString(e.Action); err != nil {
            return fmt.Errorf("entry %d: %w", i, err)
        }
    }
    return nil
}
```

2. Tests:
   - Empty grant → CheckResult{Allowed: false}.
   - Grant `[{"*"}]` allows any action with ModeExecute.
   - Grant `[{"*:read"}]` allows `node:read`, denies `node:write`.
   - Grant `[{"instance:create", mode: dry_run}, {"*"}]` — request `instance:create` returns dry_run; request `node:read` returns execute (matches second entry).
   - Grant `[{"*"}, {"instance:create", mode: dry_run}]` — request `instance:create` returns execute (first match wins) — this is the "specific entries should appear first" callout.
   - `ValidateGrant` rejects empty grant.
   - `ValidateGrant` rejects entries with bad action strings.

**Verify:** `cd foundation && go test ./auth/... -count=1 -run TestCheck`.

---

### B5. Anonymous identity helper

**Files:**
- `foundation/auth/identity.go` (new)

**Steps:**

1. Implement:

```go
package auth

import "github.com/fallguyconsulting/rimsky/foundation/shared"

// IdentityKind tags audit records.
type IdentityKind string

const (
    IdentityAPIKey    IdentityKind = "api_key"
    IdentityAnonymous IdentityKind = "anonymous"
)

// AnonymousKeyName is the synthetic key_name carried in audit
// records for requests served under implicit anonymous mode.
const AnonymousKeyName = "anonymous"

// Identity describes the caller of a request, as resolved by the
// auth middleware. KeyID is nil for anonymous identities.
type Identity struct {
    KeyID       *shared.UUID
    KeyName     string
    Kind        IdentityKind
    Permissions Grant
}

// AnonymousIdentity returns the synthetic identity used when no
// active keys exist. It carries the admin grant.
func AnonymousIdentity() Identity {
    return Identity{
        KeyID:       nil,
        KeyName:     AnonymousKeyName,
        Kind:        IdentityAnonymous,
        Permissions: Grant{{Action: "*"}},
    }
}
```

**Verify:** `cd foundation && go build ./auth/...`.

---

### B6. Audit event-kind constants + payload struct types

**Files:**
- `foundation/auth/audit.go` (new)

**Steps:**

1. Implement:

```go
package auth

import (
    "encoding/json"
    "time"

    "github.com/fallguyconsulting/rimsky/foundation/shared"
)

// Event kinds for rimsky_events.kind. See spec section "Audit" for
// payload shapes.
const (
    EventAccessAttempted = "auth.access_attempted"
    EventAccessDenied    = "auth.access_denied"
    EventKeyCreated      = "auth.key_created"
    EventKeyRevoked      = "auth.key_revoked"
    EventKeyRotated      = "auth.key_rotated"
)

// DenialReason matches the spec's auth.access_denied.denial_reason
// enum exactly.
type DenialReason string

const (
    DenialNoToken          DenialReason = "no_token"
    DenialInvalidToken     DenialReason = "invalid_token"
    DenialExpiredToken     DenialReason = "expired_token"
    DenialRevokedToken     DenialReason = "revoked_token"
    DenialPermissionDenied DenialReason = "permission_denied"
)

// AccessAttemptedPayload is the JSONB body of auth.access_attempted.
type AccessAttemptedPayload struct {
    KeyID          *shared.UUID    `json:"key_id"`
    KeyName        string          `json:"key_name"`
    IdentityKind   IdentityKind    `json:"identity_kind"`
    ProtocolSkin   string          `json:"protocol_skin"` // "http" | "mcp"
    Action         string          `json:"action"`
    RequestPath    string          `json:"request_path"`
    RequestMethod  string          `json:"request_method"`
    RequestParams  json.RawMessage `json:"request_params,omitempty"`
    ResponseStatus int             `json:"response_status"`
    Mode           Mode            `json:"mode,omitempty"`
    Executed       bool            `json:"executed"`
    DurationMS     int64           `json:"duration_ms"`
    ClientIP       string          `json:"client_ip,omitempty"`
    UserAgent      string          `json:"user_agent,omitempty"`
}

// AccessDeniedPayload is the JSONB body of auth.access_denied.
// Most fields mirror AccessAttemptedPayload; per spec "Population
// rules for denial rows" some fields are nullable depending on whether
// denial happened pre- or post-action-resolution.
type AccessDeniedPayload struct {
    KeyID          *shared.UUID    `json:"key_id"`
    KeyName        *string         `json:"key_name"`
    IdentityKind   *IdentityKind   `json:"identity_kind"`
    ProtocolSkin   string          `json:"protocol_skin"`
    Action         *string         `json:"action"`
    RequestPath    string          `json:"request_path"`
    RequestMethod  string          `json:"request_method"`
    RequestParams  json.RawMessage `json:"request_params,omitempty"`
    ResponseStatus int             `json:"response_status"`
    Mode           *Mode           `json:"mode"`
    Executed       bool            `json:"executed"`
    DurationMS     int64           `json:"duration_ms"`
    ClientIP       string          `json:"client_ip,omitempty"`
    UserAgent      string          `json:"user_agent,omitempty"`
    DenialReason   DenialReason    `json:"denial_reason"`
}

// KeyCreatedPayload is the JSONB body of auth.key_created.
type KeyCreatedPayload struct {
    KeyID          shared.UUID  `json:"key_id"`
    KeyName        string       `json:"key_name"`
    Permissions    Grant        `json:"permissions"`
    CreatedByKeyID *shared.UUID `json:"created_by_key_id"`
    ExpiresAt      *time.Time   `json:"expires_at"`
}

// KeyRevokedReason matches the spec's auth.key_revoked.reason enum.
type KeyRevokedReason string

const (
    RevokeReasonManual         KeyRevokedReason = "manual"
    RevokeReasonRotationGrace  KeyRevokedReason = "rotation_grace"
    RevokeReasonExpired        KeyRevokedReason = "expired"
)

// KeyRevokedPayload is the JSONB body of auth.key_revoked.
type KeyRevokedPayload struct {
    KeyID          shared.UUID      `json:"key_id"`
    KeyName        string           `json:"key_name"`
    RevokedByKeyID *shared.UUID     `json:"revoked_by_key_id"`
    Reason         KeyRevokedReason `json:"reason"`
}

// KeyRotatedPayload is the JSONB body of auth.key_rotated.
type KeyRotatedPayload struct {
    OldKeyID shared.UUID `json:"old_key_id"`
    NewKeyID shared.UUID `json:"new_key_id"`
    Name     string      `json:"name"`
    RevokeAt time.Time   `json:"revoke_at"`
}
```

**Verify:** `cd foundation && go build ./auth/...`.

---

# Section C — Action registry

### C1. Action registry struct + registration API

**Files:**
- `control/controlapi/actions.go` (new — registry types + canonical list)
- `control/controlapi/actions_test.go` (new — tests)

**Steps:**

1. Implement the registry:

```go
package controlapi

import (
    "fmt"
    "net/http"
    "strings"
    "sync"

    "github.com/fallguyconsulting/rimsky/foundation/auth"
)

// ActionEntry is one row in the canonical action registry.
type ActionEntry struct {
    Action   string   // e.g. "node:invalidate"
    IsWrite  bool
    Routes   []Route  // HTTP routes that map to this action
    MCPTools []string // MCP tool names that map to this action
}

// Route is one HTTP method+path pair (chi-template format).
type Route struct {
    Method string // e.g. "POST"
    Path   string // chi pattern, e.g. "/nodes/{id}/invalidate"
}

// ActionRegistry holds the canonical action list and the two
// lookup tables (route → action, MCP tool → action). Construct
// with NewActionRegistry; populate via Register; freeze with Build.
type ActionRegistry struct {
    mu      sync.RWMutex
    built   bool
    entries map[string]ActionEntry         // by action string
    byRoute map[string]string              // "<METHOD> <pattern>" → action
    byTool  map[string]string              // MCP tool name → action
}

func NewActionRegistry() *ActionRegistry {
    return &ActionRegistry{
        entries: map[string]ActionEntry{},
        byRoute: map[string]string{},
        byTool:  map[string]string{},
    }
}

// Register adds one ActionEntry. Action strings must pass
// auth.ValidateActionString. Routes and tools must not collide
// with already-registered entries.
func (r *ActionRegistry) Register(e ActionEntry) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    if r.built {
        return fmt.Errorf("ActionRegistry already built; no further Register")
    }
    if err := auth.ValidateActionString(e.Action); err != nil {
        return fmt.Errorf("action %q: %w", e.Action, err)
    }
    if _, exists := r.entries[e.Action]; exists {
        return fmt.Errorf("action %q registered twice", e.Action)
    }
    for _, rt := range e.Routes {
        k := rt.Method + " " + rt.Path
        if prev, ok := r.byRoute[k]; ok {
            return fmt.Errorf("route %s collides between action %q and %q", k, prev, e.Action)
        }
        r.byRoute[k] = e.Action
    }
    for _, t := range e.MCPTools {
        if prev, ok := r.byTool[t]; ok {
            return fmt.Errorf("MCP tool %q collides between action %q and %q", t, prev, e.Action)
        }
        r.byTool[t] = e.Action
    }
    r.entries[e.Action] = e
    return nil
}

// Build freezes the registry. Required before lookups.
func (r *ActionRegistry) Build() {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.built = true
}

// ActionForRoute returns the action for an HTTP method + chi route
// pattern, or "" if no action is registered. Note: the lookup is
// pattern-based (the chi-template pattern), not URL-string based —
// the caller passes the routed pattern, not the request path.
func (r *ActionRegistry) ActionForRoute(method, pattern string) string {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.byRoute[method+" "+pattern]
}

// ActionForTool returns the action for an MCP tool name, or "".
func (r *ActionRegistry) ActionForTool(name string) string {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.byTool[name]
}

// IsKnownAction returns true if the exact action string is
// registered. Used by POST /auth/keys to reject grants referencing
// unknown actions.
func (r *ActionRegistry) IsKnownAction(action string) bool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    _, ok := r.entries[action]
    return ok
}

// AllActions returns every registered action in deterministic order.
// Used by tests and by the CLI's role-template expansion validation.
func (r *ActionRegistry) AllActions() []string {
    r.mu.RLock()
    defer r.mu.RUnlock()
    out := make([]string, 0, len(r.entries))
    for a := range r.entries {
        out = append(out, a)
    }
    // sort for stability
    sortStrings(out)
    return out
}

// AllTools returns every registered MCP tool name in deterministic
// order.
func (r *ActionRegistry) AllTools() []string {
    r.mu.RLock()
    defer r.mu.RUnlock()
    out := make([]string, 0, len(r.byTool))
    for t := range r.byTool {
        out = append(out, t)
    }
    sortStrings(out)
    return out
}

// EntryForTool returns the ActionEntry registered for an MCP tool
// name. The second return is false if the tool is unknown.
func (r *ActionRegistry) EntryForTool(name string) (ActionEntry, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    a, ok := r.byTool[name]
    if !ok {
        return ActionEntry{}, false
    }
    e, ok := r.entries[a]
    return e, ok
}

// sortStrings is a small inlinable sort to avoid the package import.
func sortStrings(s []string) {
    // stdlib sort.Strings; spelled out to keep imports localized.
    // Use the obvious sort.Strings — sortStrings exists only as a
    // single call site; rewrite to use sort.Strings directly.
    panic("replace with sort.Strings during implementation")
}
```

Replace the `sortStrings` panic stub with `sort.Strings(s)` (add `"sort"` to imports). The placeholder is here to make the structural review easier — the registry's body is the load-bearing part.

2. Tests:
   - Register a few entries; AllActions / AllTools return them sorted.
   - Register two entries with the same action → second errors.
   - Register two entries with overlapping routes → errors.
   - Register two entries with overlapping tools → errors.
   - After Build, Register errors.
   - ActionForRoute / ActionForTool / IsKnownAction work.
   - Registering with an invalid action string errors at validation.

**Verify:** `go test ./control/controlapi/... -count=1 -run TestActionRegistry`.

---

### C2. Populate the canonical V1 action registry

**Files:**
- `control/controlapi/actions.go` (extended — add `BuildV1Registry()`)

**Steps:**

1. At the bottom of `actions.go`, add:

```go
// BuildV1Registry returns the canonical V1 action registry frozen
// and ready for use. The list mirrors the spec's action grammar
// table; updates must be made here AND in
// .ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md.
func BuildV1Registry() *ActionRegistry {
    r := NewActionRegistry()
    for _, e := range v1Actions {
        if err := r.Register(e); err != nil {
            panic("BuildV1Registry: " + err.Error())
        }
    }
    r.Build()
    return r
}

var v1Actions = []ActionEntry{
    // Instances
    {Action: "instance:read",     IsWrite: false, Routes: []Route{{"GET", "/instances"}, {"GET", "/instances/{idOrKey}"}}, MCPTools: []string{"instance_list", "instance_get"}},
    {Action: "instance:create",   IsWrite: true,  Routes: []Route{{"POST", "/instances"}}, MCPTools: []string{"instance_create"}},
    {Action: "instance:terminate",IsWrite: true,  Routes: []Route{{"DELETE", "/instances/{idOrKey}"}}, MCPTools: []string{"instance_terminate"}},

    // Templates
    {Action: "template:read",       IsWrite: false, Routes: []Route{{"GET", "/templates"}, {"GET", "/templates/{id}"}}, MCPTools: []string{"template_list", "template_get"}},
    {Action: "template:register",   IsWrite: true,  Routes: []Route{{"POST", "/templates"}}, MCPTools: []string{"template_register"}},
    {Action: "template:deploy",     IsWrite: true,  Routes: []Route{{"POST", "/templates/{id}/deploy"}}, MCPTools: []string{"template_deploy"}},
    {Action: "template:undeploy",   IsWrite: true,  Routes: []Route{{"POST", "/templates/{id}/undeploy"}}, MCPTools: []string{"template_undeploy"}},
    {Action: "template:deregister", IsWrite: true,  Routes: []Route{{"DELETE", "/templates/{id}"}}, MCPTools: []string{"template_deregister"}},

    // Tags
    {Action: "tag:read",   IsWrite: false, Routes: []Route{{"GET", "/tags"}}, MCPTools: []string{"tag_list"}},
    {Action: "tag:create", IsWrite: true,  Routes: []Route{{"POST", "/tags"}}, MCPTools: []string{"tag_create"}},
    {Action: "tag:set",    IsWrite: true,  Routes: []Route{{"PUT", "/tags/{tag}"}}, MCPTools: []string{"tag_set"}},
    {Action: "tag:delete", IsWrite: true,  Routes: []Route{{"DELETE", "/tags/{tag}"}}, MCPTools: []string{"tag_delete"}},

    // Nodes
    {Action: "node:read",       IsWrite: false, Routes: []Route{{"GET", "/instances/{idOrKey}/nodes"}, {"GET", "/nodes/{id}"}}, MCPTools: []string{"node_list", "node_get"}},
    {Action: "node:invalidate", IsWrite: true,  Routes: []Route{{"POST", "/nodes/{id}/invalidate"}, {"POST", "/admin/instances/{instance}/nodes/{node_id}/invalidate"}}, MCPTools: []string{"node_invalidate"}},
    {Action: "node:reset",      IsWrite: true,  Routes: []Route{{"POST", "/nodes/{id}/reset"}}, MCPTools: []string{"node_reset"}},

    // Messages
    {Action: "message:send", IsWrite: true,  Routes: []Route{{"POST", "/instances/{id}/messages"}}, MCPTools: []string{"message_send"}},
    {Action: "message:read", IsWrite: false, Routes: []Route{{"GET", "/instances/{id}/messages"}, {"GET", "/messages/{id}"}}, MCPTools: []string{"message_list", "message_get"}},

    // Events
    {Action: "event:read", IsWrite: false, Routes: []Route{{"GET", "/events"}}, MCPTools: []string{"event_list"}},

    // Lineage
    {Action: "lineage:read",  IsWrite: false, Routes: []Route{
        {"GET", "/lineage/runs/{run_id}"},
        {"GET", "/lineage/runs/{run_id}/ancestors"},
        {"GET", "/lineage/runs/{run_id}/descendants"},
        {"GET", "/lineage/claims/{claim_handle_id}"},
        {"GET", "/lineage/claims/{claim_handle_id}/ancestors"},
        {"GET", "/lineage/by-source/{source_type}/{source_id}"},
        {"GET", "/lineage/by-producer/{executor_name}"},
    }, MCPTools: []string{"lineage_get"}},
    {Action: "lineage:prune", IsWrite: true,  Routes: []Route{{"POST", "/admin/lineage/prune"}}, MCPTools: []string{"lineage_prune"}},

    // Parked nodes
    {Action: "parked-node:read", IsWrite: false, Routes: []Route{{"GET", "/diagnostics/parked"}, {"GET", "/admin/diagnostics/parked-nodes"}}, MCPTools: []string{"parked_node_list"}},

    // Wait-sets
    {Action: "waitset:read", IsWrite: false, Routes: []Route{{"GET", "/admin/diagnostics/wait-sets"}}, MCPTools: []string{"waitset_list"}},

    // Claim holders
    {Action: "claim-holders:read", IsWrite: false, Routes: []Route{{"GET", "/lock-holders/{claim_handle_id}/claim-holders"}}, MCPTools: []string{"claim_holders_list"}},

    // Backfills
    {Action: "backfill:create", IsWrite: true,  Routes: []Route{{"POST", "/instances/{id}/backfills"}}, MCPTools: []string{"backfill_create"}},
    {Action: "backfill:read",   IsWrite: false, Routes: []Route{{"GET", "/instances/{id}/backfills"}, {"GET", "/backfills/{op_id}"}, {"GET", "/backfills/{op_id}/partitions"}}, MCPTools: []string{"backfill_list", "backfill_get", "backfill_partitions"}},
    {Action: "backfill:cancel", IsWrite: true,  Routes: []Route{{"POST", "/backfills/{op_id}/cancel"}}, MCPTools: []string{"backfill_cancel"}},

    // Assets
    {Action: "asset:read",        IsWrite: false, Routes: []Route{
        {"GET", "/instances/{id}/assets"},
        {"GET", "/instances/{id}/assets/{alias}"},
        {"GET", "/instances/{id}/assets/{alias}/versions"},
        {"GET", "/instances/{id}/assets/{alias}/materialization-history"},
    }, MCPTools: []string{"asset_list", "asset_get", "asset_versions", "asset_materialization_history"}},
    {Action: "asset:materialize", IsWrite: true,  Routes: []Route{{"POST", "/instances/{id}/assets/{alias}/materialize"}}, MCPTools: []string{"asset_materialize"}},
    {Action: "asset:delete",      IsWrite: true,  Routes: []Route{{"DELETE", "/instances/{id}/assets/{alias}"}}, MCPTools: []string{"asset_delete"}},

    // Diagnostics
    {Action: "diagnostics:read", IsWrite: false, Routes: []Route{{"GET", "/admin/diagnostics/held-frames"}}, MCPTools: []string{"held_frames_list"}},

    // Auth (self-administration)
    {Action: "auth:read",   IsWrite: false, Routes: []Route{{"GET", "/auth/keys"}, {"GET", "/auth/keys/{nameOrID}"}, {"GET", "/auth/status"}}, MCPTools: []string{"auth_list", "auth_get", "auth_status"}},
    {Action: "auth:create", IsWrite: true,  Routes: []Route{{"POST", "/auth/keys"}}, MCPTools: []string{"auth_create_key"}},
    {Action: "auth:revoke", IsWrite: true,  Routes: []Route{{"DELETE", "/auth/keys/{nameOrID}"}}, MCPTools: []string{"auth_revoke_key"}},
    {Action: "auth:rotate", IsWrite: true,  Routes: []Route{{"POST", "/auth/keys/{nameOrID}/rotate"}}, MCPTools: []string{"auth_rotate_key"}},

    // Observability (HTTP-only; no MCP tool surface in V1)
    // Covers all read-only routes mounted under /v1/observability/* by
    // the existing control/observability/ package. The chi-pattern below
    // matches every sub-route under that prefix; the action gate accepts
    // any method+path lookup that resolves to this pattern.
    {Action: "observability:read", IsWrite: false, Routes: []Route{{"GET", "/v1/observability/*"}}, MCPTools: []string{}},
}
```

**Note on `observability:read` vs the spec.** The spec's action grammar table did not enumerate `observability:read` — the table focused on actions reachable via the chi routes registered in `control/controlapi/`. The `/v1/observability/*` subtree is mounted via `deps.Observability` in `control/controlapi/app.go` and is read-only operator-infrastructure surface. Per the spec's "Existing endpoints — gating" section ("All existing control-api endpoints come under the auth middleware. ... No endpoint is exempt except `auth:status` ... and `/health` / `/ready`"), observability must be gated. We add `observability:read` to the registry here; it's implicitly covered by `*:read` in the bundled `read-only`, `operator`, `agent-supervisor`, and `admin` roles. When the spec is next reviewed (post-execute-plan), the action grammar table should grow this row.

2. Add tests that build the V1 registry and verify:
   - The registry builds without panic.
   - Every action listed in the spec's action grammar table is present in `BuildV1Registry().AllActions()` (subset check).
   - The registry MAY contain extra actions beyond the spec's table — explicitly, `observability:read` is supplemental (see note above and the explanatory note in `v1Actions`). The test should assert: `(set of spec-table actions) ⊆ (set of registry actions)`, AND `(set of registry actions) \ (set of spec-table actions) ⊆ {"observability:read"}` (the only allowed supplement in V1).
   - Hard-coded `specTableActions := []string{ /* the 34 actions from the spec */ }` lives in the test source so the assertion is unambiguous; when the spec table grows, this slice grows in lockstep. (The spec table has 34 rows post the 2026-05-17 sensor-messaging-unification landing that removed `sensor:observe`.)

**Verify:** `go test ./control/controlapi/... -count=1 -run TestV1Registry`.

---

# Section D — Auth middleware replacement

### D1. Identity context plumbing

**Files:**
- `control/controlapi/auth.go` (rewrite — replace the existing 44-line stub)

**Steps:**

1. Replace the existing file's contents with the new shape — the existing `Authenticator` interface and `authCtxKey{}` plumbing are out. The new shape:

```go
package controlapi

import (
    "context"
    "net/http"

    "github.com/fallguyconsulting/rimsky/foundation/auth"
)

// ctxKeyIdentity is the context key for the resolved Identity.
type ctxKeyIdentity struct{}

// ctxKeyMode is the context key for the per-request Mode
// (execute or dry_run) resolved from the first-matching grant entry.
type ctxKeyMode struct{}

// IdentityFromContext returns the Identity placed in ctx by the auth
// middleware. Panics in handlers that run without the middleware
// (which would be a wiring bug — every handler is gated). Callers
// who need a softer failure mode can use IdentityFromContextOK.
func IdentityFromContext(ctx context.Context) auth.Identity {
    id, ok := IdentityFromContextOK(ctx)
    if !ok {
        panic("controlapi: no identity in context — auth middleware missing?")
    }
    return id
}

func IdentityFromContextOK(ctx context.Context) (auth.Identity, bool) {
    v, ok := ctx.Value(ctxKeyIdentity{}).(auth.Identity)
    return v, ok
}

// ModeFromContext returns the per-request Mode (execute or dry_run).
// Defaults to execute for read actions / when the auth middleware
// didn't set it.
func ModeFromContext(ctx context.Context) auth.Mode {
    if m, ok := ctx.Value(ctxKeyMode{}).(auth.Mode); ok {
        return m
    }
    return auth.ModeExecute
}
```

2. The existing `Authenticator` interface and `GetAuth` shim used by today's code are no longer needed. Search the codebase for callers (`rg 'controlapi\.GetAuth\b'`, `rg 'controlapi\.Authenticator\b'`) and update them — for now there should be very few, since the existing scaffolding is barely used. Add a one-line compat note in the package comment if a downstream test still references the old shape.

**Verify:** `go build ./...` is clean. The old `AppDeps.Auth` field will fail to compile — D2 fixes the type.

---

### D2. AuthState struct + identity resolution

**Files:**
- `control/controlapi/auth_middleware.go` (new)
- `control/controlapi/app.go` (modified — replace `Auth Authenticator` field with `AuthState *AuthState`)

**Note on the split between D2 and D6.** The auth flow is implemented as two cooperating pieces:

- **An outer middleware** (this task, D2) that resolves identity (`Authorization: Bearer ...` → real key or anonymous-mode synthetic), denies pre-action-resolution failures (no-token, invalid-token, expired-token, revoked-token) with 401, and otherwise puts the identity on the request context. It does NOT do action lookup or permission check, because chi's `RouteContext().RoutePattern()` is empty at outer-middleware time (the inner router hasn't matched the route yet).
- **A per-handler wrapper** `gateByAction` (D6) that knows the route's action name at registration time, does the permission check against the identity on the context, sets `ctxKeyMode` from the matched entry, emits the audit row, and dispatches to the handler.

D2 defines the outer middleware + the `AuthState` struct + identity-resolution helpers. D6 defines `gateByAction`. D7 wires the existing route registrations to use it.

**Steps:**

1. Implement `AuthState` and the outer middleware:

```go
package controlapi

import (
    "context"
    "encoding/json"
    "net/http"
    "strings"
    "sync/atomic"
    "time"

    "github.com/fallguyconsulting/rimsky/foundation/auth"
    "github.com/fallguyconsulting/rimsky/foundation/persistence"
    foundationshared "github.com/fallguyconsulting/rimsky/foundation/shared"
)

// AuthState is the per-process auth-middleware state. Built once at
// startup; the outer middleware and gateByAction close over it.
type AuthState struct {
    Keys     persistence.APIKeyTable
    Events   persistence.EventTable
    Registry *ActionRegistry
    Clock    foundationshared.Clock
    Logger   foundationshared.Logger

    // Predicate cache for the "any active keys?" check, used in
    // the unauthenticated fallback path. TTL bounded at 1s.
    anonCache atomic.Pointer[anonCacheEntry]
}

type anonCacheEntry struct {
    isAnon bool
    until  time.Time
}

const anonCacheTTL = 1 * time.Second

// IsAnonymousMode returns whether the deployment currently has zero
// active keys. Uses a short TTL cache to avoid per-request DB hits
// in the unauthenticated fallback path.
func (s *AuthState) IsAnonymousMode(ctx context.Context) (bool, error) {
    now := s.Clock.Now()
    if e := s.anonCache.Load(); e != nil && now.Before(e.until) {
        return e.isAnon, nil
    }
    n, err := s.Keys.ActiveCount(ctx)
    if err != nil {
        return false, err
    }
    e := &anonCacheEntry{isAnon: n == 0, until: now.Add(anonCacheTTL)}
    s.anonCache.Store(e)
    return e.isAnon, nil
}

// InvalidateAnonCache drops the cached predicate. Called by auth
// endpoint handlers after a mutation that could cross the zero
// boundary (create / revoke / rotate / sweep).
func (s *AuthState) InvalidateAnonCache() {
    s.anonCache.Store(nil)
}

// IdentityResolver is the outer chi middleware. It:
//   - extracts Authorization: Bearer <plaintext>
//   - looks up the key by SHA-256(plaintext)
//   - applies the active-status predicate
//   - on success, sets ctxKeyIdentity and falls through to the inner router
//   - on failure with no anonymous fallback, returns 401 with the
//     appropriate denial_reason and emits auth.access_denied
//
// Does NOT resolve action or check permission — that happens in
// gateByAction (per-handler), once chi has matched the route.
func (s *AuthState) IdentityResolver() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ident, denial, err := s.resolveIdentity(r.Context(), r)
            if err != nil {
                s.Logger.Error("auth.middleware.error", "err", err.Error())
                writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "auth middleware failure"})
                return
            }
            if denial != "" {
                start := s.Clock.Now()
                s.emitDenied(r.Context(), r, start, ident, "", protocolSkinFromContext(r.Context()), nil, http.StatusUnauthorized, denial)
                writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
                return
            }
            ctx := context.WithValue(r.Context(), ctxKeyIdentity{}, ident)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// resolveIdentity returns (identity, "", nil) on success, including
// the anonymous-mode synthetic identity. Returns
// (zero-or-row-identity, denialReason, nil) on auth failure.
// Errors are reserved for unexpected DB failures.
func (s *AuthState) resolveIdentity(ctx context.Context, r *http.Request) (auth.Identity, auth.DenialReason, error) {
    h := r.Header.Get("Authorization")
    if !strings.HasPrefix(h, "Bearer ") {
        anon, err := s.IsAnonymousMode(ctx)
        if err != nil {
            return auth.Identity{}, "", err
        }
        if anon {
            return auth.AnonymousIdentity(), "", nil
        }
        return auth.Identity{}, auth.DenialNoToken, nil
    }
    plaintext := strings.TrimPrefix(h, "Bearer ")
    if err := auth.ValidatePlaintext(plaintext); err != nil {
        return auth.Identity{}, auth.DenialInvalidToken, nil
    }
    h32 := auth.Hash(plaintext)
    row, ok, err := s.Keys.GetByHash(ctx, h32[:])
    if err != nil {
        return auth.Identity{}, "", err
    }
    if !ok {
        return auth.Identity{}, auth.DenialInvalidToken, nil
    }
    now := s.Clock.Now()
    if row.RevokedAt != nil {
        return rowIdentity(row), auth.DenialRevokedToken, nil
    }
    if row.ExpiresAt != nil && !row.ExpiresAt.After(now) {
        return rowIdentity(row), auth.DenialExpiredToken, nil
    }
    if row.RevokeAt != nil && !row.RevokeAt.After(now) {
        return rowIdentity(row), auth.DenialRevokedToken, nil
    }
    return rowIdentity(row), "", nil
}

func rowIdentity(row persistence.APIKey) auth.Identity {
    var perms auth.Grant
    _ = json.Unmarshal(row.Permissions, &perms)
    id := row.ID
    return auth.Identity{
        KeyID:       &id,
        KeyName:     row.Name,
        Kind:        auth.IdentityAPIKey,
        Permissions: perms,
    }
}

// protocolSkinFromContext returns "mcp" if MCP-dispatched, else "http".
// See H5 for where the "mcp" value is set on the context.
type ctxKeyProtocolSkin struct{}

func protocolSkinFromContext(ctx context.Context) string {
    if v, ok := ctx.Value(ctxKeyProtocolSkin{}).(string); ok {
        return v
    }
    return "http"
}
```

2. Update `control/controlapi/app.go`:
   - Replace the `Auth Authenticator` field on `AppDeps` with `AuthState *AuthState`.
   - In `NewApp`, install `r.Use(deps.AuthState.IdentityResolver())` early in the chain (after RequestID/Recoverer/accessLog but before any route registration).
   - D3 separately handles mounting `/health` outside the auth chain.

**Verify:** `go build ./...` is clean. Tests for identity resolution + anonymous-mode predicate caching land in E3.

---

### D3. Bypass list: `/health` and `/ready`

**Files:**
- `control/controlapi/app.go` (modified)

**Steps:**

1. The auth middleware applies to every route in the chi router. `/health` (and `/ready` if it exists) must be exempt — they predate auth and serve infrastructure clients (load balancer, k8s probes) that don't carry Bearer tokens.
2. Look at the current `registerHealthRoutes` call and where it's mounted. If health is currently inside the `r.Group(...)` that applies `AllowContentType`, move it outside — into a group registered before `r.Use(deps.AuthState.AuthMiddleware())` is invoked.
3. Restructure `NewApp` to:
   - Mount `/health` (and `/ready` if present) FIRST, before any middleware that gates on auth.
   - Then apply auth middleware to the rest.

   Skeleton:

```go
func NewApp(deps AppDeps) http.Handler {
    r := chi.NewRouter()
    r.Use(chimiddleware.RequestID)
    r.Use(chimiddleware.Recoverer)
    r.Use(accessLog(deps.Logger))

    // Health endpoints — NOT auth-gated.
    registerHealthRoutes(r, deps)

    // Everything else under the auth middleware.
    r.Group(func(rr chi.Router) {
        rr.Use(deps.AuthState.AuthMiddleware())

        // Observability (read-only) — still auth-gated; readers need
        // `*:read` or similar in V1. Adjust the action registry if a
        // specific observability action is needed.
        if deps.Observability != nil {
            rr.Group(func(obs chi.Router) {
                obs.Use(func(next http.Handler) http.Handler {
                    return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
                        w.Header().Set("Content-Type", "application/json")
                        next.ServeHTTP(w, req)
                    })
                })
                obs.Route("/v1/observability", deps.Observability)
            })
        }

        rr.Group(func(rrr chi.Router) {
            rrr.Use(chimiddleware.AllowContentType("application/json"))
            rrr.Use(func(next http.Handler) http.Handler {
                return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
                    w.Header().Set("Content-Type", "application/json")
                    next.ServeHTTP(w, req)
                })
            })

            registerTemplatesRoutes(rrr, deps)
            registerTagsRoutes(rrr, deps)
            registerInstancesRoutes(rrr, deps)
            registerNodesRoutes(rrr, deps)
            registerEventsRoutes(rrr, deps)
            registerClaimsRoutes(rrr, deps)
            registerMessagesRoutes(rrr, deps)
            registerBackfillsRoutes(rrr, deps)
            registerAssetsRoutes(rrr, deps)
            registerLineageRoutes(rrr, deps)
            registerAdminDiagnosticsRoutes(rrr, deps)
            registerAuthRoutes(rrr, deps)         // new — F1
            registerMCPRoute(rrr, deps)           // new — H5
        })
    })

    return r
}
```

   Note: `/v1/observability/*` reads are gated by `*:read` patterns via the action registry. If the observability routes are not represented in the V1 action registry, decide whether to (a) add a `observability:read` action or (b) exempt observability from auth (similar to /health). The spec section "Existing endpoints — gating" implies all are gated; the agent must add observability to the action registry. Add an `observability:read` action entry to C2 mapping the routes under `/v1/observability/*` and corresponding MCP tool `observability_get` (or just no MCP tool — it's a read-only HTTP-only surface).

4. **Update**: also add `observability:read` to `read-only.json` and `operator.json` if not already covered by `*:read` (it is via the wildcard).

**Verify:** `go test ./control/controlapi/...` — anonymous-mode tests will exercise the /health bypass once L1 lands. For now, `go build ./...`.

---

### D4. AuthState wiring in StartControlAPI

**Files:**
- `control/config/controlapi.go` (modified)

**Steps:**

1. Find the `StartControlAPI` function. Where it currently builds `AppDeps`, add:

```go
authState := &controlapi.AuthState{
    Keys:     persist.APIKeys(),
    Events:   persist.Events(),   // existing accessor
    Registry: controlapi.BuildV1Registry(),
    Clock:    cfg.Clock,
    Logger:   cfg.Logger,
}
deps := controlapi.AppDeps{
    // ... existing fields ...
    AuthState: authState,
    // remove old Auth field
}
```

2. The `cfg.Auth controlapi.Authenticator` field in `ControlAPIConfig` is no longer needed. Remove it.
3. After the handler is built, kick off the anonymous-mode banner goroutine (D5).

**Verify:** `go build ./...` clean across all modules.

---

### D5. Anonymous-mode startup banner

**Files:**
- `control/controlapi/auth_banner.go` (new)
- `control/config/controlapi.go` (modified — start the banner goroutine)

**Steps:**

1. Implement `WatchAnonymousMode` plus a testable single-shot `CheckAnonymousBanner`:

```go
package controlapi

import (
    "context"
    "time"
)

// DefaultBannerInterval is the production cadence between
// repeated WARN banners in anonymous mode.
const DefaultBannerInterval = 5 * time.Minute

// CheckAnonymousBanner queries the anonymous-mode predicate once.
// If true, it logs the WARN banner and returns true; otherwise it
// returns false and logs nothing. Exposed for tests to exercise
// banner emission without running the goroutine loop.
func CheckAnonymousBanner(ctx context.Context, s *AuthState) bool {
    anon, err := s.IsAnonymousMode(ctx)
    if err != nil {
        s.Logger.Error("auth.anonymous_mode.check_failed", "err", err.Error())
        return false
    }
    if anon {
        s.Logger.Warn("auth.anonymous_mode",
            "message", "ANONYMOUS MODE: no API keys provisioned; all requests treated as admin. Run 'rimsky auth init' to enable authentication.")
    }
    return anon
}

// WatchAnonymousMode runs CheckAnonymousBanner once at startup and
// then on each tick of the supplied interval until ctx is cancelled.
// Intended to be started as a goroutine by StartControlAPI; tests
// pass a small interval to exercise the loop without timing flake.
func WatchAnonymousMode(ctx context.Context, s *AuthState, interval time.Duration) {
    if interval <= 0 {
        interval = DefaultBannerInterval
    }
    _ = CheckAnonymousBanner(ctx, s)
    ticker := time.NewTicker(interval)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            _ = CheckAnonymousBanner(ctx, s)
        case <-ctx.Done():
            return
        }
    }
}
```

2. In `StartControlAPI`, after the handler starts:

```go
go controlapi.WatchAnonymousMode(serverCtx, authState, controlapi.DefaultBannerInterval)
```

   Where `serverCtx` is the existing cancellation context the server uses (grep `context.Background()` near the http.Server.Serve call).

**Verify:** `go build ./...`. L9's test exercises `CheckAnonymousBanner` directly.

---

### D6. `gateByAction`: per-handler permission check + audit emit

**Files:**
- `control/controlapi/auth_middleware.go` (extended)

**Why this is per-handler rather than outer middleware.** Chi's `RouteContext().RoutePattern()` returns the matched route pattern, but it is only populated **after** chi's inner router has matched the request to a route. An outer middleware (one registered with `r.Use(...)`) runs BEFORE the inner match, so `RoutePattern()` returns "". By wrapping each handler with `gateByAction("<action>", handler)` at registration time, we move the permission check to a point where the action is statically known. Each route registration explicitly declares the action it gates on.

**Steps:**

1. Implement `gateByAction`:

```go
// gateByAction returns a handler that:
//   - reads the identity placed on ctx by IdentityResolver
//   - checks the identity's grant against the named action
//   - on deny, returns 403 with auth.access_denied audit
//   - on allow, sets ctxKeyMode from the matched entry, runs the
//     inner handler, then emits auth.access_attempted with the
//     captured status code
//   - best-effort updates last_used_at on the key
func (s *AuthState) gateByAction(action string, inner http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        start := s.Clock.Now()
        ident, ok := IdentityFromContextOK(r.Context())
        if !ok {
            // IdentityResolver should have populated this. Surface as 500 — a wiring bug.
            s.Logger.Error("auth.gate.no_identity", "action", action)
            writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "no identity"})
            return
        }
        res := auth.CheckGrant(ident.Permissions, action)
        skin := protocolSkinFromContext(r.Context())
        if !res.Allowed {
            s.emitDenied(r.Context(), r, start, ident, action, skin, captureBody(r), http.StatusForbidden, auth.DenialPermissionDenied)
            writeJSON(w, http.StatusForbidden, map[string]any{"error": "permission denied"})
            return
        }
        ctx := context.WithValue(r.Context(), ctxKeyMode{}, res.Mode)

        ww := newCapturingWriter(w)
        inner.ServeHTTP(ww, r.WithContext(ctx))

        s.emitAttempted(r.Context(), r, start, ident, action, skin, captureBody(r), ww.status(), res.Mode)

        if ident.KeyID != nil {
            go func(id foundationshared.UUID, now time.Time) {
                bg, cancel := context.WithTimeout(context.Background(), 2*time.Second)
                defer cancel()
                _ = s.Keys.UpdateLastUsed(bg, id, now)
            }(*ident.KeyID, start)
        }
    }
}

// captureBody reads the request body (best-effort) and returns it
// for verbatim inclusion in audit records. The body is re-attached
// to r.Body via NopCloser so the handler can re-read it. If the body
// is empty or unreadable, returns nil.
func captureBody(r *http.Request) []byte {
    if r.Body == nil || r.ContentLength == 0 {
        return nil
    }
    body, err := io.ReadAll(r.Body)
    if err != nil {
        return nil
    }
    r.Body = io.NopCloser(bytes.NewReader(body))
    return body
}

// newCapturingWriter wraps http.ResponseWriter so the audit emitter
// can read the response status code after the handler returns.
type capturingWriter struct {
    http.ResponseWriter
    statusCode int
}

func newCapturingWriter(w http.ResponseWriter) *capturingWriter {
    return &capturingWriter{ResponseWriter: w, statusCode: http.StatusOK}
}

func (c *capturingWriter) WriteHeader(code int) {
    c.statusCode = code
    c.ResponseWriter.WriteHeader(code)
}

func (c *capturingWriter) status() int { return c.statusCode }
```

2. Tests:
   - `gateByAction("instance:create", h)` with a key holding `*` → 200 (or whatever the inner handler returns); audit row `kind: auth.access_attempted`.
   - Same gate with a key holding `*:read` → 403; audit row `kind: auth.access_denied`, `denial_reason: permission_denied`, `action: instance:create`.
   - Inner handler runs with `ModeFromContext(ctx) = dry_run` when the matched entry's mode is `dry_run`.

**Verify:** `go test ./control/controlapi/... -count=1 -run TestGateByAction`.

---

### D7. Update existing route registrations to use `gateByAction`

**Files:**
- `control/controlapi/instances.go` (modified)
- `control/controlapi/templates.go` (modified)
- `control/controlapi/nodes.go` (modified)
- `control/controlapi/tags.go` (modified)
- `control/controlapi/messages.go` (modified)
- `control/controlapi/events.go` (modified)
- `control/controlapi/lineage.go` (modified)
- `control/controlapi/backfills.go` (modified)
- `control/controlapi/assets.go` (modified)
- `control/controlapi/admin_diagnostics.go` (modified)
- `control/controlapi/claims.go` (modified)
- `control/controlapi/actions_test.go` (extended — registry-vs-router cross-check test)

**Steps:**

1. In each `registerFooRoutes` function, find every `r.Get / r.Post / r.Put / r.Delete` line. Wrap the handler with `deps.AuthState.gateByAction("<action-string>", <handler>)`. The action strings come from the V1 registry in C2.
2. The route → action mapping (canonical) from the V1 registry:
   - `instances.go`: `POST /instances`→`instance:create`; `GET /instances`,`GET /instances/{idOrKey}`→`instance:read`; `DELETE /instances/{idOrKey}`→`instance:terminate`.
   - `templates.go`: `POST /templates`→`template:register`; `GET /templates`,`GET /templates/{id}`→`template:read`; `DELETE /templates/{id}`→`template:deregister`; `POST /templates/{id}/deploy`→`template:deploy`; `POST /templates/{id}/undeploy`→`template:undeploy`.
   - `tags.go`: `POST /tags`→`tag:create`; `GET /tags`→`tag:read`; `PUT /tags/{tag}`→`tag:set`; `DELETE /tags/{tag}`→`tag:delete`.
   - `nodes.go`: `GET /nodes/{id}`,`GET /instances/{idOrKey}/nodes`→`node:read`; `POST /nodes/{id}/invalidate`→`node:invalidate`; `POST /nodes/{id}/reset`→`node:reset`.
   - `messages.go`: `POST /instances/{id}/messages`→`message:send`; `GET /instances/{id}/messages`,`GET /messages/{id}`→`message:read`.
   - `events.go`: `GET /events`→`event:read`.
   - `lineage.go`: `GET /lineage/*`→`lineage:read`; `POST /admin/lineage/prune`→`lineage:prune`.
   - `backfills.go`: `POST /instances/{id}/backfills`→`backfill:create`; `GET /instances/{id}/backfills`,`GET /backfills/{op_id}`,`GET /backfills/{op_id}/partitions`→`backfill:read`; `POST /backfills/{op_id}/cancel`→`backfill:cancel`.
   - `assets.go`: `GET /instances/{id}/assets`,`GET /instances/{id}/assets/{alias}`,`GET /instances/{id}/assets/{alias}/versions`,`GET /instances/{id}/assets/{alias}/materialization-history`→`asset:read`; `POST /instances/{id}/assets/{alias}/materialize`→`asset:materialize`; `DELETE /instances/{id}/assets/{alias}`→`asset:delete`.
   - `admin_diagnostics.go`: `GET /admin/diagnostics/held-frames`→`diagnostics:read`; `GET /admin/diagnostics/parked-nodes`,`GET /diagnostics/parked`→`parked-node:read`; `GET /admin/diagnostics/wait-sets`→`waitset:read`; `POST /admin/instances/{instance}/nodes/{node_id}/invalidate`→`node:invalidate`.
   - `claims.go`: `GET /lock-holders/{claim_handle_id}/claim-holders`→`claim-holders:read`.
3. Add a registry-vs-router cross-check test in `actions_test.go`:

```go
func TestRegistryCoversRouter(t *testing.T) {
    deps := buildTestDeps(t) // standard test-deps builder
    reg := controlapi.BuildV1Registry()
    h := controlapi.NewApp(deps)
    walkRoutes(t, h, func(method, pattern string) {
        // /health and /v1/observability/* are out of the registry by design.
        if pattern == "/health" || strings.HasPrefix(pattern, "/v1/observability") {
            return
        }
        action := reg.ActionForRoute(method, pattern)
        if action == "" {
            t.Errorf("route %s %s has no action mapping in V1 registry", method, pattern)
        }
    })
}
```

`walkRoutes` uses chi's `Walk` function on the underlying router. This test catches any handler that gets a route but is not wired to `gateByAction` (because the registry walk will find the un-gated pattern that has no corresponding registry entry).

**Verify:** `go test ./control/controlapi/... -count=1 -run "TestRegistryCoversRouter|TestActionRegistry"` and `go build ./...` clean.

---

# Section E — Audit emission

### E1. Event-table writer accessor

**Files:**
- `foundation/persistence/api_keys.go` (extended — or sibling: where `EventTable` is defined; the existing `Events()` accessor should already be there)

**Steps:**

1. Confirm `persistence.Events()` returns an `EventTable` with an `Insert(ctx, EventRow)` method. Grep `foundation/persistence/` for `type EventTable` and `func ... Events()`.
2. If `EventTable.Insert` already exists, no change needed. If not, add it — same shape as other table interfaces, with `kind TEXT` and `payload JSONB` columns. The schema is part of the baseline migration.
3. Document on the interface: "auth.* event kinds are emitted by `control/controlapi/auth_middleware.go::AuthState.emit*`."

**Verify:** `cd foundation && go build ./...`.

---

### E2. Audit emit helpers

**Files:**
- `control/controlapi/audit.go` (new)

**Steps:**

1. Implement:

```go
package controlapi

import (
    "context"
    "encoding/json"
    "net/http"
    "time"

    "github.com/fallguyconsulting/rimsky/foundation/auth"
    "github.com/fallguyconsulting/rimsky/foundation/persistence"
    "github.com/fallguyconsulting/rimsky/foundation/shared"
)

// emitAttempted writes one auth.access_attempted event.
func (s *AuthState) emitAttempted(
    ctx context.Context,
    r *http.Request,
    start time.Time,
    ident auth.Identity,
    action string,
    skin string,
    requestParams json.RawMessage,
    status int,
    mode auth.Mode,
) {
    elapsed := s.Clock.Now().Sub(start).Milliseconds()
    p := auth.AccessAttemptedPayload{
        KeyID:          ident.KeyID,
        KeyName:        ident.KeyName,
        IdentityKind:   ident.Kind,
        ProtocolSkin:   skin,
        Action:         action,
        RequestPath:    r.URL.Path,
        RequestMethod:  r.Method,
        RequestParams:  requestParams,
        ResponseStatus: status,
        Mode:           mode,
        Executed:       mode == auth.ModeExecute && status < 400,
        DurationMS:     elapsed,
        ClientIP:       clientIP(r),
        UserAgent:      r.Header.Get("User-Agent"),
    }
    s.insertEvent(ctx, auth.EventAccessAttempted, p)
}

// emitDenied writes one auth.access_denied event with the
// population rules from spec section "Audit / Population rules for
// denial rows".
func (s *AuthState) emitDenied(
    ctx context.Context,
    r *http.Request,
    start time.Time,
    ident auth.Identity, // zero-value if pre-lookup
    action string,       // "" if pre-action-resolution
    skin string,
    requestParams json.RawMessage, // nil if body not parsed
    status int,
    reason auth.DenialReason,
) {
    elapsed := s.Clock.Now().Sub(start).Milliseconds()
    p := auth.AccessDeniedPayload{
        ProtocolSkin:   skin,
        RequestPath:    r.URL.Path,
        RequestMethod:  r.Method,
        RequestParams:  requestParams,
        ResponseStatus: status,
        Executed:       false,
        DurationMS:     elapsed,
        ClientIP:       clientIP(r),
        UserAgent:      r.Header.Get("User-Agent"),
        DenialReason:   reason,
    }
    if ident.KeyID != nil || ident.KeyName != "" {
        kn := ident.KeyName
        kk := ident.Kind
        p.KeyID = ident.KeyID
        p.KeyName = &kn
        p.IdentityKind = &kk
    }
    if action != "" {
        a := action
        p.Action = &a
    }
    s.insertEvent(ctx, auth.EventAccessDenied, p)
}

// EmitKeyCreated / EmitKeyRevoked / EmitKeyRotated — exported for
// the F-section endpoints to call after they mutate.
func (s *AuthState) EmitKeyCreated(ctx context.Context, p auth.KeyCreatedPayload) {
    s.insertEvent(ctx, auth.EventKeyCreated, p)
}
func (s *AuthState) EmitKeyRevoked(ctx context.Context, p auth.KeyRevokedPayload) {
    s.insertEvent(ctx, auth.EventKeyRevoked, p)
}
func (s *AuthState) EmitKeyRotated(ctx context.Context, p auth.KeyRotatedPayload) {
    s.insertEvent(ctx, auth.EventKeyRotated, p)
}

func (s *AuthState) insertEvent(ctx context.Context, kind string, payload any) {
    data, err := json.Marshal(payload)
    if err != nil {
        s.Logger.Error("audit.marshal", "kind", kind, "err", err.Error())
        return
    }
    row := persistence.EventRow{
        ID:        shared.NewUUID(),
        Kind:      kind,
        Payload:   data,
        CreatedAt: s.Clock.Now(),
    }
    if err := s.Events.Insert(ctx, row); err != nil {
        s.Logger.Error("audit.insert", "kind", kind, "err", err.Error())
    }
}

func clientIP(r *http.Request) string {
    if h := r.Header.Get("X-Forwarded-For"); h != "" {
        if i := indexComma(h); i > 0 {
            return h[:i]
        }
        return h
    }
    return r.RemoteAddr
}

func indexComma(s string) int {
    for i := 0; i < len(s); i++ {
        if s[i] == ',' {
            return i
        }
    }
    return -1
}
```

   Adjust to use whatever `persistence.EventRow` looks like (grep `type EventRow` in `foundation/persistence/`).

**Verify:** `go test ./control/controlapi/... -count=1 -run TestAudit` — tests come in E3.

---

### E3. Audit emit unit tests

**Files:**
- `control/controlapi/audit_test.go` (new)

**Steps:**

1. Write tests using a fake `EventTable` and a fake `Clock`. Verify:
   - `emitAttempted` for an api_key identity produces a payload with `key_id`, `key_name`, `identity_kind: api_key`, `action`, etc.
   - `emitAttempted` for anonymous identity produces `key_id: null`, `key_name: "anonymous"`, `identity_kind: "anonymous"`.
   - `emitDenied` with `denial_reason: no_token` produces null `action`, null `request_params`.
   - `emitDenied` with `denial_reason: permission_denied` produces populated `action` and `request_params`.
   - Marshal errors are logged but don't panic.
   - All required fields are present per kind (spot-check JSON output).

**Verify:** `go test ./control/controlapi/... -count=1 -run TestAudit`.

---

# Section F — Auth endpoints

### F1. Auth routes registration

**Files:**
- `control/controlapi/auth_routes.go` (new)

**Steps:**

1. Create:

```go
package controlapi

import "github.com/go-chi/chi/v5"

// registerAuthRoutes wires the /auth/keys/* surface plus
// GET /auth/status. Each handler is wrapped by gateByAction with
// the appropriate auth:* action.
func registerAuthRoutes(r chi.Router, deps AppDeps) {
    r.Post(  "/auth/keys",                          deps.AuthState.gateByAction("auth:create", handleCreateKey(deps)))
    r.Get(   "/auth/keys",                          deps.AuthState.gateByAction("auth:read",   handleListKeys(deps)))
    r.Get(   "/auth/keys/{nameOrID}",               deps.AuthState.gateByAction("auth:read",   handleShowKey(deps)))
    r.Delete("/auth/keys/{nameOrID}",               deps.AuthState.gateByAction("auth:revoke", handleRevokeKey(deps)))
    r.Post(  "/auth/keys/{nameOrID}/rotate",        deps.AuthState.gateByAction("auth:rotate", handleRotateKey(deps)))
    r.Get(   "/auth/status",                        deps.AuthState.gateByAction("auth:read",   handleAuthStatus(deps)))
}
```

2. Update `app.go`'s `NewApp` to call `registerAuthRoutes(rrr, deps)` (the inner-most group; already added in D3's restructure).

**Verify:** `go build ./...`.

---

### F2. `POST /auth/keys` (mint)

**Files:**
- `control/controlapi/auth_handlers.go` (new)
- `control/controlapi/auth_handlers_test.go` (new)

**Steps:**

1. Write the failing test first (TDD). In `auth_handlers_test.go`, test:
   - Request body `{"name": "ops", "permissions": [{"action": "*"}]}` → 201 with `plaintext` field, `id`, `name`, `permissions`, `created_at`.
   - Same name twice → 409 conflict.
   - Body with an unknown action string (e.g. `"foo:bar"`) → 400.
   - Body with an action string that's well-formed grammar but unknown in the registry (e.g. `"unknown:thing"`) → 400 with message mentioning the action.
2. Implement `handleCreateKey`:

```go
func handleCreateKey(deps AppDeps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        type req struct {
            Name        string     `json:"name"`
            Permissions auth.Grant `json:"permissions"`
            ExpiresAt   *time.Time `json:"expires_at,omitempty"`
        }
        var body req
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
            badRequest(w, "invalid JSON: "+err.Error())
            return
        }
        if body.Name == "" {
            badRequest(w, "name is required")
            return
        }
        if err := auth.ValidateGrant(body.Permissions); err != nil {
            badRequest(w, err.Error())
            return
        }
        for _, e := range body.Permissions {
            if e.Action != "*" && !strings.HasSuffix(e.Action, ":*") && !strings.HasPrefix(e.Action, "*:") {
                if !deps.AuthState.Registry.IsKnownAction(e.Action) {
                    badRequest(w, "unknown action: "+e.Action)
                    return
                }
            }
        }
        plaintext, hash, err := authpkg.Mint()
        if err != nil {
            writeError(w, err)
            return
        }
        permsJSON, _ := json.Marshal(body.Permissions)
        ident, _ := IdentityFromContextOK(r.Context())
        var createdBy *shared.UUID
        if ident.KeyID != nil {
            createdBy = ident.KeyID
        }
        row := persistence.APIKey{
            ID:             shared.NewUUID(),
            KeyHash:        hash[:],
            Name:           body.Name,
            Permissions:    permsJSON,
            CreatedAt:      deps.AuthState.Clock.Now(),
            CreatedByKeyID: createdBy,
            ExpiresAt:      body.ExpiresAt,
        }
        if err := deps.AuthState.Keys.Insert(r.Context(), row); err != nil {
            if errors.Is(err, persistence.ErrAPIKeyNameTaken) {
                writeJSON(w, http.StatusConflict, map[string]any{"error": "name already in use"})
                return
            }
            writeError(w, err)
            return
        }
        deps.AuthState.InvalidateAnonCache()
        deps.AuthState.EmitKeyCreated(r.Context(), auth.KeyCreatedPayload{
            KeyID: row.ID, KeyName: row.Name, Permissions: body.Permissions,
            CreatedByKeyID: createdBy, ExpiresAt: row.ExpiresAt,
        })
        resp := map[string]any{
            "id":          row.ID,
            "name":        row.Name,
            "plaintext":   plaintext,
            "permissions": body.Permissions,
            "created_at":  row.CreatedAt,
            "expires_at":  row.ExpiresAt,
        }
        writeJSON(w, http.StatusCreated, resp)
    }
}
```

   The validation loop's check for "wildcards exempt from known-action lookup" is intentional — a wildcard like `*:read` is a valid grant even if no specific `*:read` action exists; the wildcard matches what's in the registry at request time. Document this in a comment.

**Verify:** `go test ./control/controlapi/... -count=1 -run TestCreateKey`.

---

### F3. `GET /auth/keys` and `GET /auth/keys/{nameOrID}`

**Files:**
- `control/controlapi/auth_handlers.go` (extended)
- `control/controlapi/auth_handlers_test.go` (extended)

**Steps:**

1. Tests:
   - List returns empty array initially.
   - After minting two keys, List returns both, sorted by `created_at DESC` per the persistence impl.
   - `name_filter=ops*` returns only keys whose name matches the glob.
   - `include_revoked=true` includes revoked rows; default `false` excludes them.
   - Show by name returns the active row; show by UUID returns the row regardless of revoked status.
   - Show on nonexistent name → 404.
2. Implement `handleListKeys`:
   - Read query params `name_filter`, `include_revoked` (bool, default false).
   - Call `deps.AuthState.Keys.List(ctx, includeRevoked, nameFilter)`.
   - Convert each row to a public DTO with no plaintext field.
   - Return JSON array.
3. Implement `handleShowKey`:
   - Extract `nameOrID` from URL path.
   - Try parsing as UUID first; if it parses, `GetByID`. Otherwise `GetByName`.
   - 404 on not-found.

**Verify:** `go test ./control/controlapi/... -count=1 -run "TestListKeys|TestShowKey"`.

---

### F4. `DELETE /auth/keys/{nameOrID}` (revoke with last-key guard)

**Files:**
- `control/controlapi/auth_handlers.go` (extended)
- `control/controlapi/auth_handlers_test.go` (extended)

**Steps:**

1. Tests:
   - Revoke an active key → 200; row's `revoked_at` is set; subsequent auth attempts with that plaintext → 401 with `denial_reason: revoked_token`.
   - Revoke the LAST active key → 409 conflict with body explaining the issue.
   - Revoke the last active key with `?force_leave_anonymous=true` → 200; deployment is now anonymous.
   - Revoke a nonexistent key → 404.
   - Idempotent revoke: revoking an already-revoked key → 200 (no-op).
2. Implement `handleRevokeKey`:

```go
func handleRevokeKey(deps AppDeps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        nameOrID := chi.URLParam(r, "nameOrID")
        force := r.URL.Query().Get("force_leave_anonymous") == "true"

        row, ok, err := lookupByNameOrID(ctx, deps.AuthState.Keys, nameOrID)
        if err != nil {
            writeError(w, err)
            return
        }
        if !ok {
            notFoundResp(w, "no such key")
            return
        }

        // Guard: refuse if this revocation would leave zero active keys.
        active, err := deps.AuthState.Keys.ActiveCount(ctx)
        if err != nil {
            writeError(w, err)
            return
        }
        // Active count includes the target if it is still active.
        thisRowActive := row.RevokedAt == nil &&
            (row.ExpiresAt == nil || row.ExpiresAt.After(deps.AuthState.Clock.Now())) &&
            (row.RevokeAt == nil || row.RevokeAt.After(deps.AuthState.Clock.Now()))
        if thisRowActive && active <= 1 && !force {
            writeJSON(w, http.StatusConflict, map[string]any{
                "error":  "would leave zero active keys (anonymous mode); pass ?force_leave_anonymous=true to confirm",
                "active_keys_after": 0,
            })
            return
        }

        if err := deps.AuthState.Keys.MarkRevoked(ctx, row.ID, deps.AuthState.Clock.Now()); err != nil {
            writeError(w, err)
            return
        }
        deps.AuthState.InvalidateAnonCache()
        ident, _ := IdentityFromContextOK(ctx)
        var revokedBy *shared.UUID
        if ident.KeyID != nil {
            revokedBy = ident.KeyID
        }
        deps.AuthState.EmitKeyRevoked(ctx, auth.KeyRevokedPayload{
            KeyID: row.ID, KeyName: row.Name, RevokedByKeyID: revokedBy,
            Reason: auth.RevokeReasonManual,
        })
        writeJSON(w, http.StatusOK, map[string]any{"id": row.ID, "name": row.Name})
    }
}
```

**Verify:** `go test ./control/controlapi/... -count=1 -run TestRevokeKey`.

---

### F5. `POST /auth/keys/{nameOrID}/rotate`

**Files:**
- `control/controlapi/auth_handlers.go` (extended)
- `control/controlapi/auth_handlers_test.go` (extended)

**Steps:**

1. Tests:
   - Rotate a key with default grace (24h) → 200 with `old_key_id`, `new_key_id`, `name`, `plaintext`, `revoke_at`.
   - Old key still authenticates during grace window.
   - New key authenticates.
   - Both keys have same `name` AND both rows present (old row has `revoke_at` set; new row does not).
   - Rotate with `grace: 0s` (or 1m for testability) → after sweep, old key → 401.
   - Rotate a nonexistent key → 404.
2. Implement `handleRotateKey`. The mint + set_revoke_at + insert must be atomic — use a transaction. If the existing persistence layer doesn't expose a transaction helper, add one (`Tables.WithTx(ctx, func(tx Tables) error)` — grep for existing pattern). If the existing API doesn't support transactions at the table level, fall back to:
   - Insert the new row.
   - On success, set revoke_at on the old row.
   - On failure of step 2, the new row is orphaned but harmless; log and rely on the operator to clean up.

   Prefer the transactional path. The unique-name partial index requires `revoke_at` to be set on the old row BEFORE the new row inserts, otherwise the unique-name constraint fires. So the order is:
   1. Read old row.
   2. SET old.revoke_at = now + grace (this drops it from the partial unique index).
   3. INSERT new row (now allowed by the partial unique index).
   4. Both inside one transaction.

3. Implementation:

```go
func handleRotateKey(deps AppDeps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        nameOrID := chi.URLParam(r, "nameOrID")
        type req struct {
            Grace string `json:"grace,omitempty"`
        }
        var body req
        _ = json.NewDecoder(r.Body).Decode(&body)
        grace := 24 * time.Hour
        if body.Grace != "" {
            d, err := time.ParseDuration(body.Grace)
            if err != nil {
                badRequest(w, "invalid grace duration: "+err.Error())
                return
            }
            grace = d
        }
        oldRow, ok, err := lookupByNameOrID(ctx, deps.AuthState.Keys, nameOrID)
        if err != nil {
            writeError(w, err)
            return
        }
        if !ok {
            notFoundResp(w, "no such key")
            return
        }

        plaintext, hash, err := authpkg.Mint()
        if err != nil {
            writeError(w, err)
            return
        }

        now := deps.AuthState.Clock.Now()
        revokeAt := now.Add(grace)
        newRow := persistence.APIKey{
            ID:        shared.NewUUID(),
            KeyHash:   hash[:],
            Name:      oldRow.Name,
            Permissions: oldRow.Permissions,
            CreatedAt: now,
            ExpiresAt: oldRow.ExpiresAt,
        }

        // Atomic: set revoke_at on old, then insert new.
        if err := deps.AuthState.Keys.WithTx(ctx, func(tx persistence.APIKeyTable) error {
            if err := tx.SetRevokeAt(ctx, oldRow.ID, revokeAt); err != nil {
                return err
            }
            return tx.Insert(ctx, newRow)
        }); err != nil {
            writeError(w, err)
            return
        }

        deps.AuthState.EmitKeyRotated(ctx, auth.KeyRotatedPayload{
            OldKeyID: oldRow.ID, NewKeyID: newRow.ID, Name: oldRow.Name, RevokeAt: revokeAt,
        })
        writeJSON(w, http.StatusOK, map[string]any{
            "old_key_id": oldRow.ID,
            "new_key_id": newRow.ID,
            "name":       newRow.Name,
            "plaintext":  plaintext,
            "revoke_at":  revokeAt,
        })
    }
}
```

   The `WithTx` method needs to exist on `APIKeyTable` (or the runtime Tables umbrella). If not present, add it now: `WithTx(ctx, func(tx APIKeyTable) error) error`. Both postgres and sqlite impls wrap a DB transaction. Postgres: `pgxpool.Begin()` + commit/rollback. SQLite: `sql.Tx`.

**Verify:** `go test ./control/controlapi/... -count=1 -run TestRotateKey`.

---

### F6. `GET /auth/status`

**Files:**
- `control/controlapi/auth_handlers.go` (extended)
- `control/controlapi/auth_handlers_test.go` (extended)

**Steps:**

1. Tests:
   - Anonymous mode → `{ "mode": "anonymous", "active_key_count": 0, "admin_count": 0 }`.
   - One admin key minted → `{ "mode": "authenticated", "active_key_count": 1, "admin_count": 1 }`.
   - Two keys, one admin, one operator → admin_count: 1.
2. Implement `handleAuthStatus`:
   - Query `ActiveCount`.
   - Query a per-key sample to count admins (a key is admin if grant contains `{ "action": "*" }` somewhere). Simpler: List all active keys and count those whose grant contains an entry with `action == "*"`.
   - Return JSON.

**Verify:** `go test ./control/controlapi/... -count=1 -run TestAuthStatus`.

---

### F7. Helper: `lookupByNameOrID`

**Files:**
- `control/controlapi/auth_handlers.go` (extended)

**Steps:**

1. Implement:

```go
func lookupByNameOrID(ctx context.Context, t persistence.APIKeyTable, nameOrID string) (persistence.APIKey, bool, error) {
    if id, err := shared.ParseUUID(nameOrID); err == nil {
        return t.GetByID(ctx, id)
    }
    return t.GetByName(ctx, nameOrID)
}
```

**Verify:** Compiles; covered by F3-F5 tests.

---

### F8. Integration test: end-to-end auth lifecycle

**Files:**
- `test/scenarios/auth/lifecycle_test.go` (new)

**Steps:**

1. Boot a control-api with a real Postgres (testcontainers fixture from `internal/pgtest/`).
2. Confirm anonymous mode at startup (`GET /auth/status` → anonymous).
3. Mint admin key (`POST /auth/keys`).
4. `GET /auth/status` → authenticated, 1 admin.
5. Try `POST /auth/keys` without a bearer token → 401.
6. With the admin's plaintext, mint a read-only key.
7. With the read-only plaintext, attempt `POST /auth/keys` → 403.
8. With the read-only plaintext, `GET /auth/keys` → 200.
9. Rotate the admin key with 1 minute grace; verify both keys work.
10. Inject a `foundationshared.Clock` fake at AuthState construction (the existing pgtest fixture supports clock injection — grep `internal/pgtest/` for `FakeClock` or similar). Advance the fake clock 70 seconds past `revoke_at`; explicitly call `runtime.SweepRotationGrace(ctx, tables.APIKeys(), tables.Events(), fakeClock, log)`. Verify old admin plaintext → 401 with `denial_reason: revoked_token`; new plaintext → 200. Do NOT use a real `time.Sleep` — the test must be deterministic.

**Verify:** `go test ./test/scenarios/auth/... -count=1 -run TestAuthLifecycle`.

---

# Section G — Rotation-grace sweep in rimsky-scheduler

### G1. Sweep function

**Files:**
- `runtime/auth_sweep.go` (new)
- `runtime/auth_sweep_test.go` (new)

**Steps:**

1. Implement:

```go
package runtime

import (
    "context"
    "time"

    "github.com/fallguyconsulting/rimsky/foundation/auth"
    "github.com/fallguyconsulting/rimsky/foundation/persistence"
    foundationshared "github.com/fallguyconsulting/rimsky/foundation/shared"
)

// SweepRotationGrace revokes keys whose rotation-grace window has
// expired. Emits auth.key_revoked with reason=rotation_grace for
// each. Idempotent. Runs in cmd:rimsky-scheduler alongside other
// sweeps.
func SweepRotationGrace(
    ctx context.Context,
    keys persistence.APIKeyTable,
    events persistence.EventTable,
    clock foundationshared.Clock,
    log foundationshared.Logger,
) (int, error) {
    now := clock.Now()
    swept, err := keys.SweepRotationGrace(ctx, now)
    if err != nil {
        return 0, err
    }
    for _, k := range swept {
        payload := auth.KeyRevokedPayload{
            KeyID:   k.ID,
            KeyName: k.Name,
            Reason:  auth.RevokeReasonRotationGrace,
        }
        // emitEvent helper, similar to the audit one in controlapi
        emitKeyRevoked(ctx, events, clock, payload)
        log.Info("auth.rotation_grace_revoked", "key_id", k.ID, "key_name", k.Name)
    }
    return len(swept), nil
}

func emitKeyRevoked(ctx context.Context, events persistence.EventTable, clock foundationshared.Clock, p auth.KeyRevokedPayload) {
    data, err := jsonMarshal(p)
    if err != nil {
        return
    }
    _ = events.Insert(ctx, persistence.EventRow{
        ID:        foundationshared.NewUUID(),
        Kind:      auth.EventKeyRevoked,
        Payload:   data,
        CreatedAt: clock.Now(),
    })
}
```

2. Test:
   - Insert a key with `revoke_at = now - 1m`.
   - Run `SweepRotationGrace(ctx, ..., clock)` where `clock.Now() = now`.
   - Verify the row's `revoked_at` is set.
   - Verify one `auth.key_revoked` event was inserted with `reason: rotation_grace`.
   - Run sweep again immediately → 0 swept (idempotent).

**Verify:** `go test ./runtime/... -count=1 -run TestSweepRotationGrace`.

---

### G2. Schedule the sweep in rimsky-scheduler

**Files:**
- `cmd/rimsky-scheduler/main.go` (modified — add the sweep loop)

**Steps:**

1. Locate `cmd/rimsky-scheduler/main.go`. Find the periodic-sweep dispatch loop (grep for `SweepStaleHeartbeats` or similar). Add a parallel goroutine or merge into the existing tick:

```go
go func() {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            n, err := runtime.SweepRotationGrace(ctx, tables.APIKeys(), tables.Events(), clock, log)
            if err != nil {
                log.Error("auth.sweep.failed", "err", err.Error())
                continue
            }
            if n > 0 {
                log.Info("auth.sweep.done", "swept", n)
            }
        }
    }
}()
```

   Match the cadence and structure of nearby sweeps. The 1-minute cadence matches the spec.

**Verify:** `go build ./cmd/rimsky-scheduler/...`. Integration test in F8 (or L4) exercises it.

---

# Section H — MCP fold-in

### H1. Audit and adapt the existing standalone module

**Files:**
- Read-only inspection of:
  - `mcp-servers/control-api/server.go`
  - `mcp-servers/control-api/tools.go`
  - `mcp-servers/control-api/config.go`

**Steps:**

1. Open and read each file. Understand:
   - `server.go`: JSON-RPC over HTTP envelope handling; `initialize`, `tools/list`, `tools/call` dispatch.
   - `tools.go`: hand-curated tool catalog where each tool registers a handler that calls `s.callJSON(...)` against control-api over HTTP.
   - `config.go`: control-api URL + bearer token forwarding.

2. The fold-in changes:
   - JSON-RPC envelope handling moves to `control/controlapi/mcp/server.go` largely intact.
   - The tool catalog moves but is re-curated against the V1 action registry from C2. Each MCP tool's handler dispatches to the **internal HTTP handler** (chi router) rather than calling `s.callJSON`. This avoids a self-loopback HTTP call.
   - The `config.go` URL+token model is no longer needed (in-process dispatch).

3. **Inventory** the current tools.go list and cross-check against the V1 action registry in C2. The current tools (per earlier inspection):
   - `template_list`, `template_get`, `template_register`, `template_deploy`, `template_undeploy`, `template_deregister`
   - `tag_list`, `tag_set`, `tag_delete`
   - `instance_list`, `instance_get`, `instance_create`, `instance_terminate`
   - `node_get`, `node_invalidate`
   - `held_frames_list`, `parked_node_list` (singular `node`; existing module uses this spelling — match it)
   - (`force_fire_scheduled` already retired)

   Missing tools the V1 registry adds: `tag_create`, `node_list`, `node_reset`, `message_send`, `message_list`, `message_get`, `event_list`, `lineage_get`, `lineage_prune`, `waitset_list`, `claim_holders_list`, `backfill_*`, `asset_*`, `auth_*`.

**Verify:** No code edits this task; output is understanding. Move to H2.

---

### H2. New MCP package: types + JSON-RPC envelope

**Files:**
- `control/controlapi/mcp/server.go` (new)
- `control/controlapi/mcp/types.go` (new)

**Steps:**

1. Create `control/controlapi/mcp/types.go` with the JSON-RPC + MCP envelope types:

```go
package mcp

import "encoding/json"

type Request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id,omitempty"`
    Result  any             `json:"result,omitempty"`
    Error   *Error          `json:"error,omitempty"`
}

type Error struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Data    any    `json:"data,omitempty"`
}

// Tool is one MCP tool descriptor (for tools/list).
type Tool struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    InputSchema json.RawMessage `json:"inputSchema"`
}

// Standard JSON-RPC error codes.
const (
    CodeParseError      = -32700
    CodeInvalidRequest  = -32600
    CodeMethodNotFound  = -32601
    CodeInvalidParams   = -32602
    CodeInternalError   = -32603
)
```

2. Create `control/controlapi/mcp/server.go` with the HTTP handler:

```go
package mcp

import (
    "encoding/json"
    "io"
    "net/http"
)

// Server is the in-process MCP handler. It is mounted at POST /mcp
// inside control-api's chi router. It dispatches initialize /
// tools/list / tools/call by calling back into the control-api
// router for tool invocations.
type Server struct {
    Tools ToolCatalog
}

type ToolCatalog interface {
    // Filtered returns the subset of the catalog that the requesting
    // identity is allowed to see. The action gate has already run
    // before this is called.
    Filtered(r *http.Request) []Tool

    // Invoke runs the named tool by dispatching to its registered
    // handler. The handler may be an in-process call into the chi
    // router or a direct function. Returns the result (JSON-marshalable).
    Invoke(r *http.Request, name string, args json.RawMessage) (any, *Error)
}

// ServeHTTP handles a single POST /mcp JSON-RPC request.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    body, err := io.ReadAll(r.Body)
    if err != nil {
        writeRPCError(w, nil, CodeParseError, "read body")
        return
    }
    var req Request
    if err := json.Unmarshal(body, &req); err != nil {
        writeRPCError(w, nil, CodeParseError, "invalid JSON-RPC")
        return
    }
    if req.JSONRPC != "2.0" {
        writeRPCError(w, req.ID, CodeInvalidRequest, "jsonrpc must be 2.0")
        return
    }
    switch req.Method {
    case "initialize":
        s.handleInitialize(w, req)
    case "tools/list":
        s.handleToolsList(w, r, req)
    case "tools/call":
        s.handleToolsCall(w, r, req)
    default:
        writeRPCError(w, req.ID, CodeMethodNotFound, "method not found: "+req.Method)
    }
}

func (s *Server) handleInitialize(w http.ResponseWriter, req Request) {
    // Advertise tools capability only.
    writeRPCResult(w, req.ID, map[string]any{
        "protocolVersion": "2024-11-05",
        "capabilities": map[string]any{
            "tools": map[string]any{},
        },
        "serverInfo": map[string]any{
            "name":    "rimsky-control-api",
            "version": "v1",
        },
    })
}

func (s *Server) handleToolsList(w http.ResponseWriter, r *http.Request, req Request) {
    writeRPCResult(w, req.ID, map[string]any{
        "tools": s.Tools.Filtered(r),
    })
}

func (s *Server) handleToolsCall(w http.ResponseWriter, r *http.Request, req Request) {
    var p struct {
        Name      string          `json:"name"`
        Arguments json.RawMessage `json:"arguments"`
    }
    if err := json.Unmarshal(req.Params, &p); err != nil {
        writeRPCError(w, req.ID, CodeInvalidParams, "invalid params")
        return
    }
    result, rpcErr := s.Tools.Invoke(r, p.Name, p.Arguments)
    if rpcErr != nil {
        writeRPCErrorObj(w, req.ID, rpcErr)
        return
    }
    writeRPCResult(w, req.ID, map[string]any{
        "content": []any{
            map[string]any{"type": "text", "text": jsonMarshal(result)},
        },
    })
}

// Helpers omitted: writeRPCResult, writeRPCError, writeRPCErrorObj,
// jsonMarshal. Copy from mcp-servers/control-api/server.go.
```

**Verify:** `go build ./control/controlapi/mcp/...`.

---

### H3. ToolCatalog implementation: filtered list + invocation

**Files:**
- `control/controlapi/mcp/catalog.go` (new)
- `control/controlapi/mcp/catalog_test.go` (new)

**Steps:**

1. Implement `ToolCatalog`:

```go
package mcp

import (
    "encoding/json"
    "net/http"

    "github.com/fallguyconsulting/rimsky/foundation/auth"
    controlapi "github.com/fallguyconsulting/rimsky/control/controlapi"
)

// Catalog implements ToolCatalog by consulting the action registry
// + a per-tool input-schema map + a per-tool router-dispatch closure.
type Catalog struct {
    Registry *controlapi.ActionRegistry
    Schemas  map[string]json.RawMessage // tool name → JSON schema
    Router   http.Handler                // the chi router this catalog dispatches into
}

func (c *Catalog) Filtered(r *http.Request) []Tool {
    ident, _ := controlapi.IdentityFromContextOK(r.Context())
    out := []Tool{}
    for _, name := range c.Registry.AllTools() {
        entry, _ := c.Registry.EntryForTool(name)
        if !auth.CheckGrant(ident.Permissions, entry.Action).Allowed {
            continue
        }
        schema := c.Schemas[name]
        if len(schema) == 0 {
            schema = []byte(`{"type":"object"}`)
        }
        out = append(out, Tool{
            Name:        name,
            Description: descriptionFor(name),
            InputSchema: schema,
        })
    }
    return out
}

func (c *Catalog) Invoke(r *http.Request, name string, args json.RawMessage) (any, *Error) {
    entry, ok := c.Registry.EntryForTool(name)
    if !ok {
        return nil, &Error{Code: CodeMethodNotFound, Message: "unknown tool: " + name}
    }
    // Translate arguments → an HTTP request matching the tool's
    // first registered route. Dispatch into c.Router. Capture the
    // response and return as the tool result.
    inner, err := buildHTTPRequest(r, entry.Routes[0], args)
    if err != nil {
        return nil, &Error{Code: CodeInvalidParams, Message: err.Error()}
    }
    rec := newRecorder()
    c.Router.ServeHTTP(rec, inner)
    if rec.Code >= 400 {
        return map[string]any{"error": rec.BodyJSON(), "status": rec.Code}, nil
    }
    return rec.BodyJSON(), nil
}
```

2. `buildHTTPRequest(parent, route, args)` translates MCP arguments to an HTTP request:
   - Path: substitute `{paramName}` placeholders in `route.Path` with values from `args`.
   - Body: for write methods, pass remaining args as a JSON body.
   - Headers: forward `Authorization` from the parent request so the inner auth middleware re-runs (the same identity is enforced again at the action gate, providing defense-in-depth).
3. `descriptionFor(name)` is a hardcoded map of MCP-tool-name → human description (copy descriptions from the existing `mcp-servers/control-api/tools.go`).
4. Tests:
   - Filtered catalog with `*:read` grant returns only read-shaped tools.
   - Filtered catalog with `*` grant returns every tool.
   - `Invoke("instance_list", {})` dispatches to the chi router and returns the list response.
   - `Invoke("unknown_tool", {})` returns `CodeMethodNotFound`.
   - For write-mode tools, dry-run is propagated end-to-end (verify the synthetic response surfaces).

**Verify:** `go test ./control/controlapi/mcp/... -count=1 -run TestCatalog`.

---

### H4. Mount `/mcp` in app.go

**Files:**
- `control/controlapi/app.go` (modified)
- `control/controlapi/mcp_route.go` (new — `registerMCPRoute`)

**Steps:**

1. Add `registerMCPRoute`:

```go
package controlapi

import (
    "github.com/go-chi/chi/v5"
    "github.com/fallguyconsulting/rimsky/control/controlapi/mcp"
)

func registerMCPRoute(r chi.Router, deps AppDeps) {
    catalog := &mcp.Catalog{
        Registry: deps.AuthState.Registry,
        Schemas:  builtinSchemas(),
        Router:   deps.SelfRouter,  // the chi router itself; circular but resolvable via late-binding
    }
    server := &mcp.Server{Tools: catalog}
    r.Post("/mcp", server.ServeHTTP)
}
```

   `deps.SelfRouter` is a new `AppDeps` field that holds the constructed router; set after `chi.NewRouter()` completes. Note the circular dependency: the catalog needs the router; the router needs the catalog mount. Resolution:
   - Build the router empty.
   - Build the catalog with a closure over a pointer that's set later.
   - Mount the `/mcp` route last, after all other routes are registered.

   Simpler alternative: use a lazy pointer:

```go
type routerRef struct {
    h http.Handler
}
func (rr *routerRef) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    rr.h.ServeHTTP(w, r)
}
```

   Build the `routerRef`, pass to the catalog, set `rr.h = built router` at the end of `NewApp`.

2. `builtinSchemas()` returns the `map[string]json.RawMessage` for tool inputSchemas; copy from `mcp-servers/control-api/tools.go` for the existing tools and synthesize for new ones (input schemas matching the route's request body / path params).

**Verify:** `go build ./...`. End-to-end MCP test in M2 exercises it.

---

### H5. MCP envelope: protocol-skin awareness in audit

**Files:**
- `control/controlapi/auth_middleware.go` (modified — propagate skin)
- `control/controlapi/mcp/catalog.go` (modified — set the skin)

**Steps:**

1. The `gateByAction` writes `protocol_skin: "http"` by default. For MCP-originated requests (those dispatched via the in-process router from `Catalog.Invoke`), the skin must be `"mcp"`.
2. The context key `ctxKeyProtocolSkin{}` and the helper `protocolSkinFromContext(ctx)` are already defined in `control/controlapi/auth_middleware.go` by task D2. **Do not re-declare them here.** The key type stays unexported; the only addition is an exported setter helper.
3. Add the exported setter to `control/controlapi/auth_middleware.go` (next to the existing `protocolSkinFromContext`):

```go
// WithProtocolSkin returns ctx tagged with the given protocol skin
// (e.g. "mcp"). The auth audit emitters read this back via
// protocolSkinFromContext; default skin is "http".
func WithProtocolSkin(ctx context.Context, skin string) context.Context {
    return context.WithValue(ctx, ctxKeyProtocolSkin{}, skin)
}
```

In `Catalog.Invoke`, before dispatching the inner HTTP request, set the skin on the request context via the new helper:

```go
inner = inner.WithContext(controlapi.WithProtocolSkin(inner.Context(), "mcp"))
```

4. In `emitAttempted` / `emitDenied` (already in `audit.go` from E2), the protocol-skin argument should be threaded from each call site. Confirm the call sites in `IdentityResolver` and `gateByAction` already pass `protocolSkinFromContext(r.Context())`. (They do, per the D2 + D6 code.) No further audit changes needed here.

**Verify:** `go build ./...`. L7 scenario tests will exercise both skins.

---

### H6. Delete the standalone `mcp-servers/control-api/` module

**Files:**
- `mcp-servers/control-api/` (deleted)
- `go.work` (modified)
- `Makefile` (modified)
- `deploy/Dockerfile.all` and similar (modified — drop references)

**Steps:**

1. Delete the directory: `rm -rf mcp-servers/control-api/`.
2. If `mcp-servers/` is empty, delete it too.
3. Edit `go.work` (the workspace file at repo root) — remove the line referencing the old module.
4. Grep the codebase for references to `mcp-servers/control-api`:
   - `Makefile`: any `build-mcp-control-api` target retires.
   - `deploy/Dockerfile.all`: any COPY / RUN building it retires.
   - `deploy/build-images.sh`: any image-build step retires.
   - CHANGELOG (will be updated in N1).

**Verify:** `go work sync && go build ./...` clean; `make build-all` clean.

---

### H7. MCP-protocol smoke

**Files:**
- `control/controlapi/mcp/server_test.go` (new — black-box test of the MCP server)

**Steps:**

1. With a fake `ToolCatalog`, exercise `initialize`, `tools/list`, `tools/call` over the JSON-RPC envelope.
2. Test that an unsupported method (`resources/list`, `prompts/list`) returns `CodeMethodNotFound`.

**Verify:** `go test ./control/controlapi/mcp/... -count=1 -run TestMCPServer`.

---

# Section I — CLI binary rename

### I1. Move `cmd/rimsky-cli/` → `cmd/rimsky/`

**Files:**
- `cmd/rimsky-cli/` → `cmd/rimsky/` (rename directory)
- All files in the directory: package declarations unchanged; module path references updated.

**Steps:**

1. `git mv cmd/rimsky-cli cmd/rimsky` (or `mv` if not using git tracking inside this task).
2. Grep for `cmd/rimsky-cli` across the codebase: `rg 'cmd/rimsky-cli'`. Edit each reference to point at `cmd/rimsky`:
   - Makefile build targets.
   - Dockerfiles (`deploy/Dockerfile.all`, `deploy/Dockerfile.cli` if exists).
   - CI YAML.
   - README mentions.
   - Any test that invokes the binary by path.
3. Inside the binary itself, grep for `rimsky-cli` as a string (e.g. usage text, help banner, error messages): `rg '"rimsky-cli"' cmd/rimsky/`. Replace with `"rimsky"`.

**Verify:** `make build-all` clean; the resulting binary is at `cmd/rimsky/` and named `rimsky`. The user-agent in the CLI's HTTP requests reads `rimsky/<version>` (not `rimsky-cli/<version>`).

---

### I2. No alias / compat shim

**Files:**
- (none — confirm absence)

**Steps:**

1. Grep for any post-rename references to `rimsky-cli` that suggest a compat path. There must be none (the spec says no alias shim).
2. Confirm by `rg 'rimsky-cli'` in the repo (excluding `.ok-planner/`, `CHANGELOG.md`, archived history): no results expected outside CHANGELOG.

**Verify:** Grep returns zero matches outside the workflow-scratch / changelog files.

---

### I3. Re-run `go work sync` after rename

**Files:**
- (none — workspace sync)

**Steps:**

1. `go work sync && make tidy`.

**Verify:** `make build-all && make test-all` clean.

---

# Section J — CLI auth subcommands

### J1. Embed bundled role JSONs

**Files:**
- `cmd/rimsky/roles/admin.json` (new — spec section "Bundled role templates (CLI-side)" / admin)
- `cmd/rimsky/roles/operator.json` (new — spec / operator)
- `cmd/rimsky/roles/read-only.json` (new — spec / read-only)
- `cmd/rimsky/roles/agent-supervisor.json` (new — spec / agent-supervisor)
- `cmd/rimsky/roles/publisher-service.json` (new — spec / publisher-service)
- `cmd/rimsky/roles/embed.go` (new — `//go:embed`)

**Steps:**

1. Copy each role JSON from the spec verbatim (lines 264–329 of the spec).
2. Create `embed.go`:

```go
package roles

import "embed"

//go:embed *.json
var FS embed.FS

// Load returns the bundled role JSON by name (without ".json").
// Returns ("", false) if not found.
func Load(name string) ([]byte, bool) {
    data, err := FS.ReadFile(name + ".json")
    if err != nil {
        return nil, false
    }
    return data, true
}

// AllNames returns the bundled role names sorted.
func AllNames() []string {
    entries, _ := FS.ReadDir(".")
    out := []string{}
    for _, e := range entries {
        n := e.Name()
        if len(n) > 5 && n[len(n)-5:] == ".json" {
            out = append(out, n[:len(n)-5])
        }
    }
    sortStrings(out)
    return out
}
```

**Verify:** `go build ./cmd/rimsky/...`.

---

### J2. Auth subcommand dispatcher

**Files:**
- `cmd/rimsky/auth.go` (new — top-level `auth` verb dispatcher)
- `cmd/rimsky/main.go` (modified — register `auth` verb)

**Steps:**

1. The existing CLI binary parses subcommands from `os.Args`. Find the dispatcher in `main.go` and add `auth` as a top-level verb:

```go
case "auth":
    return authCmd(subargs)
```

2. `authCmd` in `auth.go` parses the next argument and dispatches to `init`, `create-key`, `list`, `show`, `revoke`, `rotate`, `status`.

```go
func authCmd(args []string) int {
    if len(args) == 0 {
        fmt.Fprintln(os.Stderr, "usage: rimsky auth {init|create-key|list|show|revoke|rotate|status}")
        return 2
    }
    sub := args[0]
    rest := args[1:]
    switch sub {
    case "init":
        return authInit(rest)
    case "create-key":
        return authCreateKey(rest)
    case "list":
        return authList(rest)
    case "show":
        return authShow(rest)
    case "revoke":
        return authRevoke(rest)
    case "rotate":
        return authRotate(rest)
    case "status":
        return authStatus(rest)
    default:
        fmt.Fprintln(os.Stderr, "unknown auth subcommand:", sub)
        return 2
    }
}
```

**Verify:** `cmd/rimsky/rimsky auth --help` prints the usage line (after J3 lands).

---

### J3. `rimsky auth init`

**Files:**
- `cmd/rimsky/auth_init.go` (new)
- `cmd/rimsky/auth_init_test.go` (new)

**Steps:**

1. Implement:

```go
func authInit(args []string) int {
    fs, _, endpoint, code := runWithCommon("auth init", args, nil)
    if code != 0 {
        return code
    }
    _ = fs

    // Load bundled admin role.
    raw, ok := roles.Load("admin")
    if !ok {
        fmt.Fprintln(os.Stderr, "bundled admin role missing")
        return 1
    }
    var role struct {
        Permissions auth.Grant `json:"permissions"`
    }
    if err := json.Unmarshal(raw, &role); err != nil {
        fmt.Fprintln(os.Stderr, "decode admin role:", err.Error())
        return 1
    }

    // CLI UX nicety: refuse if any active key exists.
    if status, ok := fetchAuthStatus(endpoint, ""); ok {
        if status.Mode == "authenticated" {
            fmt.Fprintln(os.Stderr, "rimsky auth init: deployment is already authenticated (use 'rimsky auth create-key' instead)")
            return 1
        }
    }

    body, _ := json.Marshal(map[string]any{
        "name":        "admin",
        "permissions": role.Permissions,
    })
    resp, err := postJSON(endpoint+"/auth/keys", "" /* no token; anonymous */, body)
    if err != nil {
        fmt.Fprintln(os.Stderr, "rimsky auth init: POST failed:", err.Error())
        return 1
    }
    plaintext, _ := resp["plaintext"].(string)
    fmt.Fprintln(os.Stdout, "")
    fmt.Fprintln(os.Stdout, "Save this admin key now — it will not be shown again:")
    fmt.Fprintln(os.Stdout, "  "+plaintext)
    fmt.Fprintln(os.Stdout, "")
    return 0
}
```

   `runWithCommon` is the existing helper (grep `cmd/rimsky/admin.go` or similar). `postJSON` / `fetchAuthStatus` are small new helpers.

2. Test:
   - Spin up a stub control-api (httptest.Server) that accepts `POST /auth/keys` with no Bearer and responds with a fake plaintext.
   - Run `authInit([]string{})` with `RIMSKY_CONTROL_API` pointing at the stub.
   - Verify stdout contains "Save this admin key now" and the fake plaintext.

**Verify:** `go test ./cmd/rimsky/... -count=1 -run TestAuthInit`.

---

### J4. `rimsky auth create-key`

**Files:**
- `cmd/rimsky/auth_create.go` (new)
- `cmd/rimsky/auth_create_test.go` (new)

**Steps:**

1. Implement flags: `--name`, `--role`, `--role-file`, `--add` (repeatable), `--remove` (repeatable), `--dry-run` (repeatable), `--expires`.
2. Load the role JSON (from bundled or `--role-file`).
3. Apply `--add` / `--remove` / `--dry-run` patches:
   - `--add=<action>` appends `{action: X, mode: execute}`.
   - `--remove=<action>` removes any entry with exact `action == X`.
   - `--dry-run=<action>`:
     - Reject if action ends with `:read` (CLI-side nicety — dry-run is meaningless for reads).
     - Reject if action is `auth:create`, `auth:revoke`, `auth:rotate` (CLI-side rejection per spec).
     - Append `{action: X, mode: dry_run}`.
4. Validate the resulting grant via the action registry (the CLI doesn't have the registry; do the check server-side by POSTing — let the server reject unknown actions).
5. POST to `/auth/keys` with the resolved body. Print the plaintext.

2. Test:
   - `--role=read-only --add=node:invalidate` → grant has `[{*:read}, {node:invalidate}]`.
   - `--role=admin --dry-run=auth:create` → CLI exits with error before POSTing.
   - `--role=read-only --dry-run=instance:read` → CLI exits with error.
   - `--role-file=/tmp/custom.json` loads role from disk.

**Verify:** `go test ./cmd/rimsky/... -count=1 -run TestAuthCreate`.

---

### J5. `rimsky auth list`

**Files:**
- `cmd/rimsky/auth_list.go` (new)
- `cmd/rimsky/auth_list_test.go` (new)

**Steps:**

1. Flags: `--name-filter`, `--include-revoked`.
2. GET `/auth/keys?name_filter=...&include_revoked=...`.
3. Print as a table: `NAME  ID-PREFIX  ROLE-MATCH  CREATED  LAST-USED  EXPIRES`.
4. Role-match: fuzzy compare the grant against bundled role expansions; show `role:operator` or `role:operator + 1 override` if close, else `custom`.

**Verify:** `go test ./cmd/rimsky/... -count=1 -run TestAuthList`.

---

### J6. `rimsky auth show`

**Files:**
- `cmd/rimsky/auth_show.go` (new)

**Steps:**

1. Argument: `<name-or-id>`.
2. GET `/auth/keys/{name-or-id}`. Print the key record (all fields except plaintext, which the server never returns).

**Verify:** `go test ./cmd/rimsky/... -count=1 -run TestAuthShow`.

---

### J7. `rimsky auth revoke`

**Files:**
- `cmd/rimsky/auth_revoke.go` (new)

**Steps:**

1. Argument: `<name-or-id>`. Flag: `--force-leave-anonymous`.
2. DELETE `/auth/keys/{name-or-id}?force_leave_anonymous=<bool>`.
3. On 409, print the server's error message and exit nonzero.

**Verify:** `go test ./cmd/rimsky/... -count=1 -run TestAuthRevoke`.

---

### J8. `rimsky auth rotate`

**Files:**
- `cmd/rimsky/auth_rotate.go` (new)

**Steps:**

1. Argument: `<name-or-id>`. Flag: `--grace=<duration>` (default 24h).
2. POST `/auth/keys/{name-or-id}/rotate` with `{grace: <duration>}`.
3. Print the new plaintext with the "save now" banner.

**Verify:** `go test ./cmd/rimsky/... -count=1 -run TestAuthRotate`.

---

### J9. `rimsky auth status`

**Files:**
- `cmd/rimsky/auth_status.go` (new)

**Steps:**

1. GET `/auth/status`. Print the response as human-readable lines:
   - "Mode: anonymous (0 keys provisioned)"
   - "Mode: authenticated (3 keys total, 1 admin)"
2. **Missing-key tolerance.** `rimsky auth status` must work without `RIMSKY_API_KEY` or `--key`. In anonymous mode the server accepts the unauthenticated GET via the synthetic admin identity; in authenticated mode an unauthenticated request returns 401, which the CLI surfaces as `"error: auth required; set RIMSKY_API_KEY or pass --key"` and exits nonzero.
3. The `runWithCommon` helper used by other subcommands typically requires a key — bypass that requirement here. If `runWithCommon` doesn't support a "key optional" mode, add one (a flag in the common-args parser), and have `auth status` and `auth init` opt into it.

**Verify:** `go test ./cmd/rimsky/... -count=1 -run TestAuthStatus`. Test must cover both anonymous-mode (no key in env) and authenticated-mode (no key → 401) paths.

---

# Section K — Per-handler dry-run wiring

> **Goal:** Each write handler honors the per-request `mode` (from context) by running validation, optionally skipping the mutation, and returning a synthetic response with `dry_run: true` when mode is dry-run. Read handlers ignore mode entirely.

> **Pattern.** Each handler factors into `validate(req) -> (validation, errors)` and `execute(req, validation) -> response`. Dispatch:
> ```go
> validation, verrs := validate(parsedReq)
> if len(verrs) > 0 { return errors }
> if controlapi.ModeFromContext(ctx) == auth.ModeDryRun {
>     return syntheticResponse(parsedReq, validation)
> }
> response := execute(parsedReq, validation)
> return response
> ```

> **Audit.** The middleware already records `executed: false` when mode is dry-run and status < 400 (see E2). No per-handler audit change needed.

> **Verification rhythm for each K task.** Each task performs three concrete edits: (1) refactor the existing handler into `validate` and `execute` (no behavior change for the execute path); (2) add the dry-run branch returning the synthetic response; (3) add a new test that exercises the dry-run path. The Verify command at the end of each task runs the test pattern that matches the handler's package — it picks up BOTH the existing positive tests (which now exercise the refactored execute path) AND the new dry-run test. If a task lists a single test name, that test name is a `-run` regex that covers both paths. The existing-test-still-passes check is implicit in this; an explicit test of the existing positive path is the responsibility of the codebase's pre-spec test coverage and stays unchanged during the K-section work.

The following handlers need dry-run paths (all write actions except auth mutations):

### K1. `POST /instances` (instance:create)

**Files:** `control/controlapi/instances.go` (modified)

**Steps:**

1. Locate `handleCreateInstance`. Factor into `validateCreateInstance` and `executeCreateInstance`.
2. After validation succeeds, check `ModeFromContext`. If dry-run, return:
```json
{"dry_run": true, "would_have_created": {"instance_id": "dry-run-not-persisted", "template_hash": "<actual>", "params": {...}}}
```

**Verify:** `go test ./control/controlapi/... -count=1 -run "TestCreateInstance"`. Existing tests adapted to cover dry-run path.

---

### K2. `DELETE /instances/{idOrKey}` (instance:terminate)

**Files:** `control/controlapi/instances.go` (modified)

**Steps:**

1. `handleDeleteInstance` factor. Dry-run response: `{"dry_run": true, "would_have_terminated": {"instance_id": "<actual>"}}`.

**Verify:** `go test ./control/controlapi/... -count=1 -run TestDeleteInstance`.

---

### K3. `POST /templates` (template:register)

**Files:** `control/controlapi/templates.go` (modified)

**Note on naming.** The Go handler at `code:control/controlapi/templates.go::handleDeployTemplate` is the registration handler despite its name (it predates the post-2026-05-12 nomenclature resolution and was never renamed). The `template:register` *action* and `template_register` *MCP tool* names follow the spec; the Go-side handler name stays as it is. Don't rename the handler in this task — the action mapping in C2 (`POST /templates` → `template:register`) already resolves the mismatch at the registry level.

**Steps:**

1. Locate `handleDeployTemplate` (current name in the codebase). Factor it. Dry-run honors Validation mix-in calls (those are side-effect-free reads against producers, per the data-platform-extensions spec). Skip only the DB insert.
2. Dry-run response includes `{"dry_run": true, "would_have_registered": {"template_hash": "<computed>", "tag_changes": [...]}}`.

**Verify:** `go test ./control/controlapi/... -count=1 -run TestDeployTemplate` (test name matches the handler's existing Go name). Existing positive test still passes; new dry-run test exercises the synthetic-response path.

---

### K4. `POST /templates/{id}/deploy` (template:deploy) and K4b `undeploy`, K4c `deregister`

**Files:** `control/controlapi/templates.go` (modified)

**Steps:**

1. Each handler factors the same way. Dry-run responses describe the state transition that would have happened.

**Verify:** `go test ./control/controlapi/... -count=1 -run "TestDeployTemplateState|TestUndeployTemplateState|TestDeleteTemplate"`.

---

### K5. `POST /tags` (tag:create) and K5b `PUT /tags/{tag}`, K5c `DELETE /tags/{tag}`

**Files:** `control/controlapi/tags.go` (modified)

**Steps:**

1. Each factor and add dry-run path. Synthetic responses describe what would have changed.

**Verify:** `go test ./control/controlapi/... -count=1 -run "TestTag"`.

---

### K6. `POST /nodes/{id}/invalidate` (node:invalidate)

**Files:** `control/controlapi/nodes.go` (modified), `control/controlapi/admin_diagnostics.go` (modified — the legacy admin alias)

**Steps:**

1. Factor `handleInvalidateNode` and `handleAdminInvalidateNode`. Dry-run response: `{"dry_run": true, "would_have_invalidated": {"node_id": "<id>"}}`.

**Verify:** `go test ./control/controlapi/... -count=1 -run "TestInvalidateNode|TestAdminInvalidate"`.

---

### K7. `POST /nodes/{id}/reset` (node:reset)

**Files:** `control/controlapi/nodes.go` (modified)

**Steps:**

1. Factor `handleResetNode`. Validation includes the "node must be in `failed` state" check; that's part of validate(). Dry-run response: `{"dry_run": true, "would_have_reset": {"node_id": "<id>"}}`.

**Verify:** `go test ./control/controlapi/... -count=1 -run TestResetNode`.

---

### K8. `POST /instances/{id}/messages` (message:send)

**Files:** `control/controlapi/messages.go` (modified)

**Steps:**

1. Factor `handleCreateMessage`. Dry-run response: `{"dry_run": true, "would_have_sent": {"message_kind": "...", "target": "..."}}`.

**Verify:** `go test ./control/controlapi/... -count=1 -run TestCreateMessage`.

---

### K9. `POST /admin/lineage/prune` (lineage:prune)

**Files:** `control/controlapi/lineage.go` (modified)

**Steps:**

1. Factor `handleLineagePrune`. Validation: parse the prune-window arg. Dry-run response: `{"dry_run": true, "would_have_pruned": {"rows_count": <estimated>}}` — the estimate comes from a count query (side-effect-free).

**Verify:** `go test ./control/controlapi/... -count=1 -run TestLineagePrune`.

---

### K10. `POST /instances/{id}/backfills` (backfill:create) and K10b `POST /backfills/{op_id}/cancel`

**Files:** `control/controlapi/backfills.go` (modified)

**Steps:**

1. Factor `handleCreateBackfill` and `handleCancelBackfill`. Dry-run responses describe what would have been enqueued / cancelled.

**Verify:** `go test ./control/controlapi/... -count=1 -run "TestBackfill"`.

---

### K11. `POST /instances/{id}/assets/{alias}/materialize` (asset:materialize) and K11b `DELETE /instances/{id}/assets/{alias}`

**Files:** `control/controlapi/assets.go` (modified)

**Steps:**

1. Factor `handleAssetMaterialize` and `handleDeleteAsset`. Dry-run responses describe the would-have action.

**Verify:** `go test ./control/controlapi/... -count=1 -run "TestAsset"`.

---

### K12. Auth mutations explicitly NOT dry-runnable

**Files:** `control/controlapi/auth_handlers.go` (modified)

**Steps:**

1. In `handleCreateKey`, `handleRevokeKey`, `handleRotateKey`: ignore `ModeFromContext` and always execute. Server tolerates `mode: dry_run` on grant entries for these actions but the handler ignores it. Document via comments.

**Verify:** `go test ./control/controlapi/... -count=1 -run TestAuthDryRunIgnored`.

---

# Section L — Scenario tests

### L1. Bootstrap scenarios

**Files:**
- `test/scenarios/auth/bootstrap_test.go` (new)

**Steps:**

1. Fresh DB; control-api starts; `GET /auth/status` → anonymous.
2. POST /auth/keys with `admin` body, no Bearer → 201; plaintext returned.
3. Subsequent `GET /auth/keys` without Bearer → 401.
4. With Bearer = plaintext, `GET /auth/keys` → 200.
5. `rimsky auth init` called a second time (any way) → server returns 401 because keys exist.

**Verify:** `go test ./test/scenarios/auth/ -run TestBootstrap -count=1`.

---

### L2. Permission grant scenarios

**Files:**
- `test/scenarios/auth/permission_grant_test.go` (new)

**Steps:**

1. Mint a `*:read` key, attempt write → 403; read → 200.
2. Mint a `instance:*` key, attempt instance ops → 200; template ops → 403.
3. Wildcard semantics: mint a key with `auth:*`; verify `auth:create` allowed and `authority:create` rejected (because no such action; 400 — but also the wildcard would NOT match it).
4. First-match-wins ordering: mint a key with `[{instance:create, mode: dry_run}, {*}]`; POST /instances → dry-run response with 200. Mint another with `[{*}, {instance:create, mode: dry_run}]`; POST /instances → 201 (executed, the wildcard matched first).

**Verify:** `go test ./test/scenarios/auth/ -run TestPermissionGrants -count=1`.

---

### L3. Dry-run scenarios

**Files:**
- `test/scenarios/auth/dry_run_test.go` (new)

**Steps:**

1. Mint a key with `[{instance:create, mode: dry_run}]`. POST /instances → 200 with `dry_run: true` envelope; verify no row in `rimsky_instances`.
2. The audit row in `rimsky_events` has `executed: false`.
3. Same key with no `mode` (replace with `[{instance:create}]`) — POST /instances actually creates.
4. Dry-run for `template:register` runs `Validation` RPCs (mock a producer that records the call) and skips the DB insert.

**Verify:** `go test ./test/scenarios/auth/ -run TestDryRun -count=1`.

---

### L4. Rotation scenarios

**Files:**
- `test/scenarios/auth/rotation_test.go` (new)

**Steps:**

1. Mint admin key; rotate with `grace: 60s`. Both old and new plaintexts authenticate.
2. Fast-forward the clock (use a controllable fake `Clock`) past the grace; run `runtime.SweepRotationGrace` directly; old plaintext → 401 with `denial_reason: revoked_token`; new plaintext → 200.
3. Verify one `auth.key_rotated` and one `auth.key_revoked` (with `reason: rotation_grace`) event are present.

**Verify:** `go test ./test/scenarios/auth/ -run TestRotation -count=1`.

---

### L5. Revoke guard scenarios

**Files:**
- `test/scenarios/auth/revoke_guard_test.go` (new)

**Steps:**

1. Single active admin key; attempt revoke → 409.
2. `?force_leave_anonymous=true` → 200; subsequent unauthenticated request → handled as anonymous (admin); subsequent `POST /auth/keys` works without Bearer.

**Verify:** `go test ./test/scenarios/auth/ -run TestRevokeGuard -count=1`.

---

### L6. Anonymous transition scenarios

**Files:**
- `test/scenarios/auth/anonymous_test.go` (new)

**Steps:**

1. Start anonymous; mint key; verify subsequent requests require Bearer (assert through `IsAnonymousMode` and request behavior).
2. Revoke with force; verify anonymous resumes.
3. The cache TTL: after the first `IsAnonymousMode` call, mint a key; the next call within 1s may still return `anon: true` (cache); after `InvalidateAnonCache()` (called from create-key handler), the next call returns `anon: false`. Confirm the invalidation path in `handleCreateKey` is exercised.

**Verify:** `go test ./test/scenarios/auth/ -run TestAnonymous -count=1`.

---

### L7. MCP-skin scenarios

**Files:**
- `test/scenarios/auth/mcp_skin_test.go` (new)

**Steps:**

1. Mint a key with `[{*}]`. Make a request via `GET /instances/{id}` (HTTP) and via `POST /mcp { tools/call: instance_get }`. Both succeed; both audit rows present; the HTTP one has `protocol_skin: "http"`, the MCP one has `protocol_skin: "mcp"`.
2. Mint a key with `[{*:read}]`. `POST /mcp tools/list` → returns only read tools; `instance_create` is not in the list. `POST /mcp tools/call instance_create` → permission denied error in the JSON-RPC envelope (and an audit row).

**Verify:** `go test ./test/scenarios/auth/ -run TestMCPSkin -count=1`.

---

### L8. Audit content scenarios

**Files:**
- `test/scenarios/auth/audit_content_test.go` (new)

**Steps:**

1. Mint key; make a successful POST /instances; verify the `auth.access_attempted` event row has every field populated per E2's helper, including `request_params` verbatim, `key_name` denormalized.
2. After revoking the key, make a request with its plaintext; the `auth.access_denied` event has `denial_reason: revoked_token`, `key_id` populated (because the row was found, just inactive).
3. Pre-action-resolution denial (malformed token); event has `denial_reason: invalid_token`, `action: null`, `request_params: null`.

**Verify:** `go test ./test/scenarios/auth/ -run TestAuditContent -count=1`.

---

### L9. Anonymous-mode banner test

**Files:**
- `test/scenarios/auth/banner_test.go` (new)

**Steps:**

1. Start control-api with a fake logger that captures WARN messages. Confirm a WARN with the anonymous-mode banner appears at startup.
2. Mint a key. Wait for the next banner tick (or call `WatchAnonymousMode`'s internal once-and-return helper). Confirm no WARN.

**Verify:** `go test ./test/scenarios/auth/ -run TestAnonymousBanner -count=1`.

---

# Section M — Smoke + conformance

### M1. Smoke test extension

**Files:**
- `test/smoke/setup.go` (modified)
- `test/smoke/auth_smoke_test.go` (new)

**Steps:**

1. In `setup.go`'s `BootCluster` (or equivalent), after the cluster comes up:
   - Verify `GET /auth/status` returns anonymous.
   - Run `rimsky auth init` (call the CLI function directly, or shell out if the CLI binary is available).
   - Capture the plaintext; set `RIMSKY_API_KEY` for downstream test helpers.
   - Verify subsequent unauthenticated requests are rejected.
2. Add `auth_smoke_test.go` that calls the same flow end-to-end and additionally:
   - `rimsky auth create-key --name=test-ops --role=operator`.
   - `rimsky auth rotate test-ops --grace=1m`.
   - `rimsky auth revoke test-ops --force-leave-anonymous`.

**Verify:** `go test ./test/smoke/... -count=1 -run TestAuthSmoke`.

---

### M2. Conformance probe MCP extension

**Files:**
- `cmd/rimsky-conformance-probe/main.go` (modified)
- `cmd/rimsky-conformance-probe/auth_probe.go` (new)

**Steps:**

1. Add a probe mode (flag `--mode=auth-mcp` or new case) that:
   - Calls `POST /mcp initialize` and asserts the response advertises `tools` capability only.
   - Calls `POST /mcp tools/list` with an admin Bearer and asserts the catalog is non-empty.
   - Calls `POST /mcp tools/list` with a read-only Bearer and asserts the catalog is restricted to `*_list` / `*_get`.
   - Calls `POST /mcp tools/call instance_list` and asserts the result.
   - Calls `POST /mcp resources/list` and asserts `method not found`.
2. Wire into the existing probe `main.go` switch.

**Verify:** `go run ./cmd/rimsky-conformance-probe --mode=auth-mcp --endpoint=http://localhost:8080 --key=$RIMSKY_API_KEY` returns exit 0 against a running control-api with valid keys.

---

# Section N — Concept catalog + CHANGELOG + tidy

### N1. CHANGELOG entry

**Files:**
- `CHANGELOG.md` (modified)

**Steps:**

1. Add a bullet under `## Unreleased`:

```
- Add API-key auth, permissions, and structured audit to control-api,
  hosted in-process; MCP becomes a first-class control-api protocol skin
  at `POST /mcp` (tools-only V1; the standalone `mcp-servers/control-api/`
  module retires). Renames `rimsky-cli` → `rimsky`; adds `rimsky auth
  {init,create-key,list,show,revoke,rotate,status}` subcommands.
  Implicit-anonymous bootstrap; rotation with grace-period sweep;
  per-handler dry-run mode. See spec
  `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md`.
```

**Verify:** `grep "API-key auth" CHANGELOG.md` returns the new bullet.

---

### N2. Concept catalog: write new + updated entries

**Files (new):**
- `.ok-planner/design/concepts/api-key.md`
- `.ok-planner/design/concepts/permission.md`
- `.ok-planner/design/concepts/anonymous-mode.md`
- `.ok-planner/design/concepts/role-template.md`
- `.ok-planner/design/concepts/dry-run.md`

**Files (modified):**
- `.ok-planner/design/concepts/control-api.md` — "Agentic MCP shim" subsection update; standalone-module framing retires.
- `.ok-planner/design/concepts/event-log.md` — append `auth.*` event kinds.
- `.ok-planner/design/concepts/inertness.md` — append verbatim-request_params clarification.

**Files (renamed):**
- `.ok-planner/design/concepts/rimsky-cli.md` → `.ok-planner/design/concepts/rimsky.md` (binary rename); update body.

**Files (modified, clarifying):**
- `.ok-planner/design/concepts/rimsky-yml.md` — append "no auth-related keys" clarification.

**Steps:**

1. For each new file, write content derived from the spec body sections that define the concept. Use the concept template from `ok-planner:discover-design`'s SKILL.md — frontmatter with `concept: <slug>`, `## Definition`, `## Purpose`, `## Boundaries`, `## Invariants`, `## Notes` (append-only).

2. Each new file ends with a Notes entry citing the spec slug:

```
## Notes

- [2026-05-15] Concept introduced by spec
  `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md`
  ("control-plane MCP and auth"). See spec sections "Authentication
  model", "Permissions model", etc.
```

3. For modified files, update relevant sections in place and append a Notes entry citing the spec.

4. The renamed `rimsky.md` keeps `rimsky-cli.md`'s history if the underlying file is `git mv`'d.

**Verify:** Each `concepts/<slug>.md` file exists; the body matches the spec's described content. (No automated verify command; manual file inspection.)

---

### N3. Concept TOC regeneration

**Files:**
- `.ok-planner/design/concepts.md` (modified — regenerated)

**Steps:**

1. The TOC is auto-generated. If the project has a generator script, run it (grep `concepts.md` for the generator command or for a comment indicating the generator). If no generator exists, hand-update by sorting the existing TOC, adding entries for the new concepts, renaming `rimsky-cli` → `rimsky`, and updating the one-line definitions to match the new/updated concept files.

**Verify:** `concepts.md` lists exactly the files in `concepts/`; one-line definitions are non-empty.

---

### N4. Feature index update

**Files:**
- `feature-index.md` (modified or created if missing — at repo root)

**Steps:**

1. If `feature-index.md` exists at repo root, locate where control-api and CLI features are listed. Add entries for the new auth surface, MCP-as-skin, dry-run, rotation. If it doesn't exist, this task is a no-op (the project rules say "create if it doesn't exist yet" for feature index, but the spec is large enough that creating a feature index just for this is out of scope — defer to operator).

**Verify:** If `feature-index.md` exists, it mentions auth + MCP + dry-run.

---

### N5. Final tidy + full test sweep

**Files:**
- (none — running commands)

**Steps:**

1. `make tidy` — ensure go.mod / go.sum are updated across all three modules.
2. `make build-all` — clean.
3. `make lint` — clean.
4. `make test-all` — all unit tests pass.
5. `go test ./test/scenarios/... -count=1` — scenario tests pass.
6. `go test ./test/scenarios/... -count=1 -race` — race-clean.
7. `go test ./runtime/... ./foundation/persistence/postgres/... ./graph/scheduler/... -race -count=3` — race-clean on the hot paths.

**Verify:** All commands exit 0.

---

## Manual checks after completion

None. Every check in this plan is expressible as a Go test or a CLI invocation against a running stack. The user reviews via standard PR review after the implementation is complete.
