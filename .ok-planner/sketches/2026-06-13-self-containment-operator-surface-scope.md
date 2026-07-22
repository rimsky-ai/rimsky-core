# Self-containment rule: what counts as a "path" in design-doc bodies

## Why this sketch

The ok-planner self-containment rule says concept, story, and decision
bodies are self-contained: no file or directory paths in any form, no
external-doc references, no quoted code, no code-locator pointers. Its
example list of disallowed citations is `foo/bar.go`,
`services/widget/`, `pkg:github.com/...`, bare URLs,
`code:foo.go::Symbol`. The intent the rule names is durability — the
artifact body has to survive the codebase being reorganized.

A holistic compliance sweep of the live design corpus surfaced roughly
seventy sites that a strict reading of the rule would flag but that
name *operator-facing surfaces* rather than internal code locators.
Routes the operator types into a client, config keys the operator
writes into the unified config, env-vars the operator sets at deploy
time, proto wire-field names visible at the network layer, CLI verbs,
project-internal image tags. The current rule text doesn't distinguish
"internal locator" from "operator-facing surface" and is silent on
whether the second category counts; cycle-3 review applied the strict
reading, and the strict reading produces an unfixable backlog because
the operator-facing surface is genuinely the right level for many
design statements (a story's acceptance section is often about what
the operator types and observes).

The agent-prose citation grammar at `.claude/rules/citation-grammar.md`
already names `route:POST /instances`, `cfg:persistence.blob.backend`,
and `env:RIMSKY_CONFIG` as legitimate prose-citation kinds — but its
scope clause restricts it to "live agent ↔ user dialogue" and
explicitly excludes the design corpus. So the grammar acknowledges
these categories exist as named surfaces; it does not currently
authorize them in design bodies.

## The disputed categories

Nine categories appear in live design docs that one reading classifies
as path-form violations and another classifies as operator-facing-
surface citations.

### 1. HTTP route literals

Examples seen in bodies: `POST /instances`, `GET /health`,
`POST /instances/{id}/messages`, `POST /v1/instances`, `GET /audit`.

A route names what an operator types into a client. It does not name
a code location; the route survives any handler refactor. Most often
in story acceptance/falsifier/proof sections, occasionally in decision
bodies that name the surface the decision is about.

### 2. Config-key literals

Examples: `templates.ref_validation_mode`, `persistence.driver`,
`persistence.blob.backend`, `tls`, `messages.idempotency_ttl_seconds`.

A config key names what an operator writes into the unified config.
Operator-facing wire surface, not a code pointer.

### 3. Env-var names

Examples: `RIMSKY_PROCESS_ROLE=unified`, `RIMSKY_ENTRYPOINT_MIGRATE`,
`RIMSKY_METRICS_PORT`, `RIMSKY_CONFIG`.

Operator-facing surface. Names what the operator sets in deploy
configuration.

### 4. Proto wire-field literals

Examples: `success | error | park` oneof labels in
`async-callback-outcome-oneof`; `version_id` and `producer_metadata`
in `wire-commit-response-fields`.

Proto wire-field names are operator-visible at the network layer
(observers parsing the wire format) and at the conformance-test
layer. They survive proto regeneration if the schema is unchanged.

### 5. Build / CLI literals

Examples: `make dev-release`, `rimsky conformance <protocol>`,
`docker buildx build --push --provenance=mode=max --sbom=true`.

Operator-facing surface — the literal text a release engineer or
operator types. Survives build-system internals.

### 6. V1 / pre-v1 version stamping

Examples: "single-replica is the v1 contract", "Pre-v1 migration
discipline: filenames are append-only", "Bare paths only; v1 does
not version the wire format".

The rule disallows forward-looking phrasing. "Pre-v1" is strictly
forward-looking (it implies "before v1 ships") but is also a
present-state designation for a project in its current phase.
"V1" stamps a stable contract whose visibility is operator-facing.

### 7. Image-tag references

Examples: `rimsky-all-in-one`, `golang:1.25-alpine`,
`gcr.io/distroless/static-debian12:nonroot`.

Project-internal tags (`rimsky-all-in-one`) are operator-facing
surface; external tags (`gcr.io/...`) are upstream identifiers the
design names to record a dependency choice.

### 8. Decision slugs that name the change-act

Examples: `fold-ownership-bail`, `parallel-cap-removal`,
`parity-expansion`, `memory-gate-premise-corrected`.

