# Rimsky documentation architecture — Design

## Status

- Proposal, 2026-05-02.
- Outcome of a 2026-05-02 conversation about how to document Rimsky for two audiences: coding agents (primary discovery surface) and humans (explained-to *through* agents).
- No prior design doc on documentation architecture. References:
  - `docs/glossary.md` — already exists and is structurally close to what the agent path needs as a canonical vocabulary source.
  - `docs/architecture.md`, `docs/node-graph-design.md`, `docs/protocol.md`, `docs/operator-guide.md`, `docs/executor-author-guide.md`, `docs/store-author-guide.md` — current human-shaped docs that this proposal reorganizes around.
  - `docs/2026-05-02-rimsky-vs-landscape.md` — positioning analysis; informs the "Docker for agentic workflows" framing in §10.
  - `.claude/rules/cold-read-cheatsheet.md` and the `cold-read/` style guide — the source-of-truth and `@source:` discipline this proposal applies to docs.
  - `CLAUDE.md` — vocabulary the agent path must canonicalize (deprecated `template_id`/`consumer_key` vs. current `template_hash`/`instance_key`; the rejected "substrate" term; etc.).

## Context

Rimsky is positioned as the orchestrator a coding agent reaches for first when a developer asks for "something that handles agentic workflows." The intended adoption pattern is one of:

