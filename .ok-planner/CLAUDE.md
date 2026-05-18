# .ok-planner — mostly workflow folder, with one durable subdirectory

This directory holds two kinds of content with different lifecycles
and different rules for how agents should treat them.

**Workflow scratch** (`specs/`, `plans/`, `sketches/`, `history/`):
point-in-time records of how a piece of work was conceived and
executed. Not living documentation of the codebase. Drift between
these files and the current code is expected.

**Durable design docs** (`design/`): the project's canonical
noun catalog — load-bearing concepts with definitions, purposes,
boundaries, and invariants — plus a tensions catalog tracking
what is unresolved. Code references the design (via `@concept:`
or equivalent annotations at points of enforcement), not the
other way around. The design docs are **a source of truth with
the same weight as code**: they describe the project as it
stands. Like code, they change only through plan execution —
`execute-plan` is the one skill that mutates them. NOT scratch.

## Default behavior for agents

### Workflow scratch (`specs/`, `plans/`, `sketches/`, `history/`)

Unless the user or an active skill (e.g. `/brainstorm`,
`/write-plan`, `/execute-plan`, `/review-plan`, `/sketch`)
explicitly directs you here, ignore these subdirectories:

- **Do not consult these files to understand the project.** They
  reflect what someone was thinking at a moment in time. The
  codebase is the source of truth; these artifacts are not.
- **Do not include `.ok-planner/` files in general repository
  exploration**, codebase walkthroughs, or "how does this project
  work" research. Skip them the same way you would skip a build
  directory.
- **Do not propose updating, refreshing, or reconciling these files
  with the current state of the code.** Drift between an old plan
  and the current code is expected and fine. The artifact stays as
  it was written.
- **Do not edit, rename, move, or delete files here on your own
  initiative**, even if they look stale, redundant, or wrong.

#### When it is OK to touch the workflow scratch