The current-state rule says the body describes the project as it
stands; a slug that names "the act of folding" or "the act of
removing the cap" carries the same backward-looking framing into the
artifact's identity. A rename ripples to every citation site, which
is why it isn't a trivial fix.

### 9. Code-locator catalogs in decision rationale

Example: `decisions/peer-tls-enforcement` enumerates "runtime's peer
clients (store, publisher, data-processing, validation), the
executor dial, and the observability-handshake dial" as the covered
dial sites.

A different shape from 1-7: the literal text doesn't name a code path,
but the enumeration *is* a list of code locations described in prose.
Probably belongs in a concept body that catalogs the dial-site set,
with the decision referencing the concept.

## Resolution shapes

Three coherent ways to resolve this.

### A. Tighten

Treat every category 1-8 as a self-containment violation. Sweep the
corpus path-free for operator surface as well as code surface.
Decision rationale stops naming the `tls` key; story acceptance stops
naming `POST /instances`; concept invariants stop stamping `v1`.

Costs: the design corpus loses a level of concreteness operators and
authors find useful. Many stories become harder to read because the
wire surface is what makes the user-outcome identifiable. Decision
rationale can become abstract enough that the choice it records
loses connection to the operator surface the choice is *about*.

Benefits: full durability — a route rename, a config-key rename, or
an env-var rename never invalidates a design doc. Maximum consistency
with the rule's existing text.

### B. Loosen — operator-surface carve-out

Amend the rule to distinguish "internal locator" (disallowed: file
paths, package paths, code symbols, SQL table/column names,
code-locator catalogs) from "operator-facing surface" (allowed:
routes, config keys, env-vars, proto field names, CLI commands,
project-internal image tags).

Rationale: the rule's stated intent is durability against *code
reorganization*. An operator-facing surface survives code
reorganization by definition — it is the contract with operators.
Naming it in a design body is the design speaking at the level it
operates on.

Costs: the rule's text becomes longer and the boundary becomes
something authors must keep track of. Edge cases multiply (is a
proto wire-field literal really operator-facing? what about a flag's
name on a CLI command?).

Benefits: matches the rule's apparent intent. Eliminates ~70 false
positives. Aligns the design corpus with the citation grammar's
existing named categories.

### C. Distinguish allowed citation by section

Allow operator-facing surface in specific sections of each artifact
kind, disallow in others. For example: a story's Acceptance/Proof
sections can name the route the user uses; a concept's Invariants
section cannot.

This is the most rule-as-text approach but the section boundaries
are hard to police and the value over (B) is marginal.

## Recommended direction

Resolution (B) — operator-surface carve-out — fits the rule's stated
intent and resolves the backlog without sweeping the corpus for
durable, operator-meaningful citations.

If (B) is taken, the rule's amended text would name allowed
operator-surface kinds explicitly:

- Routes (HTTP method + path)
- Config keys (dotted path into the unified config)
- Env-var names
- Proto wire-field names and oneof labels (interface surface, not
  implementation type)
- CLI verbs and flag names (operator-typed text)
- Project-internal image tags

And keep disallowed:

- File paths in any form
- Package paths (`pkg:github.com/...`, package-domain prefixes)
- Code symbols (`code:foo.go::Symbol`, Go identifiers, proto type
  names)
- SQL table or column identifiers
- External documentation references (`docs/...`, READMEs)
- Quoted code, quoted lint allowlists
- Code-locator catalogs (enumerations of code sites in prose)
- Backward-looking change narration in concept/story/decision
  bodies

Two sub-questions independent of (A)/(B):

- Pre-v1 / V1 version stamping. Probably allowed — current-state for
  the project's actual stage — but the rule text should authorize
  the pattern explicitly so the cycle-3 false-positive class doesn't
  recur.
- Decision slugs that name the change-act. Always a per-slug
  rename-or-keep call. The candidates are:
  - `fold-ownership-bail` → `ownership-bail-via-resolution-engine`
  - `parallel-cap-removal` → `test-parallelism-unconstrained`
  - `parity-expansion` → `driver-parity-coverage`
  - `memory-gate-premise-corrected` →
    `memory-blob-single-process-only`

Each rename ripples to every citation site (concepts, stories, other
decisions, code annotations) so it should land through a single
spec → plan execution rather than ad-hoc.

## Disposition

This sketch is input for `/refine-design` or `/brainstorm`. The
cycle-3 sweep's roughly seventy findings are pending the resolution
chosen here.
