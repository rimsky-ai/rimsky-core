# Collapse `sdk/go` into `protocols` — one public Go module

**Date:** 2026-05-26
**Status:** sketch / concrete refactor (pre-spec)
**Related:** `2026-05-14-rimsky-development-kit.md` — its open question #10
("the split between RDK and executor SDK") assumes a coherent Go-level
"executor SDK." This sketch is what makes that coherent: there is no
separate Go SDK; there is the `protocols` module, which is everything a
Go service implementer imports. The RDK (future, Python) is the
authoring layer *above* this; this sketch is the contract layer *below*
it.

## What this covers

A mechanical-but-decision-bearing restructure of rimsky's Go module
topology so that **`protocols` is the single public-facing Go module** a
service implementer depends on. Today there are two public Go modules a
consumer must wire up (`protocols` *and* `sdk/go`), the "SDK" carries a
postgres-specific dependency and some generic boilerplate, and the
contract types leak through an internal module (`foundation/locks`). This
folds the genuinely-useful parts of `sdk/go` into `protocols`, evicts the
parts that don't belong on the public surface, and makes the
public/internal line crisp.

Driven out of an external integration review: wiring up the first external
Go claim-producer consumer exposed every one of these seams. The cross-repo
follow-on (the external consumer's own migration) is Slice 2 below.

---

## Part 1: the framing

### "SDK" is the wrong label, applied to the wrong layer

A consumer expects "SDK" to mean *the one thing you import*. Today it's
the opposite: `sdk/go` is a thin helper layer that **sits on top of**
`protocols`, so "implement a service in Go" means "depend on `protocols`
AND `sdk/go`." That's inelegant, and it's backwards from the dependency
reality.

The dependency DAG (verified):

```
protocols   → (no rimsky deps)        ← the true base / lowest level
foundation  → protocols
sdk/go      → protocols
core (root) → foundation, protocols, sdk/go
```

`protocols` is the genuine leaf. Everything — internal core, foundation,
the SDK, and external implementers — depends on it. So the honest model
is: **the contract is the single shared base; "the SDK" is just enough Go
on top of the contract to implement against it.** That Go belongs *in*
the contract module, not in a satellite.

### What `protocols` actually is

Two things, both already in the module:

- **Generated protobuf bindings** — `protocols/proto/v1/gen` (19 `.pb.go`
  from 10 `.proto` IDL files: `claim_producer`, `executor`, `publisher`,
  `lifecycle`, `events`, `validation`, etc.). The wire contract, in Go.
- **Hand-written Go contract ergonomics** — `protocols/claimproducer`,
  `protocols/lifecycle` (friendly types: `ClaimResult`, `OpenOutcome`,
  `WriteSemantics` + `ParseWriteSemantics`, `Capabilities`, errors).

So protobuf ⊂ protocols. The `.proto` is the language-neutral source of
truth; a non-Go consumer regenerates from it. The `protocols` **module**
is the Go form of that contract.

### Why `protocols` can be the single public module here (vs. re-export)

The earlier option of "have the SDK re-export `protocols`" is awkward —
generated gRPC server interfaces and `Register*Server` funcs don't alias
cleanly. Collapsing the other direction (fold the helper code *into*
`protocols`) sidesteps that entirely: the generated bindings are already
in the module; the helpers move next to them; consumers import one
module. The only constraint that holds is the obvious one — nothing
opinionated (no DB driver, no test infra) may live in the contract
module.

---

## Part 2: what's in `sdk/go` and where each piece goes

Non-test LOC, with how rimsky-core uses each:

| Package | LOC | Nature | core uses | Destination |
|---|---|---|---|---|
| `conformance/*` | ~3,400 | executable contract spec (claimproducer, executor, publisher, validation, blobbackend, dataprocessing) | the 7 `cmd/rimsky-*-conformance` binaries + 2 store testfixtures | **→ `protocols/conformance/*`** (sub-package) |
| `stores/action` | 159 | post-claim action vocabulary (`Pop`/`Recycle`/`PopAndMove`/…) | **13×** | **→ `protocols/...action`** (contract vocab) |
| `server` | 615 | gRPC executor server bridge/lifecycle/observability scaffolding | 2× | **→ `protocols/server`** |
| `publisher` | 192 | publisher-implementation scaffolding | — | **→ `protocols/publisher`** |
| `ops` | 151 | generic `DSNFromEnv` / slog `Setup` / `HealthHandler` | 1× | **→ core `internal/`** (not public) |
| `testpg` | 127 | postgres testcontainer helper | via `internal/pgmigrate` | **→ its own opt-in module** |

Two of these are really *contract*, not "helpers", which is why core
leans on them: `stores/action` (protocol vocabulary, 13 internal call
sites) and `conformance/*` (the executable definition of "correctly
implemented", run by both internal binaries and external implementers).
They co-version with the contract → into `protocols`.

The genuine remaining "SDK" is `server` + `publisher` ≈ 800 LOC of thin
gRPC scaffolding. Not enough to be its own module or to earn the "SDK"
brand — it's just convenience that ships with the contract.

`ops` is generic Go service boilerplate (health endpoint, logger, DSN
from env) with nothing rimsky-specific. Publishing it is the same
overreach pattern as shipping a SQL-checks engine or a lock mechanism —
how a service does health/logging/DSN is the implementer's call. Demote
to core-internal.

`testpg` is the **only** postgres/docker coupling in the whole module:
`testcontainers-go/modules/postgres` (in `testpg.go`) and `pgx` (in its
test) are the sole reason those land in `sdk/go`'s `go.mod`. The store
scaffolding (`stores`, `stores/action`) and `conformance` are already
DB-neutral. rimsky itself is multi-DB (`foundation/persistence/sqlite`
exists), so a postgres-only test helper has no business as a first-class
dependency of the contract. Carve it into its own opt-in module so the
contract stays DB-agnostic; if test ergonomics matter per-DB, add a
sibling `testsqlite` later — never as a forced dep.

---

## Part 3: `foundation` stays (it's internal), one leak to close

`foundation` is **kept as-is** — name and location. The earlier
"foundation isn't foundational" instinct is two senses of the word:
`protocols` is the foundation of the *contract*; `foundation` is the
shared library of the *application* (it sits above the contract). And it
is *not* "persistence + locks" — it's an 8-concern internal library
(`auth`, `cascade`, `locks`, `matcher`, `persistence`, `shared`,
`signal`, `spec`); renaming it `persistence` would mis-name it the other
direction. 245 non-test files import it (456 with tests); a rename is a
large churn for negative clarity. Leave it.

The one actionable item: **`foundation/locks` is the wrong home for
contract types.** `locks/types.go` re-exports 17 `claimproducer` types as
aliases (`type ClaimResult = claimproducer.ClaimResult`, …) alongside its
genuinely-internal machinery (`Registry`, `ModeCoexists`, lifecycle
registry, late-bind proxies). External code that wanted four contract
types reached into `foundation/locks` to get them (an external consumer
did exactly this) — pulling an internal module into a consumer. Fix: make
`protocols/claimproducer` the canonical home; internal users that want
the contract types import `claimproducer` directly; the lock machinery
stays in `foundation/locks`. After this, no external consumer needs
`foundation` at all, which is the point — `foundation` becomes internal
in practice, not just in intent.

(Optional follow-up, not required here: push the now-private foundation
packages under `internal/` so external import is a *compile error* rather
than a convention. rimsky already uses `internal/` elsewhere. Deferred —
closing the `locks` leak removes the only known external reach.)

---

## Part 4: decisions (locked)

1. **`foundation`** — keep name + location; close the `locks` contract-
   type leak (canonical home → `protocols/claimproducer`).
2. **`conformance`** — sub-package of `protocols` (preserves single-
   import; it's the executable contract).
3. **`ops`** — not public; relocate to core `internal/`.
4. **`testpg`** — its own opt-in module (carries testcontainers + pgx);
   `protocols` stays DB-agnostic.
5. **`server` / `publisher` / `stores/action`** — into `protocols`.
6. Delete the `sdk/go` module; merge its grpc/protobuf requires into
   `protocols/go.mod`; update `go.work`.

---

## Part 5: blast radius + verification

- **23 non-test files** import `sdk/go/*` (32 with tests) — all repoint
  to `protocols/*`.
- **13 core sites** import `stores/action`; the conformance move touches
  the **7 `cmd/rimsky-*-conformance` binaries** + **2 store testfixtures**
  (`stores/{filesystem,postgres}/testfixture`); `internal/pgmigrate`
  repoints to the new `testpg` module.
- **`go.work`**: drop `./sdk/go`, add the `testpg` module path.
- **Done =** `go build ./...` + `go test ./...` green across every module
  (core, foundation, protocols, testpg) **and** the conformance suites
  pass. Update `.ok-planner/design/concepts/` (the "SDK" concept
  dissolves into `protocols`; record the module topology) + CHANGELOG +
  feature-index per rimsky conventions.

---

## Part 6: cross-repo follow-on (external consumer — Slice 2)

The first external Go claim-producer consumer is the reason these seams
surfaced. Once the above lands, that consumer repoints its files onto the
new layout:

- `foundation/locks` → `protocols/claimproducer` (contract types).
- the old SDK action-vocab path → `protocols`'s action vocab.
- drop any data-quality check vocab borrowed from rimsky — that's the
  implementer's own concern (a generic SELECT-only check runner is not a
  rimsky contract).
- fix any stale rimsky module path to `github.com/fallguyconsulting/rimsky`
  throughout.
- repoint the `go.mod` `replace` directives at the new layout; update the
  Dockerfile `COPY` + conformance script + build context.

Gated on Slice 1; stageable locally via `go.work` / `replace`. This is the
external consumer's own-repo task, not a rimsky one.

---

## Open questions

1. **`conformance` size inside the contract module.** ~3,400 LOC as a
   `protocols` sub-package keeps the single-import promise but bloats the
   module's source tree. Alternative: a sibling `conformance` module that
   pins `protocols`. Leaning sub-package (consumers compile only what they
   import), but worth a sanity check on whether the conformance suite's
   own deps (grpc clients) stay light enough to belong in `protocols`.
2. **`server` package naming inside `protocols`.** `protocols/server`
   reads slightly odd (the contract module hosting a server helper).
   `protocols/serverkit` or `protocols/executorkit`? Cosmetic; pick at
   spec time.
3. **`testpg` module path.** `sdk/go/testpg` as its own module, or hoist
   to a top-level `testpg` / `testkit`? Whichever, it must be opt-in and
   carry the postgres/docker deps alone.
4. **`internal/`-fencing of `foundation` (deferred).** Worth doing for
   compiler-enforced privacy, but it intentionally breaks any current
   external `foundation` import — sequence it only after the `locks` leak
   is closed and external consumers have migrated.
5. **Relationship to the RDK's "executor SDK" (sketch #10).** Confirm the
   framing: for Go, there is no separate SDK — `protocols` is it. The RDK
   is the Python authoring/packaging layer above; the hosted
   `python-runtime` is the Python equivalent of "import protocols + write
   handlers." Document the one-import Go story as the reference shape
   other-language RDKs mirror.