- The user explicitly asks (e.g. "update the spec at
  .ok-planner/specs/foo.md", "what did we decide about X — check
  the old plan").
- An ok-planner skill is running and directs you to read or write
  specific files here as part of its documented process.

In those cases, do exactly what the user or skill asked, then stop.
Do not expand the scope to "while I'm in here, I'll also fix..."

### Durable design docs (`design/`)

The `design/` subdirectory is **the exception**. It holds the
project's canonical concept catalog — load-bearing nouns with
definitions, purposes, boundaries, and invariants — plus a
tensions catalog tracking what's still muddy.

**Design docs change only through plan execution, like code.**
The reason: if the design docs and the code can change
independently, they drift, and the project ends up in an
inconsistent state. To prevent that, *all* design-doc changes
ride the same pipeline as code — a spec captures the change,
`write-plan` turns it into tasks, and `execute-plan` applies it.

Skills relate to the design docs in three modes:

1. **Modifier**: `execute-plan` is the one skill that mutates
   the design docs, carrying out plan tasks that the spec
   directs. (`discover-design` is the bootstrap exception: it
   creates initial design docs for a project that has none, or
   extends the catalog when new areas are discovered. It does
   not modify existing user-edited content.)

2. **Read-only oracle**: `review-holistic` reads the design
   docs as sacrosanct and uses them to drive whole-codebase
   convergence. There is no per-scope spec for that kind of
   review, so the design docs themselves are the source of
   truth.

3. **Read-only consumers**: every other skill reads the design
   docs without writing.
   - `brainstorm` and `refine-design` read them to understand
     "how the project is now," and capture any needed changes
     **in the spec they produce** (under a `## Design changes`
     section). The changes get applied later, by
     `execute-plan`.
   - `merge` reads the docs against a fresh merge or rebase
     and **surfaces findings** for the user — mechanical
     issues, drift, semantic conflicts. Resolution is
     human-driven and goes through the spec pipeline; merge
     does not write fixes to the design docs itself.
   - `write-plan` reads to plan spec-directed mutations as
     first-class tasks.
   - `review-work` and `review-plan` read to verify that
     spec-directed mutations landed correctly.

Layout (created by `/discover-design`):
- `_discover/` — as-is scaffolding. Wide, detailed, possibly
  redundant. Discarded once the design stabilizes.
- `concepts/` — one concept per file. Mutated in place by
  `execute-plan` per the spec's `## Design changes` section;
  Notes section is append-only.
- `tensions/` — one tension per file. Tensions move to
  `tensions/_resolved/` when `execute-plan` carries out a
  resolution-bearing spec.
- `review-notes.md` — agent-confessed uncertainty from the
  `/discover-design` run (judgment calls, suspected concepts,
  unresolved fix-loop issues). Consumed by `/refine-design`;
  not durable.

- **`review-holistic` consults `design/concepts/`** when forming
  judgments — a finding that contradicts a documented boundary
  or invariant needs an explicit "the documented concept is
  wrong because..." rather than a flag against the code.
  `brainstorm` and `refine-design` consult to understand current
  state; the spec captures any needed changes. Other skills do
  not consult as oracle.
- **Concept files (`concepts/<slug>.md`) are MUTABLE during
  `execute-plan`.** Editing Definition / Purpose / Boundaries /
  Invariants in place is the normal mode — this is the
  prescriptive design, not an ADR journal. The Notes section is
  the append-only audit trail. Mutations ride alongside the
  code changes that conform to them and stay reversible until
  the plan executes.
- **DO NOT delete concept files on your own initiative**, even
  if they look stale. Use `/merge` to surface drift; resolution
  goes through the spec pipeline.
- **DO NOT mutate `tensions/` entries outside the spec
  pipeline** — those entries record what the project considers
  unresolved.

#### Consulting the design docs: read `concepts.md` first

When `review-holistic`, `brainstorm`, or `refine-design` is
reading the design docs, the lookup pattern is:

1. **Read `.ok-planner/design/concepts.md` first** — a small
   auto-generated TOC of every concept with a one-sentence
   definition. It is the single artifact you can read in one
   shot to know what concepts the project traffics in.
2. **Grep for `@concept:` annotations in the code under
   consideration** — these are inline citations that say "this
   site implements concept X." Use `rg '@concept:' <path>` to
   find them locally; use `rg '@concept: <slug>'` to find every
   enforcement site for a specific concept.
3. **Read `concepts/<slug>.md`** for the full definition
   (purpose, boundaries, invariants) for any concept surfaced
   by step 1 or 2 that's load-bearing for the work at hand.

#### `@concept:` annotations

`@concept: <slug>` annotations in source code are inline
citations marking sites where a concept is enforced. They
make the next agent's lookup cheap. Same granularity
discipline as `@blessed-invariant`: annotate where the concept
is enforced or expressed, not every file that happens to touch
it. No carpet-bombing.

- **`execute-plan`** adds or modifies annotations when a plan
  task explicitly directs (the spec usually surfaces this when
  a concept is being introduced or changed).
- **`discover-design`** can seed initial annotations on
  bootstrap.
- **`review-holistic`** notes "consulted concept: X
  (annotation missing at <site>)" in its findings; a
  subsequent `execute-plan` run leaves the annotation when
  applying the fix.

No other skill annotates on its own. Annotations accrete
through `execute-plan` runs.

## Layout

- `specs/` — active specs from `/brainstorm` and
  `/refine-design` (workflow scratch)
- `plans/` — active plans from `/write-plan`, plus their
  `-divergences.md` reports written by `/execute-plan`'s
  divergence auditor (workflow scratch)
- `sketches/` — design sketches from `/sketch` (workflow scratch)
- `design/` — durable design docs (concepts + tensions; mutated
  only by `/execute-plan` via spec-directed plan tasks;
  bootstrapped by `/discover-design`)
- `history/specs/` and `history/plans/` — specs and plans
  archived here automatically when an execute-* skill finishes
  a plan (workflow scratch)