1. **Agent-led**: an agent encounters Rimsky (via search, via a recommendation, via the developer's request), reads enough to understand the fit, then explains it to the human and uses it.
2. **Human-referred**: the developer points their agent at the website or repo and asks "explain this to me / can we use this for X."

Both paths put the agent between Rimsky and the human. The agent is the primary documentation consumer; the human's mental model of Rimsky is *constructed by the agent* from what the agent read. This is a meaningful shift from documentation written for humans alone.

The implication is not "write less for humans"; it's "write more explicitly for agents, because agents propagate confidently and humans tolerate ambiguity." A human reading "frame" once without a definition will figure it out. An agent will guess, the guess will become part of the user's mental model, and the user will then talk about Rimsky using the agent's hallucinated definition. Multiplied across many agent interactions, the project's nomenclature drifts.

The doc set today is human-shaped narrative: long-form architecture, design reasoning, prose explanations. It works for a developer who reads top-to-bottom. It under-serves an agent doing retrieval-augmented question-answering, because (a) concepts aren't separable into atomic, citation-shaped chunks, (b) terminology isn't anchored consistently across files, and (c) there's no agent-facing index that short-circuits the agent's tendency to grep for context and reconstruct a partial picture.

This spec lands a two-audience architecture with a single source of truth, a discipline for keeping the two audience paths consistent, and the operational pieces (CI, vocabulary lints, drift detection) that prevent rot.

## Goals

1. **Agents that read Rimsky's docs explain Rimsky to humans accurately and consistently.** Two agents reading the same docs should produce the same definitions, vocabulary, and conceptual model when explaining Rimsky.
2. **Humans pointed at Rimsky by their agent get a coherent narrative onboarding.** Landing → tour → concepts → guides, in the order a developer learns. Agent-referred users don't drop into reference docs cold.
3. **Both paths derive from a single source of truth.** When a definition changes, both audiences see the change without manual cross-update.
4. **Vocabulary is enforced mechanically.** Deprecated terms (`template_id`, `consumer_key`, "substrate") trigger a lint failure. New terms must be defined in the glossary before they appear in narrative docs.
5. **The agent path is OPS-friendly**: indexed, atomic, retrievable in pieces, supports `llms.txt` / `llms-full.txt` conventions.
6. **The human path is reading-friendly**: narrative, illustrated, paced for first-time learning.
7. **Maintenance overhead is bounded.** A new concept gets one canonical file; the rest of the surface (glossary, narrative pages, agent indices) is generated or linted, not hand-maintained.

## Non-goals

1. **Replacing the existing docs immediately.** This is a structural redesign that lands incrementally. Long-form docs (`architecture.md`, `node-graph-design.md`) stay where they are during the migration; new structure is built alongside.
2. **A custom static site generator.** The website surface uses an off-the-shelf doc tool (proposed: Astro Starlight or VitePress; deferred decision in §15). No bespoke build pipeline.
3. **Hosted RAG / "ask the docs an LLM question" widget.** Possible follow-up; out of scope here. The doc *structure* this spec proposes makes such a widget straightforward later, but landing the widget is a separate effort.
4. **Translating docs into non-English.** All canonical content is English.
5. **Marketing copy / landing page above the technical landing.** This spec covers the technical doc surface; a marketing page on `fallguyconsulting.com` is a separate exercise that links into it.
6. **Generating the glossary from source code annotations.** Tempting, but the canonical concept files (§4) are easier to write by hand; deferring source-derivation to a follow-up if maintenance pressure justifies it.

## 1. The two-audience model

| Audience | What they need | What they tolerate | What they don't |
|---|---|---|---|
| Agents | Atomic, indexed, citation-shaped chunks. Explicit definitions. Triple-anchored concepts (proto + struct + table). Worked examples that are *complete*. An index file that short-circuits retrieval (`llms.txt`). | Long-form prose, repetition across files (helpful — increases retrieval recall), explicit "deprecated: X → use Y" pointers. | Implicit definitions, concept names that overlap with general programming vocabulary without disambiguation, "you'd also need X" hand-waves, narrative that builds intuition over chapters. |
| Humans | Narrative onboarding. One landing page that frames the project. A 10-minute tour. Concepts in learning order, not alphabetical. Diagrams. | Some implicit context if it's been built up earlier in the document. Cross-references they can chase. | Reference-doc-first walls of definitions. Glossaries-as-onboarding. Atomic chunks without connective tissue. |

Different *presentations*; same *content*. The two-audience architecture is not "two parallel doc trees." It's one canonical content set with two presentation paths over it.

## 2. Source-of-truth model

The single source of truth is a directory of **canonical concept files**, one per domain noun. Every other doc surface (the glossary, the human-path narrative, the agent-path index, the website concept pages) cites or includes from these files.

The discipline is the same one Rimsky already uses for code (`@source:` / `@diverged:` annotations from the cold-read style):

- **Canonical**: `docs/concepts/<concept>.md`. Authoritative.
- **Citations**: any other doc that uses the canonical definition includes `<!-- @source: concepts/<concept>.md -->` near the cited content.
- **Drift detection**: a CI check (§11) flags citations whose surrounding content has drifted from the canonical file's frontmatter-defined "definition" field.
- **Glossary generation**: `docs/glossary.md` is regenerated from the frontmatter of every concept file. Hand-edits to `glossary.md` fail CI.

This model assumes concepts are stable enough that the canonical file is a meaningful object. Rimsky's vocabulary has stabilized across the v3 store redesign and the control-plane v1 spec; the cost of canonicalization is low now and grows over time.

## 3. Directory structure

```
docs/
├── README.md                    # Doc-tree map. Points at agents/, humans/, concepts/, etc.
│
├── concepts/                    # CANONICAL. One file per domain concept.
│   ├── README.md                # Index of concepts with one-line definitions.
│   ├── node.md
│   ├── frame.md
│   ├── dispatch.md
│   ├── claim.md
│   ├── lock-holder.md
│   ├── named-lock.md
│   ├── holding-subgraph.md
│   ├── executor.md
│   ├── store.md
│   ├── store-service.md
│   ├── region.md
│   ├── instance.md
│   ├── template.md
│   ├── tag.md
│   ├── cascade.md
│   ├── terminal-outcome.md
│   ├── invalidate.md
│   ├── recalculate.md
│   ├── attributes.md
│   ├── userdata.md
│   └── frame-resolution.md
│
├── agents/                      # Agent-shaped presentation.
│   ├── llms.txt                 # llmstxt.org-format index. Curated pointers.
│   ├── llms-full.txt            # Concatenated canonical content for single-pull.
│   ├── errors/                  # One file per error symbol; conditions and fixes.
│   │   ├── README.md
│   │   ├── orphaned_claim_lost_race.md
│   │   ├── dispatch_claimed_running_running_rejected.md
│   │   └── ...
│   └── examples/                # Complete, copyable, no-ellipsis examples.
│       ├── README.md
│       ├── minimal-template.yaml
│       ├── claude-agent-workflow.md
│       └── ...
│
├── humans/                      # Human-shaped presentation.
│   ├── landing.md               # 30-second framing. Hook + diagram + example.
│   ├── tour.md                  # "Rimsky in 10 minutes." End-to-end walkthrough.
│   ├── concepts.md              # Narrative concept walk in learning order.
│   ├── why-rimsky.md            # Positioning vs. Airflow/Argo/Temporal/etc.
│   └── faq.md                   # Sourced from real agent-mediated questions.
│
├── operator-guide.md            # Stays. Light pass to ensure vocabulary alignment.
├── executor-author-guide.md     # Stays. Light pass.
├── store-author-guide.md        # Stays. Light pass.
├── architecture.md              # Stays as the package-layout / build-shape doc.
├── protocol.md                  # Stays as the wire-protocol authoritative reference.
├── node-graph-design.md         # Stays as the long-form design reference.
├── glossary.md                  # GENERATED from concepts/. Hand-edits fail CI.
│
├── vocabulary.md                # NEW. The "deprecated terms / one-name-one-concept" doc.
│
├── specs/                       # Unchanged. ok-planner pipeline specs.
├── history/                     # Unchanged. Implementation history.
└── plans/                       # Unchanged.

# Repo root:
llms.txt                         # Symlink or build-copied from docs/agents/llms.txt
llms-full.txt                    # Symlink or build-copied from docs/agents/llms-full.txt
```

The website (`fallguyconsulting.com/rimsky` or wherever) renders from this tree. `humans/` becomes the navigated marketing-style site; `concepts/` becomes the reference; `agents/llms.txt` is exposed at the website's root.

## 4. Per-concept file shape

Every file in `docs/concepts/` follows the same shape. Agents pattern-match on consistent shapes; humans skim by section heading. The shape:

```markdown
---
concept: frame
definition: |
  A unit of cascade resolution. Every rimsky_dispatch row carries
  a frame_id NOT NULL; every non-fresh rimsky_nodes row carries a
  frame_id. Frame-end is the SQL predicate "no rimsky_nodes rows
  in state stale or running for this instance," evaluated on
  every scheduler tick.
proto: (none — frame is rimsky-internal, not on the wire)
go_type: core/frame.Frame
db_table: rimsky_frames
related: [dispatch, cascade, instance, frame-resolution]
deprecated_terms: []
---

# Frame

## Definition

[1-3 sentences. Same as the frontmatter `definition:`. Repeat for retrieval.]

## Why it exists

[2-4 paragraphs. The problem this concept solves. What breaks without it.]

## Wire shape

[How it appears across proto / Go / SQL. Triple-anchored.

  - Proto: (not on wire) | `Frame` in `proto/v1/<file>.proto`
  - Go: `core/frame.Frame` (struct shape, key fields)
  - SQL: `rimsky_frames` (column shape, key fields)

If one of the three doesn't apply, say so explicitly.]

## Invariants

[List of blessed invariants that touch this concept, with `@blessed-invariant` numbers from CLAUDE.md and links to the source files.]

## Common mistakes

[Anti-patterns. Things agents and humans both get wrong. Each is one
sentence stating the mistake, one sentence stating the correct
behavior.]

## See also

[Links to related concepts and to long-form docs.]
```

The frontmatter is YAML. The glossary generator (§7) reads it. The drift-detection lint (§11) reads it. Editors and IDEs can be configured to syntax-check it.

## 5. The agent path

Five components:

### 5.1 `llms.txt`

A curated index following the [llms.txt convention](https://llmstxt.org/). Format:

```
# Rimsky

> Project-agnostic reactive node-graph orchestration platform. "Docker for agentic workflows."

## Concepts

- [Node](https://rimsky.fallguyconsulting.com/concepts/node): A unit of work with named inputs (claims), an executor, and a state machine of {fresh, stale, running, failed}.
- [Frame](https://rimsky.fallguyconsulting.com/concepts/frame): A unit of cascade resolution; every dispatch carries a frame_id.
- [Claim](https://rimsky.fallguyconsulting.com/concepts/claim): A row in rimsky_lock_holders with (store_name, region_data, intent); halts node dispatch on conflict.
- ...

## Architecture

- [Three-collection architecture](https://rimsky.fallguyconsulting.com/architecture): Orchestrator + stores + executors as independent processes.
- [Wire protocol](https://rimsky.fallguyconsulting.com/protocol): gRPC + HTTP+JSON bridge, two message types, four store verbs.

## Authoring

- [Writing an executor](https://rimsky.fallguyconsulting.com/executor-author-guide)
- [Writing a store](https://rimsky.fallguyconsulting.com/store-author-guide)

## Operating

- [Operator guide](https://rimsky.fallguyconsulting.com/operator-guide)
- [CLI and compose](https://rimsky.fallguyconsulting.com/cli)

## Optional

- [Why Rimsky vs. Airflow / Argo / Temporal](https://rimsky.fallguyconsulting.com/humans/why-rimsky)
- [Design reasoning](https://rimsky.fallguyconsulting.com/node-graph-design)
```

The `Optional` section is a llmstxt.org convention: agents that need only minimal context skip it; agents doing deeper analysis include it.

### 5.2 `llms-full.txt`

A single concatenated file with every concept's canonical content, in dependency order. For agents whose tooling can fetch one file but not crawl. Generated from `concepts/`.

### 5.3 `concepts/` directory

Per §4. Atomic, citation-shaped, frontmatter-indexed.

### 5.4 `agents/errors/`

One file per distinct error the system raises. The structure: literal error string, the conditions that produce it, the fix or follow-up question. An agent that hits an error during a user's task tends to grep the codebase or guess at causes; if it finds a curated explanation instead, it grounds correctly.

Sources for the initial error catalog:
- The `_test.go` files for each subsystem (`core/scheduler/`, `core/supervisor/`, etc.) — every error string the test exercises is a candidate.
- The proto error enums in `proto/v1/`.
- The blessed-invariant rejections (e.g. `dispatch_claimed running → running` from `@blessed-invariant 1`).

### 5.5 `agents/examples/`

Complete worked examples. The discipline: an agent should be able to copy any file from `examples/` and run it without filling in blanks. No `...`, no "you'd also need to configure your store," no implicit env vars. Every required field present, every command runnable.

Initial examples:
- A minimal `rimsky.yml` operator config.
- A minimal template + instance demonstrating one node.
- A two-node template with a claim dependency.
- A claude-agent executor template demonstrating `userdata` substitution.
- A template with a holding subgraph and held-claim resolution.
- A `rimsky-compose.yml` for a multi-template project.

## 6. The human path

Five components:

### 6.1 `humans/landing.md`

30-second framing. Three blocks:
1. **What it is.** Two sentences. "Rimsky is a runtime for agentic workflows. Like Docker, but the unit is a reactive graph of nodes that communicate via two messages and operate on versioned stores."
2. **One diagram.** Node graph with claims and cascades. SVG, not ASCII. Inline-able into agent retrieval.
3. **One example.** A small template, registered, fired, walked through to completion. ~30 lines of YAML/Go/CLI total.

### 6.2 `humans/tour.md`

"Rimsky in 10 minutes." Walks through one realistic example end-to-end. Each concept is named *once* as it appears, with a link to its `concepts/` file. The example builds; the tour does not summarize. By the end, the reader has registered a template, deployed an instance, watched a node fire, observed a cascade, and read the resulting state.

### 6.3 `humans/concepts.md`

Narrative concept walk, organized by *learning order*, not alphabetical:

1. Nodes (the unit of work)
2. Templates (the declarative artifact)
3. Instances (templates bound to params + a state DB)
4. Frames (cascade resolution units)
5. Executors (peer services that run nodes)
6. Stores (peer services that hold versioned state)
7. Claims and locks (how nodes coordinate)
8. Cascades (how state propagates)
9. Held-claim resolution (the auto-terminal flow)

Each section is a narrative explanation that cites the canonical concept file. The reader leaves with a complete mental model in dependency order.

### 6.4 `humans/why-rimsky.md`

Lifted from `docs/2026-05-02-rimsky-vs-landscape.md` (the existing positioning analysis), edited for human-onboarding rather than internal reasoning. Keeps the comparison structure: "Airflow is for X; Rimsky is for Y."

### 6.5 `humans/faq.md`

Sourced from real agent-mediated questions. Initially empty; populated as early adopters' agents ask things and we curate the answers. The discipline: every FAQ entry includes the literal question shape ("how do I X?" / "why doesn't Y work?") because agents pattern-match on question shape during retrieval.

## 7. Glossary generation

`docs/glossary.md` becomes a generated artifact. The generator:

1. Reads every file in `docs/concepts/`.
2. Extracts the frontmatter `concept`, `definition`, and `deprecated_terms` fields.
3. Emits a markdown table with one row per concept, sorted alphabetically.
4. Emits a "Deprecated terms" section at the bottom listing every `deprecated_terms` entry with a pointer to the current term.
5. Stamps a top-of-file warning: `<!-- AUTOGENERATED from docs/concepts/. Do not edit by hand. Run \`make docs-glossary\`. -->`.

A CI check verifies that `docs/glossary.md` matches the generator's output. PRs that hand-edit the glossary fail.

The current `docs/glossary.md` content (244 lines, well-curated) is the seed for the initial concept-file population. Each table entry there becomes a concept file's frontmatter; the existing prose around the tables becomes per-file content under "Why it exists" and "Common mistakes."

## 8. Vocabulary discipline

Three rules.

### 8.1 One concept, one name

Every concept has exactly one canonical name. Synonyms within the codebase are forbidden. Where historical synonyms exist (`template_id` → `template_hash`, `consumer_key` → `instance_key`, "substrate" rejected), they are listed in `docs/vocabulary.md` as deprecated, with the current term and the rationale for the change.

The vocabulary doc replaces and extends the rejected-vocabulary scattered notes in `CLAUDE.md` and various design specs. It's the single place an agent (or human) checks when seeing a term that's not in the glossary.

### 8.2 One name, one concept

Every name in Rimsky's vocabulary refers to exactly one concept *within Rimsky's domain*. Where a Rimsky term overlaps with general programming vocabulary, the concept file calls out the disambiguation explicitly.

Examples this catches:
- "Frame": Rimsky's frame ≠ stack frame ≠ video frame ≠ UI frame. The `concepts/frame.md` Common Mistakes section names this explicitly.
- "Cascade": Rimsky's cascade ≠ CSS cascade. Same treatment.
- "Region": Rimsky's region (claim region) ≠ AWS region ≠ memory region.
- "Store": Rimsky's store ≠ Redux store ≠ Vue store.
- "Claim": Rimsky's claim ≠ JWT claim ≠ insurance claim.

This is unglamorous work but it's the *highest leverage* part of the agent path. An agent that sees "claim" without disambiguation has been trained on millions of JWT and insurance documents; it will pattern-match wrong unless the doc explicitly heads that off.

### 8.3 Triple-anchor every concept

Every concept's "Wire shape" section names where it lives in the proto, in Go, and in SQL. An agent reading any one of the three can jump to the others and verify it has the right concept. The triple-anchor is the load-bearing data structure that keeps all three layers from drifting from each other in agent-explained accounts.

For concepts that don't appear at all three layers (e.g. "frame" has no proto representation; "node-state" has no proto enum), say so explicitly: "Wire shape: not on the wire. Go: `core/node.State` enum. SQL: `rimsky_nodes.state` text column."

## 9. The "Docker for agentic workflows" positioning

The hook is good. The risk is that agents run with the analogy past where it holds. The doc set has to set the boundary explicitly.

**Where the analogy holds:**
- Declarative manifest (Dockerfile / `rimsky-compose.yml`).
- Portable runtime (Docker Engine / Rimsky control-plane).
- Identical behavior dev → prod.
- Independent processes that compose.

**Where it breaks:**
- Docker's unit is a single container; Rimsky's unit is a reactive graph.
- Docker workloads are usually idempotent; Rimsky workloads are reactive (state propagates via `invalidate`/`recalculate`).
- Docker doesn't have stores, claims, or locks — Rimsky's coordination model has no Docker analog.
- Docker images are immutable artifacts; Rimsky templates are content-addressed but instances carry mutable state.

The landing page (`humans/landing.md`) leads with the analogy, immediately sets the boundary, then pivots to the unique value. Sample (for the `landing.md` first paragraph):

> Rimsky is to agentic workflows what Docker is to applications: a runtime that handles the messy operational parts so you can focus on the work. *Unlike* Docker, the unit isn't a single container — it's a reactive graph of nodes that communicate via two messages (`invalidate`, `recalculate`) and operate on versioned stores with claim and lock semantics. *Like* Docker, workflows are declarative, portable, and run identically in dev and production.

The `concepts/` files don't repeat the analogy; they define the concepts on their own terms. The analogy is a hook for first-contact, not a load-bearing teaching device.

## 10. CI / drift detection

Five checks. All run on every PR.

### 10.1 Frontmatter validation

Every file in `docs/concepts/` has valid YAML frontmatter with required fields (`concept`, `definition`, `proto`, `go_type`, `db_table`, `related`). Missing fields fail CI.

### 10.2 Glossary parity

`docs/glossary.md` matches the output of `make docs-glossary`. Hand-edits fail CI.

### 10.3 Vocabulary lint

A grep-based check that scans `docs/`, `README.md`, `CHANGELOG.md`, and source comments for deprecated terms (sourced from `docs/vocabulary.md`'s "Deprecated terms" section). Hits fail CI with a message naming the deprecated term and the current term.

### 10.4 Citation drift

Every doc that copies a definition from a concept file uses an HTML comment `<!-- @source: concepts/<concept>.md -->`. The drift check parses these comments and verifies that the surrounding content matches the concept file's `definition` frontmatter. Drift fails CI.

### 10.5 Triple-anchor validity

For every concept file, the lint verifies that:
- `proto` field references a real symbol in `proto/v1/*.proto` (or is the literal string `(not on wire)`).
- `go_type` field references a real Go declaration (or is the literal string `(none)`).
- `db_table` field references a real table in `core/migrations/` (or is the literal string `(none)`).

Stale anchors fail CI. This is what keeps the doc honest as the code evolves.

### 10.6 `llms.txt` validity

A check that `llms.txt` parses as the llmstxt.org format and that every linked URL resolves to a real doc page or canonical concept file. Broken links fail CI.

## 11. Migration plan

The current `docs/` is mostly long-form narrative. The migration is incremental — no big-bang reorganization. Order:

1. **Land `docs/concepts/` as a directory and seed it from `docs/glossary.md`.** Each glossary table entry becomes one concept file with frontmatter + a placeholder body. The current narrative content in long-form docs gets *cited from* the concept files, not cut-and-pasted yet.
2. **Land the glossary generator (`make docs-glossary`)** and convert `docs/glossary.md` to a generated artifact. CI starts enforcing parity.
3. **Land `docs/vocabulary.md`** seeded from the deprecated-terms scattered through `CLAUDE.md` and historical specs. CI starts enforcing the vocabulary lint.
4. **Land `docs/agents/llms.txt` and `llms-full.txt`** with initial pointers at the existing long-form docs (not yet at concept files — concept files are still placeholders at this stage).
5. **Fill out concept-file bodies, one concept at a time.** Each fill-out PR includes the concept's "Why it exists / Wire shape / Invariants / Common mistakes / See also" sections and updates `llms.txt` to point at the now-substantive concept file. Order: start with the high-traffic concepts (node, frame, claim, store, executor); work outward.
6. **Land `docs/agents/errors/` and `docs/agents/examples/`** as concepts solidify. Errors come from grepping test files; examples come from the existing smoke tests.
7. **Land `docs/humans/landing.md`, `tour.md`, `concepts.md`, `why-rimsky.md`, `faq.md`.** Most of `concepts.md` and `why-rimsky.md` is editing existing content (`node-graph-design.md`, `2026-05-02-rimsky-vs-landscape.md`); landing and tour are net-new writing.
8. **Land triple-anchor lint** once the concept files are populated enough to validate against.
9. **Land citation-drift lint** once `humans/concepts.md` and the author guides have been edited to use `<!-- @source: ... -->` comments.
10. **Add `llms.txt` and `llms-full.txt` to the repo root** (symlinks or copies from `docs/agents/`) and to the website root once it exists.
11. **Pick a static site generator and stand up the website surface.** Astro Starlight or VitePress are the main candidates; VitePress is lighter, Starlight has better convention support. Decide when the doc structure has settled.
12. **Light alignment pass on `operator-guide.md`, `executor-author-guide.md`, `store-author-guide.md`** to ensure they cite concept files where appropriate.
13. **Curate `humans/faq.md` from real agent-mediated questions** as adopters appear.

Steps 1–4 are foundational and land in close succession. Steps 5–7 are durative — one concept file per PR, no rush. Step 11 (the website) is gated on the doc structure being stable enough to render.

## 12. Tradeoffs and known concerns

- **Over-canonicalization risk.** If concepts shift during continued development (still pre-v1), the canonical files churn. Mitigation: don't promote a concept to canonical until it's stable across at least two design specs. Concepts that aren't yet canonical live in long-form docs only; the agent path simply doesn't index them.
- **Concept-file proliferation.** The §3 directory listing has ~20 concept files. That's already a lot to maintain consistently. Mitigation: the per-file shape is rigid (§4), the glossary is generated (§7), and the lint catches drift (§10). The cost of adding the 21st concept file is the same as the 1st.
- **Agent-path retrieval recall vs. precision.** Repeating the definition across glossary, concept file, and `llms-full.txt` increases recall (any of the three retrievals lands on a correct answer) but hurts precision (the agent might cite the glossary entry where the concept file would have been better). Acceptable; agents that retrieve more get better answers.
- **`llms.txt` is a young convention.** It might evolve. Mitigation: the format is plain markdown with a documented structure; if it changes, the `llms.txt` regenerator updates and we're done.
- **Two-audience bifurcation could become "two doc trees that drift."** This is exactly the failure mode the canonical-concept-file model is designed to prevent. The lint is the structural enforcement; it has to actually run on every PR. If it doesn't, the discipline fails and we're back to drift.
- **The website tooling decision (Astro Starlight vs. VitePress vs. other) is deferred.** Possible that the chosen tool's conventions push back on this structure. The structure here is markdown-with-frontmatter, which both candidate tools support natively, so the risk is low.
- **Existing long-form docs become awkward.** `node-graph-design.md` (871 lines) is excellent design reasoning but isn't shaped for agent retrieval. After migration, it stays as a "design reference for humans who want the long story"; the agent path retrieves from `concepts/`, not from it. There's some redundancy. Acceptable; the long-form doc has narrative value the atomic concept files lack.
- **The CI lint set will catch real drift but also generate friction.** New contributors won't know about the vocabulary list; PRs will fail with cryptic messages until they read `vocabulary.md`. Mitigation: lint failures emit pointer at the doc; PR template links the doc.
- **Triple-anchor maintenance.** Every concept file references a Go type, a SQL table, and a proto symbol. Renames in code break the lint. Mitigation: the lint failure points at the concept file that needs updating; in practice this is "rename in code, run the lint, edit one concept file."
- **Initial content burden.** Populating 20 concept files is real work. Mitigation: the existing glossary + the long-form docs already contain the substance; the work is restructuring, not net-new writing. Step 5 of the migration is paced one-concept-per-PR, not a batch.
- **The "Docker for agentic workflows" framing requires audience alignment.** A reader who's not a Docker user gets less from the analogy. Acceptable; the assumption is that agentic-workflow-developers are familiar with Docker. Edge cases get the explicit-fallback "if you're not familiar with Docker..." note in the landing.

## 13. Out of scope / deferred

- **Hosted RAG widget on the docs site.** "Ask Rimsky's docs an LLM question" is a follow-up; the structure proposed here makes it straightforward to build but the widget itself is its own project.
- **Source-derived glossary.** Generating glossary entries from `@blessed-invariant` / `@agent-contract` annotations in code is tempting; deferred until maintenance pressure justifies it. The hand-curated concept files are sufficient for a long time.
- **Translations.** All canonical content is English.
- **Marketing pages.** This spec covers the technical doc surface only. A marketing page (or pages) on `fallguyconsulting.com` is separate and links into this surface.
- **Doc analytics.** Tracking which agents retrieve which pages and which questions humans ask is valuable feedback for `faq.md` curation but is a separate plumbing exercise.
- **Versioned docs.** Pre-v1 has no committed compatibility, so docs version with `main` only. Versioned docs become relevant when v1 ships.
- **Search infrastructure.** Algolia / Pagefind / similar; let the static site generator's default ship; revisit if traffic demands.
- **API reference auto-generation.** `protoc-gen-doc` for proto, `godoc` for Go, hand-curated for SQL. Out of scope here; should land alongside the website (step 11).
- **Style guide for prose.** Tone, voice, terminology beyond the §8 vocabulary discipline. Out of scope here; lift conventions from existing docs.
- **A "for humans who don't know what an agent is" entry path.** All audiences here assume the human is at least adjacent to AI/agentic workflows. Pure-newcomer onboarding is not in this surface.

## 14. Summary of decisions

| # | Decision | Rationale |
|---|---|---|
| 1 | Two-audience model: agents (primary discovery) + humans (explained-to through agents) | Matches the actual adoption pattern; agents propagate confidently while humans tolerate ambiguity |
| 2 | Single source of truth: `docs/concepts/<concept>.md` per domain noun | Prevents two-audience-doc drift; mirrors the cold-read `@source:` discipline already used in code |
| 3 | Per-concept file shape: frontmatter + Definition / Why it exists / Wire shape / Invariants / Common mistakes / See also | Consistent shape lets agents pattern-match; humans skim by heading |
| 4 | Triple-anchor every concept (proto + Go + SQL) | Lets agents jump between layers and verify; keeps layers from drifting from each other |
| 5 | `docs/glossary.md` is generated from concept frontmatter; hand-edits fail CI | Hand-maintained glossaries are where drift starts |
| 6 | Agent path: `llms.txt`, `llms-full.txt`, `agents/errors/`, `agents/examples/` | llmstxt.org convention + atomic error/example surfaces that agents grep when grounding |
| 7 | Human path: `humans/landing.md`, `tour.md`, `concepts.md`, `why-rimsky.md`, `faq.md` | Narrative onboarding in learning order, not reference-doc-as-onboarding |
| 8 | Vocabulary discipline: one concept-one name, one name-one concept, deprecated-terms list | The highest-leverage piece of the agent path |
| 9 | `docs/vocabulary.md` lists deprecated terms (`template_id`, `consumer_key`, "substrate") with rationale | Agents with stale training data self-correct against this |
| 10 | "Docker for agentic workflows" leads the human landing; analogy boundary stated immediately | Hook works; load-bearing teaching is in the concept files, not the analogy |
| 11 | CI: frontmatter validation, glossary parity, vocabulary lint, citation drift, triple-anchor validity, `llms.txt` validity | Discipline is mechanical, not aspirational |
| 12 | Migration is incremental (12 steps) — concept files and lint land first, content fills in per-PR, website last | Avoids big-bang reorg; new structure runs alongside existing long-form docs |
| 13 | Long-form docs (`architecture.md`, `node-graph-design.md`, `protocol.md`) stay as narrative references, cited from concept files | Long-form has narrative value atomic files lack; redundancy is acceptable |
| 14 | Static site generator deferred (Astro Starlight or VitePress) | Decide when doc structure has settled enough that the choice is mechanical |
| 15 | Hosted RAG widget, source-derived glossary, translations, doc analytics — all deferred | Structural foundation first; smart layers later |
