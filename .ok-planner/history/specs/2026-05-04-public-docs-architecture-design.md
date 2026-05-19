# Public documentation architecture — Design

## Status

- Spec, 2026-05-04.
- Outcome of a 2026-05-04 brainstorm scoping the documentation-architecture work that gates "use Rimsky in our own projects."
- Supersedes `docs/future-work/2026-05-02-documentation-architecture-design.md` (a pre-layer-crystallization sketch). The structural intuitions there carry forward; the vocabulary, layout, source-of-truth model, and scope have all moved.
- The package-manager work in `docs/future-work/2026-04-26-package-manager.md` is **out of scope** here and lands separately.

### Post-implementation revision (2026-05-04, same day)

After the initial 28-task implementation landed and was reviewed, the public-vocabulary decisions in §6 row 5, §7.2, §13.4 (and the file-list / concept-set in §4 / §6) were revisited. Net changes from this spec as originally written:

- **Message vocabulary collapsed from two to one.** `invalidate` is the only graph-level message. "Recalculate" is a verb describing what the scheduler does to a stale node — not a peer message. `concepts/recalculate.md` was retired.
- **Four-layer model retired from the public surface.** It is implementation detail: useful for engineers reading `foundation/`, `modeling/`, `protocols/`, but not consumer-facing. `concepts/four-layer-model.md` was retired; `humans/landing.md` and `humans/concepts.md` no longer lead with it.
- **`layer_senses:` frontmatter and "Layer senses" prose sections retired.** The `claim-producer.md` distinction between protocol-level prose and the colloquial "store" survives as a "Naming" section, no four-layer-model invocation.
- **Frame-resolution merged into `frame.md`.** It was always "the policy that governs how concurrent invalidates are handled" — a paragraph-shaped concept, not file-shaped. `concepts/frame-resolution.md` was retired.
- **Error-action vocabulary expanded from three to five.** The supervisor's policy chain actually resolves to `retry`, `discard_then_retry`, `resume_then_retry`, `invalidate(targets)`, `give_up`. The "three actions" framing was a simplification that operators couldn't reconcile with what they saw in error logs.
- **`auto-terminal` no longer named on the public surface.** Held-claim resolution is described in plain terms ("Rimsky fires exactly one automatic resolution at holding-subgraph completion"); the internal mechanism name is implementation detail.

Final concept count: 20 files (down from the originally-planned 23). See `docs/plans/2026-05-04-public-docs-architecture-plan-notes.md` for the per-decision rationale.

The remainder of this spec is preserved as the original design record; sections below that name retired concepts (e.g. `four-layer-model.md`, `recalculate.md`, `frame-resolution.md`) reflect the original intent, not the shipped surface.

## Context

Rimsky has no public documentation surface today. Everything currently in `docs/` (except `licensing.md`) is internal/working material the team uses; `docs/internal/`, `docs/specs/`, `docs/plans/`, `docs/history/`, `docs/future-work/` are all ephemeral or unmaintained. Eventually those move out of the rimsky tree entirely (sibling-folder layout, with rimsky as a submodule). For the purposes of this work, the public surface and the internal/working surface are decisively separate: **the public surface is fully self-contained and never cites or references internal/working docs.**

The two-audience adoption model assumed by this spec:

1. **Agent-led**. A developer asks their coding agent for "something that handles agentic workflows." The agent encounters Rimsky, reads enough of the public surface to ground itself, and explains it to the developer.
2. **Human-referred**. The developer points their agent at the public surface and says "explain this to me / can we use this for X."

Both paths put the agent between Rimsky and the human. The agent is the primary documentation consumer; the human's mental model is constructed by the agent from what the agent retrieved. The implication is that the public surface must be agent-shaped (atomic, indexed, vocabulary-disciplined, citation-shaped) so that two agents reading it produce the same definitions, vocabulary, and conceptual model when explaining Rimsky.

The human path is intentionally thin in this spec: concepts walk + "point your favorite coding agent at the docs" framing + dashboard UI guide. Use-case showcases ("here is where Rimsky earns its architecture") and any "why Rimsky vs. X" positioning are deferred — it is too soon to make those claims; they will land later through a series of examples (existing `docs/examples/` is the deferred surface for this).

## Goals

1. **Two agents reading the public surface produce the same definitions, vocabulary, and conceptual model when explaining Rimsky to a human.**
2. **An agent that reads the public surface can implement a custom `Executor`, `ClaimProducer`, or `LifecycleSubscriber` against the wire protocol without reading source code or internal docs.**
3. **A human pointed at the public surface by their agent gets a coherent narrative onboarding** — what Rimsky is, the canonical concepts in learning order, and how to use the dashboard.
4. **The public surface is fully self-contained.** Cites within itself only. Does not cite `docs/internal/`, `docs/specs/`, `docs/plans/`, `docs/history/`, `docs/future-work/`, or source-code internals (no Go-package references, no SQL-table references, no `@blessed-invariant`-number references). Proto file paths are public artifacts and *are* citable since they carry the wire contract.
5. **Vocabulary is enforced mechanically.** Deprecated terms trigger a lint failure on the public surface. New canonical terms get a concept file.
6. **The agent path is OPS-friendly**: indexed via `llms.txt`, atomic per-concept files, complete copy-pasteable examples, curated error catalog.
7. **Maintenance overhead is bounded.** A new concept gets one canonical file; the glossary regenerates, the lints catch drift, no other surface needs hand-update.

## Non-goals

1. **Replacing or maintaining the existing internal docs.** `docs/internal/` is unmaintained going forward. Content is *lifted* from it once into the public surface; after lift, the internal version is no longer authoritative for anything user-facing.
2. **A static site generator.** No Astro Starlight / VitePress / similar decision in this spec. The public surface renders via GitHub markdown for now; the SSG decision is deferred to a follow-up when the doc structure has settled.
3. **Hosted RAG widget on the docs site.** Possible follow-up; out of scope. The structure proposed here makes such a widget straightforward later.
4. **Translations.** All canonical content is English.
5. **Marketing copy.** Any "why Rimsky vs. Airflow / Argo / Temporal" positioning is deferred to the case-making example surface (`docs/examples/`).
6. **Generating concept files from source-code annotations.** Tempting but premature; concept files are hand-curated. Source-derivation is a follow-up if maintenance pressure justifies it.
7. **A 1000-line public operator guide.** Operator questions are answered through concept files' "How you encounter it" sections + reference configs in `agents/examples/`. If consumer demand reveals this is insufficient, a public operator guide lands as a follow-up.
8. **A 10-minute tour or worked-example walkthrough in the human path.** A tour is a worked example, and the worked-example surface is deferred. The narrative concept walk plus the dashboard guide carries the human path for v1.
9. **Public FAQ.** Re-added when there is real material from real adopters' agent-mediated questions.
10. **Versioned docs.** Pre-v1 has no committed compatibility; docs version with `main` only. Versioned docs become relevant when v1 ships.
11. **Migrating, deleting, or rewriting `docs/internal/` itself.** Internal/working surface stays where it is; this spec is additive on the public side.

## 1. Public vs. working docs

The decisive separation:

- **Public**. Self-contained. Cites within itself, into proto files, and into source code only via concept-file frontmatter for the proto-message anchor (see §3). Lint-enforced for vocabulary and structural integrity.
- **Working/internal**. `docs/internal/`, `docs/specs/`, `docs/plans/`, `docs/history/`, `docs/future-work/`, plus the existing case-making narrative surface in `docs/examples/`. None of these are cited by the public surface. None are guaranteed to be maintained going forward. Eventually these move outside the rimsky tree entirely.

Concrete consequence: if the public surface needs a fact that currently lives in an internal doc, that fact is **lifted** (one-time copy at land time, restructured to fit the public-surface shape) into the appropriate public file. After lift, the internal source is not authoritative for the same fact.

`docs/licensing.md` is already-public and stays where it is. No other top-level public files exist today.

## 2. Public-surface directory structure

```
docs/
├── README.md                    # Doc-tree map. Distinguishes public vs internal.
├── licensing.md                 # Already public. Stays.
│
├── concepts/                    # CANONICAL. One file per domain noun.
│   ├── README.md                # Index of concepts with one-line definitions.
│   ├── four-layer-model.md
│   ├── node.md
│   ├── node-state.md
│   ├── cascade.md
│   ├── invalidate.md
│   ├── recalculate.md
│   ├── frame.md
│   ├── frame-resolution.md
│   ├── claim.md
│   ├── claim-handle.md
│   ├── named-lock.md
│   ├── scope.md
│   ├── write-semantics.md
│   ├── holding-subgraph.md
│   ├── inheritance.md
│   ├── template.md
│   ├── instance.md
│   ├── tag.md
│   ├── attributes.md
│   ├── userdata.md
│   ├── claim-producer.md
│   ├── executor.md
│   └── lifecycle-subscriber.md
│
├── protocols/                   # Protocol-implementation guides for external implementers.
│   ├── README.md                # When to read which guide.
│   ├── claim-producer.md        # Lifted + scrubbed from internal claim-producer-author-guide.md.
│   ├── executor.md              # Lifted + scrubbed from internal executor-author-guide.md.
│   └── lifecycle-subscriber.md  # New. Brief; protocol is small.
│
├── agents/                      # Agent-shaped presentation surfaces.
│   ├── llms.txt                 # llmstxt.org-format curated index.
│   ├── llms-full.txt            # Concatenated canonical content for single-pull retrieval.
│   ├── errors/                  # One file per consumer-observable error.
│   │   ├── README.md
│   │   ├── orphaned_claim_lost_race.md
│   │   ├── ... (filled per §8.4 sources)
│   │   └── ...
│   └── examples/                # Complete, copy-pasteable, no-ellipsis examples.
│       ├── README.md
│       ├── minimal-rimsky-yml.md
│       ├── minimal-template-and-instance.md
│       ├── two-node-with-claim.md
│       ├── claude-agent-userdata.md
│       ├── holding-subgraph.md
│       └── rimsky-compose-multi-template.md
│
├── humans/                      # Thin human-shaped presentation.
│   ├── landing.md               # What Rimsky is + "point your agent at the docs" + dashboard pointer.
│   ├── concepts.md              # Narrative concept walk in learning order.
│   └── dashboard.md             # Dashboard UI guide.
│
├── glossary.md                  # GENERATED from concepts/. Hand-edits fail CI.
└── vocabulary.md                # Deprecated terms + layered-sense disambiguation discipline.

# Repo root:
llms.txt                         # Symlink (or build-copy) of docs/agents/llms.txt.
llms-full.txt                    # Symlink (or build-copy) of docs/agents/llms-full.txt.
```

Working surface (unchanged by this spec): `docs/internal/`, `docs/specs/`, `docs/plans/`, `docs/history/`, `docs/future-work/`, `docs/examples/`. This spec does not move, modify, or delete files in those directories beyond the one-time content lifts that source the public surface.

## 3. Per-concept file shape

Every file in `docs/concepts/` follows the same shape. Agents pattern-match on consistent shapes; humans skim by section heading.

```markdown
---
concept: <kebab-case-name>
definition: |
  <1-3 sentences. The same text is repeated under "Definition" — the
  redundancy is intentional for retrieval recall.>
proto_symbol: <FullyQualifiedName in protocols/proto/v1/<file>.proto> | (none)
config_field: <rimsky.yml:<dotted.path>> | (none)
api_surface: <HTTP_VERB /path> | (none)
related: [<concept>, <concept>, ...]
deprecated_terms: [<term>, ...]
layer_senses: (omit if none)
  - layer: <foundation | modeling | protocol | bundled-services>
    sense: <one-sentence presentation in that layer>
---

# <Title>

## Definition

<1-3 sentences. Same as frontmatter.>

## Why it exists

<2-4 paragraphs. The problem this concept solves. What breaks without it.>

## Layer senses

<Only when layer_senses is non-empty in frontmatter. One paragraph per
layer-sense, naming the presentation in that layer and pointing at the
canonical layer's sense.>

## How you encounter it

<What config field, what API endpoint, what dashboard view, what wire
message, what CLI command. For internal-to-rimsky concepts that are not
consumer-observable, explicitly say so:

  > Not directly observable to consumers. Documented here because <X>
  > behavior depends on it.

>

## Consumer-visible guarantees

<Only when relevant. Properties consumers can rely on (e.g. "rimsky
compares scope bytes byte-for-byte; producers must canonicalize scopes
that should conflict"). Do NOT include internal-correctness invariants
(advisory locks, sweep cutoffs, internal state-machine rejections).>

## Common mistakes

<Anti-patterns. Each is one sentence stating the mistake, one sentence
stating the correct behavior. Vocabulary disambiguation lives here:

- **Frame ≠ stack frame, video frame, UI frame.** Rimsky's frame is a
  unit of cascade resolution.
- **Cascade ≠ CSS cascade.** Rimsky's cascade is `invalidate`-based
  state propagation through a node graph.

The disambiguation entries are the highest-leverage part of this
section — they keep agent-explained accounts from drifting into
unrelated semantic neighborhoods.>

## See also

<Bullet list of related concept files (relative links).>
```

Frontmatter field requirements:

- **Required, every concept file**: `concept`, `definition`, `proto_symbol`, `config_field`, `api_surface`, `related`, `deprecated_terms`. The three anchor fields use the literal string `(none)` when not applicable; missing fields fail the frontmatter lint (§7.1).
- **Optional**: `layer_senses`. Omitted entirely (not present as an empty list) when there are no layered senses.

Frontmatter design notes:

- `proto_symbol` references a real proto symbol in `protocols/proto/v1/`. Proto files are part of the public wire contract, so this is a citable anchor.
- `config_field` references a path inside `rimsky.yml` (the operator config) when the concept surfaces there. The reference config in `agents/examples/minimal-rimsky-yml.md` is the source of truth for valid paths.
- `api_surface` references a control-api HTTP route when the concept surfaces there.
- `related` is a (possibly empty) list of related concept names; `deprecated_terms` is a (possibly empty) list of deprecated synonym terms. Both must be present even when empty (`related: []`, `deprecated_terms: []`).
- No `go_type`, no `db_table`, no `invariants` numbers. Those are internal-leaning and would couple the public surface to source layout. Consumer-visible properties go in the prose "Consumer-visible guarantees" section, not in frontmatter.

## 4. Concept set

The canonical concept set, ~23 files. Every file **lands substantively populated as part of this work** (Definition + Why it exists + Layer senses where applicable + How you encounter it + Consumer-visible guarantees where relevant + Common mistakes + See also). No stubs. The per-file content shape is defined in §3; the per-file content itself is produced during implementation, not embedded in this spec.

Meta:
- `four-layer-model.md` — the foundation/modeling/protocol/bundled-services structure that organizes the rest of the vocabulary.

Behavior and propagation:
- `node.md` — unit of work.
- `node-state.md` — `fresh` / `stale` / `running` / `failed`.
- `cascade.md` — invalidate-based state propagation.
- `invalidate.md` — the only graph-level message.
- `recalculate.md` — the per-node action; not a graph-level message.
- `frame.md` — unit of cascade resolution.
- `frame-resolution.md` — how frames terminate.

Coordination primitives:
- `claim.md` — the foundation primitive. (Sub-elements `alias`, `intent`, `address`, `payload` are documented inline within this file rather than spun out — they are properties of a claim, not standalone concepts agents need to ground on independently.)
- `claim-handle.md` — the persistent row asserting `(holder, scope, purpose)`.
- `named-lock.md` — the non-producer counting primitive.
- `scope.md` — both conceptual `(producer, selector)` and the concrete bytes. (Includes `selector` inline.)
- `write-semantics.md` — `sync` / `staged_async` / `blocking_async` / `read_only`. (Includes `WriteSemanticsEnvelope` and `realized_write_semantics` inline.)

Lifetime:
- `holding-subgraph.md` — the set of nodes a held claim spans.
- `inheritance.md` — the DSL mechanism for extending a claim's lifetime.

Modeling layer (control plane):
- `template.md` — content-addressed bundle.
- `instance.md` — running execution of a template.
- `tag.md` — movable alias to a template content hash.
- `attributes.md` — per-node attribute schema and writeback.
- `userdata.md` — opaque-to-rimsky executor data.

Service protocols (one concept file per protocol; the deeper protocol-implementation guides live under `docs/protocols/`):
- `claim-producer.md` — the protocol; layered-sense entry pointing at the bundled-services-layer "store" colloquialism.
- `executor.md` — the protocol.
- `lifecycle-subscriber.md` — the protocol.

Notes on deliberate folds:

- "Store" gets no separate concept file. It is a `layer_senses` entry on `claim-producer.md` (bundled-services-layer colloquialism for data-backed claim producers).
- "Selector," "address," "payload," "intent," "alias" do not get separate files; they are documented inline on `claim.md` and/or `scope.md`.
- "Value-pass" / "claim-pass" are documented inline on `inheritance.md` (or `claim.md`) as propagation modes; not separate files.
- "Compose project" / "compose manifest" / "context" are operational vocabulary surfaced in concept-file "How you encounter it" sections (and in `agents/examples/rimsky-compose-multi-template.md`), not standalone concept files.
- "Schedule" (cron schedules) is operational vocabulary that surfaces in the dashboard guide and in `agents/examples/`; not a standalone concept file in v1. (May earn a file later.)

If the planning phase surfaces a needed concept that's not on this list, it gets added then.

## 5. Glossary generation

`docs/glossary.md` is generated from `docs/concepts/*.md`. The generator (`make docs-glossary`):

1. Reads every file in `docs/concepts/`.
2. Extracts frontmatter `concept`, `definition`, `deprecated_terms`, and `layer_senses`.
3. Emits a markdown table sorted alphabetically by `concept`, with one row per concept showing the concept name, its definition, and (if present) its layered senses inline.
4. Emits a "Deprecated terms" section at the bottom by aggregating `deprecated_terms` arrays across all concept files; each entry points at the canonical concept.
5. Stamps a top-of-file warning: `<!-- AUTOGENERATED from docs/concepts/. Do not edit by hand. Run \`make docs-glossary\`. -->`.

A CI check (§7.2) verifies that the committed `docs/glossary.md` matches the generator's output.

The generator implementation language: Go (in `cmd/rimsky-docs-glossary/`, run via `make docs-glossary`). Rationale: stays inside the existing toolchain, no new language dependency, can also be invoked as a `go run` step from CI without external setup.

## 6. The vocabulary discipline

Three rules.

### 6.1 One concept, one name

Every concept has exactly one canonical name in the public surface. Synonyms are forbidden. Where historical synonyms exist (`template_id`, `consumer_key`, "substrate", `region`, the `Store` protocol-level interface name), they are listed in `docs/vocabulary.md` as deprecated, with the current term and the rationale for the change.

### 6.2 One name, one concept (with layered-senses disambiguation)

Some Rimsky terms have layered senses — same word, slightly different presentation per layer:

- "Store" — at the protocol layer, this term is *not used* (use "claim producer"). At the bundled-services layer, "store" is the colloquial name for a data-backed claim producer (filesystem store, postgres store, stub store).
- "Frame" — appears at the foundation layer (frame-id correlation) and the modeling layer (the unit of cascade resolution, with layered presentation through scheduling).
- "Claim" — foundation-layer primitive vs. modeling-layer presentation in templates.

These are handled by the `layer_senses` frontmatter entry and the "Layer senses" prose section on the relevant concept file. The vocabulary lint (§7.3) does *not* try to enforce layered terms — they are context-dependent and not greppable. Disambiguation happens in the prose, not in the lint.

Where a Rimsky term overlaps with general programming vocabulary (frame, cascade, region, store, claim), the concept file's "Common mistakes" section names the disambiguation explicitly. This is the highest-leverage piece of the agent path: an agent that sees "claim" without disambiguation has been trained on millions of JWT and insurance documents and will pattern-match wrong unless the doc heads it off.

### 6.3 `docs/vocabulary.md`

A standalone public file that:

1. Names the deprecated-terms list with current-term replacements (sourced from `deprecated_terms` frontmatter arrays across `concepts/`, plus a curated layered-sense section).
2. Explains the layered-sense discipline and points readers at the four-layer-model concept.
3. Names the canonical `proto_symbol` / `config_field` / `api_surface` shape that anchors each concept.

`docs/vocabulary.md` is hand-curated (not generated); the deprecated-terms section is mechanically cross-checked against concept-file frontmatter by the public-anchor lint (§7.5).

## 7. CI lints

Six lints, all running on every PR. All scoped to the public surface; none reach into `docs/internal/`, `docs/specs/`, `docs/plans/`, `docs/history/`, `docs/future-work/`, or `docs/examples/`.

### 7.1 Frontmatter validation

Every file in `docs/concepts/` parses as valid YAML frontmatter and has all required fields: `concept`, `definition`, `proto_symbol`, `config_field`, `api_surface`, `related`, `deprecated_terms`. Optional: `layer_senses`. Missing required fields fail.

### 7.2 Glossary parity

`docs/glossary.md` byte-equals the output of `make docs-glossary`. Hand-edits fail.

### 7.3 Vocabulary lint

A grep-based check scans the public surface (`docs/concepts/`, `docs/protocols/`, `docs/humans/`, `docs/agents/`, `docs/glossary.md`, `docs/vocabulary.md`, `docs/licensing.md`, `docs/README.md`, repo-root `llms.txt` / `llms-full.txt`, `README.md`) for context-independent forbidden terms.

Initial seed list (the user-confirmed terms; all unambiguously context-independent):
- `template_id` → `template_hash`
- `consumer_key` → `instance_key`
- `substrate` → "store" (bundled-services-layer) or "claim producer" (protocol-layer) per context

Additional forbidden terms — including but not limited to obsolete table names (`rimsky_dispatch`, `rimsky_lock_holders`, `rimsky_store_lifecycle`) and the protocol-layer interface name `Store` — are added during the planning phase as the concept-file fill surfaces them. Each addition specifies its concrete grep pattern (regex, anchoring, code-fence handling) so layered terms like "store" are not accidentally captured.

Layer-dependent terms (e.g. "store" used colloquially at the bundled-services layer) are *not* in the forbidden list. Disambiguation happens in concept-file prose, not in the lint.

The lint outputs the offending line, the deprecated term, and the current term, and points at `docs/vocabulary.md`. The forbidden-term list lives in a config file (`docs/.vocabulary-lint.yml`) read by the lint. Each entry has shape `{ term: <regex>, replacement: <text>, scope: [<paths or globs>] }`.

### 7.4 Citation drift

Concept files, protocol guides, and human-path docs may cite a concept's canonical definition. The convention is concrete:

1. The citation site begins with an HTML comment of the form `<!-- @source: concepts/<concept>.md -->`.
2. The comment is **immediately followed by a markdown blockquote** (one or more lines starting with `> `). The blockquote is the cited definition copy.
3. The blockquote's content (joined with single spaces, whitespace-collapsed, trailing/leading whitespace stripped) MUST byte-equal the cited concept file's `definition` frontmatter (after the same normalization).
4. Citations may target only files in `docs/concepts/`. Citation comments whose path resolves outside `docs/concepts/` fail the lint (this prevents accidental citations into the internal-working surface).

Mismatches between the blockquote content and the canonical `definition` fail the lint. Adding new prose without the citation comment is fine; the lint only fires when a comment is present and its blockquote does not match. Removing a citation block is fine. The matching algorithm is the simplest possible: extract blockquote, normalize, compare.

Concept files may cite each other (a foundation-layer concept's "Layer senses" entry can cite the modeling-layer concept it points at, for example). Protocol guides cite concept files where they carry definition-shaped text. The human-path concept walk cites every concept file it walks through.

### 7.5 Public-anchor validity

For every concept file, the lint verifies:

- `proto_symbol` is either `(none)` or refers to a real symbol (message, enum, or service) in `protocols/proto/v1/*.proto`. The check parses the .proto files and looks up the symbol name.
- `config_field` is either `(none)` or follows the documented `rimsky.yml:<dotted.path>` shape. (Validating that the path actually exists in a real `rimsky.yml` schema is a follow-up; v1 of this lint just enforces shape.)
- `api_surface` is either `(none)` or follows the documented `<HTTP_VERB> <path>` shape. (Same: shape-only in v1.)

Stale `proto_symbol` references fail. Internal anchors (Go types, SQL tables) are not part of the frontmatter and so cannot be referenced.

### 7.6 `llms.txt` validity

Two checks:

- `docs/agents/llms.txt` parses as the llmstxt.org format.
- Every URL in `llms.txt` resolves to a real file in the public surface.

Same for `docs/agents/llms-full.txt` (parses as plain markdown; every linked URL resolves).

The repo-root `llms.txt` and `llms-full.txt` are symlinks (or build-copies) of the `docs/agents/` versions, so the validity check at `docs/agents/` covers both.

## 8. The agent path

Five components.

### 8.1 `docs/agents/llms.txt`

A curated llmstxt.org-format index. Format:

```
# Rimsky

> Project-agnostic reactive node-graph orchestration platform organized as four layers (foundation, modeling, service protocols, bundled services). Coding agents are the primary documentation consumer.

## Concepts

- [Four-layer model](concepts/four-layer-model.md): The vocabulary structure: foundation, modeling, service protocols, bundled services.
- [Node](concepts/node.md): A unit of work with named inputs (claims), an executor, and a state machine of fresh / stale / running / failed.
- [Frame](concepts/frame.md): The unit of cascade resolution; every worker request carries a frame_id.
- [Claim](concepts/claim.md): A foundation-layer primitive asserting (producer, scope, intent).
- [Claim handle](concepts/claim-handle.md): A persistent row in rimsky_claim_handle (the source of authority for lock state).
- ... (one bullet per concept file, with the one-sentence frontmatter definition)

## Protocols

- [ClaimProducer](protocols/claim-producer.md): How to implement a claim producer (Open / Commit / Abandon / Release / Capabilities).
- [Executor](protocols/executor.md): How to implement an executor (Execute / StreamTrace / GetTrace / GetCapabilities).
- [LifecycleSubscriber](protocols/lifecycle-subscriber.md): How to implement a lifecycle subscriber (six template/instance state-transition hooks).

## Examples

- [Minimal rimsky.yml](agents/examples/minimal-rimsky-yml.md)
- [Minimal template and instance](agents/examples/minimal-template-and-instance.md)
- [Two-node template with a claim dependency](agents/examples/two-node-with-claim.md)
- [Claude-agent template demonstrating userdata substitution](agents/examples/claude-agent-userdata.md)
- [Holding-subgraph template demonstrating held-claim resolution](agents/examples/holding-subgraph.md)
- [rimsky-compose multi-template project](agents/examples/rimsky-compose-multi-template.md)

## Errors

- [Error catalog](agents/errors/README.md)

## Optional

- [Glossary](glossary.md)
- [Vocabulary discipline and deprecated terms](vocabulary.md)
- [Dashboard UI guide](humans/dashboard.md)
```

The `Optional` section is per llmstxt.org convention: agents that need only minimal context skip it; agents doing deeper analysis include it.

URLs are relative. The repo-root `/llms.txt` symlink resolves the relative paths against the docs root; once the surface lands at a hosted URL, those resolve to absolute URLs at the host.

### 8.2 `docs/agents/llms-full.txt`

A single concatenated markdown file with every concept's canonical content (frontmatter stripped, body included), plus the protocol guides, in dependency order. Generated from `docs/concepts/` and `docs/protocols/`. For agents whose tooling can fetch one file but not crawl.

Generated by `make docs-llms-full` (Go binary in `cmd/rimsky-docs-llms-full/`). CI verifies the committed file byte-equals the generator output, same as glossary parity.

### 8.3 `docs/concepts/`

Per §3 and §4. The canonical surface.

### 8.4 `docs/agents/errors/`

One file per consumer-observable error. Sources for the initial catalog:

- Proto-level error responses defined in `protocols/proto/v1/*.proto` (consumer-visible by virtue of being on the wire).
- Operator-facing config-validation errors (rimsky.yml load failures; shapes errors users will encounter at deployment).
- CLI error messages emitted by `rimsky-cli`.
- Runtime errors that propagate to peer services through the wire protocol (`orphaned_claim_lost_race` is the canonical example — the executor sees this path).

Per-file shape:

```markdown
---
error: <error-code-or-string>
surfaced_to: <executor | claim-producer | lifecycle-subscriber | operator | cli-user>
---

# <Error string or symbolic name>

## What it means

<Plain-English explanation. Two sentences max.>

## When it happens

<Concrete trigger conditions.>

## What to do

<Actionable response. Includes follow-up question shapes the agent
might ask the user if the response is ambiguous.>

## See also

<Links to relevant concept files.>
```

Internal-correctness errors (state-machine rejections like `dispatch_claimed running → running rejected`, advisory-lock failures, sweep-internal errors) are not consumer-observable and do not get error files. They are catalogued only in source code and internal docs.

### 8.5 `docs/agents/examples/`

Complete, copy-pasteable, no-ellipsis examples. The discipline: an agent should be able to copy any file's content and run it without filling in blanks. No `...`, no "you'd also need to configure your store," no implicit env vars. Every required field present, every command runnable. Where a follow-up command is needed, the example states it verbatim.

Initial examples (each as one markdown file with embedded `yaml` / `bash` / `json` blocks):

- `minimal-rimsky-yml.md` — minimal `rimsky.yml` (lifted from `deploy/rimsky.yml`, scrubbed to the smallest runnable shape).
- `minimal-template-and-instance.md` — register a one-node template, create an instance, observe completion.
- `two-node-with-claim.md` — two-node template demonstrating a claim dependency between nodes.
- `claude-agent-userdata.md` — claude-agent executor template demonstrating `userdata` substitution into a node.
- `holding-subgraph.md` — template with `inherits:` declarations demonstrating held-claim resolution.
- `rimsky-compose-multi-template.md` — `rimsky-compose.yml` declaring multiple templates and instances.

Each example file ends with a verification block: the exact commands to run, and the expected output. Where the example requires the bundled docker-compose stack to be up, the example states `docker compose -f deploy/docker-compose.yml up -d` as a precondition.

## 9. The protocol-implementation guides

Three files in `docs/protocols/`. These cover the gap between "concepts grounded" and "I can implement the protocol in my language of choice."

### 9.1 `docs/protocols/claim-producer.md`

Lifted from `docs/internal/claim-producer-author-guide.md`, scrubbed to:

- Cite concept files for definitions (claim, claim-handle, scope, write-semantics, etc.) instead of restating them inline.
- Reference proto file paths in `protocols/proto/v1/claim_producer.proto`.
- Drop references to internal docs, source-code internals (Go internals beyond what consumers see), and SQL table names.
- Keep the substantive guidance: capabilities handshake, idempotency requirements, byte-equal-scope uniformity invariant, conformance-binary usage.

### 9.2 `docs/protocols/executor.md`

Lifted from `docs/internal/executor-author-guide.md`. Same scrubbing discipline as 9.1. Covers `Execute`, `StreamTrace`, `GetTrace`, `GetCapabilities`, the async-callback path, the userdata-is-opaque guarantee, conformance-binary usage.

### 9.3 `docs/protocols/lifecycle-subscriber.md`

New (no internal predecessor; the lifecycle-subscriber protocol is younger than the author-guide framework). Brief — six method signatures, semantics of each event, idempotency requirements (rimsky tracks idempotency via `(peer-name, event-type, object-id)`; replays are no-ops, peers should still write idempotent handlers). References the proto file at `protocols/proto/v1/lifecycle.proto`.

All three guides cite concept files using the `<!-- @source: concepts/<concept>.md -->` convention where they carry definition-shaped text, so the citation-drift lint covers them.

## 10. The human path

Three files. Thin by design.

### 10.1 `docs/humans/landing.md`

Three blocks:

1. **What it is** — two sentences. Project-agnostic reactive node-graph orchestration platform, four layers, designed for agent-mediated adoption.
2. **Point your favorite coding agent at this surface** — explicit framing. The recommended consumption path is via an agent. Names the entry points: `concepts/four-layer-model.md` (vocabulary structure), `agents/llms.txt` (curated index for agents that follow the convention), `protocols/` (for implementers), `humans/concepts.md` (for narrative concept walk).
3. **Dashboard pointer** — two sentences, links to `humans/dashboard.md`.

No diagrams in v1 (deferred until the SSG decision lands and we can render SVG sensibly across surfaces). No analogies, no positioning claims, no "here's why this is better than X."

### 10.2 `docs/humans/concepts.md`

Narrative concept walk in *learning order*, not alphabetical. Each section names the canonical concept once, links to its concept file, and walks through the concept narratively. Citation comments make every definition-shaped sentence trackable.

Order:

1. The four-layer model (the meta-frame)
2. Nodes and node states (the unit of work)
3. Templates and instances (the declarative artifacts)
4. Frames and frame resolution (cascade resolution units)
5. Cascades (`invalidate` propagation)
6. Claims, claim handles, scopes, named locks (coordination)
7. Write semantics (concurrency model)
8. Holding subgraphs and inheritance (claim-lifetime extension)
9. Service protocols (the three external-implementation surfaces)
10. Attributes and userdata (substitution and writeback)

By the end, the reader has a complete mental model. Bare references throughout link to concept files for depth; definition-shaped text uses `<!-- @source: ... -->` citations.

### 10.3 `docs/humans/dashboard.md`

A guide to using the bundled dashboard (the `dashboards/` collection). Sources:

- The dashboard spec at `docs/specs/2026-05-02-dashboard-and-observability-design.md` is the working internal source — content is *lifted* from there into `humans/dashboard.md`, restructured for end-user usage rather than design rationale.
- The actual dashboard implementation under `dashboards/` (UI screens, view structure).

The guide covers: how to launch the dashboard (compose profile, Docker image, k8s service), the three observability protocols it composes (each cited from the corresponding concept file), the screens (instance list, node graph view, frame timeline, claim-handle inspector), and how to read each. No write actions in v1 (consistent with the dashboard spec's read-only stance).

The guide does *not* cover deployment topology, env var meanings, postgres setup, or migration. Those are operator concerns answered by concept files' "How you encounter it" sections + reference configs in `agents/examples/`.

## 11. Migration plan

The migration is **content-lift-then-stabilize**. There is no big-bang: existing internal docs stay where they are; the public surface lands additively; lint runs from the moment the public surface exists.

Order (intended for the planning phase to expand into discrete tasks):

1. **Land directory skeleton + frontmatter validator + glossary generator.** Create `docs/concepts/`, `docs/protocols/`, `docs/agents/`, `docs/agents/errors/`, `docs/agents/examples/`, `docs/humans/`. Land the `make docs-glossary` and `make docs-llms-full` Go binaries. CI runs frontmatter validation and glossary parity from this step forward; concept files are stub frontmatter at this point.
2. **Land `docs/vocabulary.md`** with the deprecated-terms seed list. CI starts running the vocabulary lint against the (still-mostly-empty) public surface.
3. **Fill concept files substantively, in dependency order.** Concrete order: four-layer-model → node, node-state → cascade, invalidate, recalculate → frame, frame-resolution → claim, claim-handle, scope, named-lock, write-semantics → holding-subgraph, inheritance → template, instance, tag → attributes, userdata → claim-producer, executor, lifecycle-subscriber. Each concept file lifts content from `docs/internal/glossary.md`, the contract docs in `docs/specs/2026-05-04-*-contract.md`, `docs/internal/node-graph-design.md`, and the blessed-invariants list in `CLAUDE.md` as appropriate. After lift, the lifted-from internal source is no longer authoritative for the lifted content; the concept file is.
4. **Land `docs/protocols/claim-producer.md`, `docs/protocols/executor.md`, `docs/protocols/lifecycle-subscriber.md`.** Lift from the internal author guides; scrub internal citations; route definition-carrying text through `<!-- @source: concepts/... -->` citations.
5. **Land `docs/agents/errors/`.** Curate from proto error enums, operator-facing config-validation errors, CLI errors, and the consumer-observable runtime errors named in §8.4. Initial seed: 10–15 errors. Easy to grow.
6. **Land `docs/agents/examples/`.** Lift from `deploy/rimsky.yml` and the existing smoke fixtures; flesh out each as a complete copy-pasteable file with embedded verification commands.
7. **Land `docs/agents/llms.txt`** populated against the now-substantive concept files, protocol guides, errors, and examples. Land `docs/agents/llms-full.txt` generator (`make docs-llms-full`).
8. **Land `docs/humans/landing.md`, `concepts.md`, `dashboard.md`.** Concepts walk uses concept-file citations throughout.
9. **Land citation-drift, public-anchor-validity, and `llms.txt`-validity lints** once the surface they lint against is populated.
10. **Land repo-root `llms.txt` and `llms-full.txt` symlinks/build-copies.**

Steps 1–2 are foundational. Step 3 is the bulk of the writing. Steps 4–8 each unblock a different surface. Steps 9–10 finalize.

The plan does not commit to a per-step PR structure; the plan-writing phase decides PR boundaries.

## 12. What this spec doesn't change

Explicitly:

- `docs/internal/` — untouched. Content is read-only inputs to the lift; no edits to internal files in this spec's scope.
- `docs/specs/`, `docs/plans/`, `docs/history/`, `docs/future-work/` — untouched. The contract docs at `docs/specs/2026-05-04-*-contract.md` are inputs to the concept-file lift; they are not edited or moved by this spec.
- `docs/examples/` — untouched. The case-making narrative surface is deferred.
- `CLAUDE.md` — only the `docs/` references in the "Where to look first" section may need a light update to point at the public surface where appropriate. Otherwise unchanged.
- Source code — no source changes other than the new `cmd/rimsky-docs-glossary/` and `cmd/rimsky-docs-llms-full/` binaries.

## 13. Tradeoffs and known concerns

- **Public surface and internal surface diverge over time.** This is not a bug — it is the design. Internal docs are unmaintained going forward; public docs are the canonical source for everything user-facing. The risk is that internal docs become misleading for contributors who don't know to check the public surface first. Mitigation: `docs/internal/README.md` (or equivalent) gets a top-of-tree note: *"This directory is unmaintained. For canonical user-facing material, see the public surface above this directory."*
- **Operator guidance is thin in v1.** Per §10.3, the dashboard guide explicitly punts on deployment topology / env-var meanings / postgres setup / migration. Agents synthesizing operator answers from concept files + reference configs may produce thin guidance for the first wave of consumers. Mitigation: the gap is documented; if consumer demand reveals it, a follow-up adds `docs/operator-guide.md`.
- **No worked-example walkthrough.** A 10-minute tour with a worked example is a load-bearing onboarding shape for many projects. We are betting that the agent-mediated path makes this redundant: an agent can synthesize a tour from concept files + reference examples for the human it's onboarding. If the bet is wrong, a tour lands as a follow-up; the structure here doesn't preclude it.
- **No "why Rimsky vs. X" positioning.** Same bet — too soon to make the claim. The case-making example surface (`docs/examples/`) is where this lands eventually.
- **Citation-drift lint is the load-bearing structural enforcement.** If the lint doesn't actually run on every PR (or is skippable), the discipline fails and the public surface drifts back into prose-with-restated-definitions. Mitigation: lint is mandatory CI; PRs cannot merge with it failing.
- **The vocabulary lint is grep-based.** False positives will happen (e.g. `template_id` appearing inside a code-fenced block describing what *not* to call something). Mitigation: the lint config supports per-file ignore comments (`<!-- vocabulary-lint-ignore: template_id -->`). Used sparingly and reviewed.
- **Concept-file proliferation.** ~23 files is a lot to maintain consistently. The per-file shape is rigid (§3), the glossary is generated (§5), and the lint catches drift (§7). Adding the 24th concept file is the same cost as the 1st.
- **Public-anchor-validity lint v1 is shape-only for `config_field` and `api_surface`.** Validating that a `config_field` path actually exists in the rimsky.yml schema, or that an `api_surface` route is actually registered, is a follow-up. v1 catches typos and stale references for proto messages (which is where most drift would happen).
- **Lift-from-internal is one-time at land.** If the internal source changes after the lift, the public surface does not auto-update. This is the design (public surface is authoritative once landed). The risk is contributors editing internal docs assuming the change propagates. Mitigation: internal-tree top-of-tree note + the unmaintained signal.
- **Repo-root symlinks may not work cleanly on Windows.** `llms.txt` and `llms-full.txt` at the repo root use symlinks to `docs/agents/`. Symlinks need git config (`core.symlinks=true`) which is not the Windows default. Mitigation: use a build-step copy (Make target) instead of symlinks; CI verifies the copies are up-to-date.
- **`docs/agents/llms-full.txt` will be large.** All concept content + protocol guides concatenated may exceed agent context windows for some retrievers. Mitigation: the file is documented as "for agents with one-shot fetch but limited context"; agents that can crawl are pointed at `llms.txt` first. The `Optional` section in `llms.txt` lets retrievers skip non-essentials.

## 14. Out of scope / deferred

- Static site generator and rendered website surface.
- Hosted RAG widget on the docs site.
- `docs/humans/why-rimsky.md` and any positioning vs. Airflow / Argo / Temporal.
- `docs/humans/tour.md` — 10-minute worked-example walkthrough.
- `docs/humans/faq.md` — re-added when there's real material.
- A 1000-line public operator guide.
- Source-derived concept files (generation from `@blessed-invariant` / `@agent-contract` annotations).
- Translations.
- Versioned docs.
- Per-page search infrastructure (Algolia, Pagefind, etc.).
- Auto-generated proto / Go / SQL reference (`protoc-gen-doc` etc.).
- Docs analytics.
- Editing or migrating `docs/internal/`, `docs/specs/`, `docs/plans/`, `docs/history/`, `docs/future-work/`, or `docs/examples/`.
- Marketing pages / a top-of-funnel landing on `fallguyconsulting.com`.
- The package-manager work in `docs/future-work/2026-04-26-package-manager.md`.

## 15. Summary of decisions

| # | Decision | Rationale |
|---|---|---|
| 1 | Public surface is fully self-contained; never cites `docs/internal/`, `docs/specs/`, `docs/plans/`, `docs/history/`, `docs/future-work/`, or source-code internals | The internal/working surface is unmaintained going forward and eventually moves outside the rimsky tree |
| 2 | Single source of truth: `docs/concepts/<concept>.md` per canonical noun | Mirrors the `@source:` discipline already used in code; prevents two-audience drift |
| 3 | Per-concept file shape: frontmatter + Definition / Why it exists / Layer senses (when applicable) / How you encounter it / Consumer-visible guarantees / Common mistakes / See also | Consistent shape lets agents pattern-match; humans skim by heading |
| 4 | Concept anchors are proto-message + config-field + api-surface; no Go-type or SQL-table anchors | Public surface is for consumers, not contributors |
| 5 | Layered terms (e.g. "store") get a `layer_senses` frontmatter entry on the canonical concept file; the four-layer-model is its own concept file | Disambiguates the same word across layers without fanning into multiple files |
| 6 | All ~23 concept files land substantively populated as part of this work (no stubs); the per-file content shape is in §3, the per-file content itself is produced during implementation | Scope C requires concept files to carry the human-path narrative; partial fill ships hollow |
| 7 | `docs/glossary.md` generated from concept frontmatter; hand-edits fail CI | Hand-maintained glossaries are where drift starts |
| 8 | `docs/vocabulary.md` is hand-curated; the deprecated-terms seed list at land is `template_id`, `consumer_key`, `substrate`; additional forbidden terms (including obsolete table names and protocol-layer `Store`) are added during planning with concrete grep patterns | These are context-independent forbidden terms; layered terms get prose disambiguation, not a lint |
| 9 | Six CI lints: frontmatter validation, glossary parity, vocabulary lint (public surface only), citation drift (within public surface only), public-anchor validity (proto-message-strict, config-field/api-surface shape-only), `llms.txt` validity | Discipline is mechanical, not aspirational |
| 10 | Agent path: `llms.txt` (curated index), `llms-full.txt` (generated concatenation), `agents/errors/` (consumer-observable subset only), `agents/examples/` (complete copy-pasteable, sourced from `deploy/` and smoke tests) | llmstxt.org convention + atomic surfaces agents grep when grounding |
| 11 | Protocol-implementation guides at `docs/protocols/{claim-producer,executor,lifecycle-subscriber}.md` lifted from internal author guides | Closes the "external implementer can't read source" gap |
| 12 | Human path: `humans/landing.md`, `humans/concepts.md`, `humans/dashboard.md` only — no tour, no FAQ, no why-rimsky | Thin by design; case-making and tours are deferred to the example surface |
| 13 | No public operator guide in v1; operator answers come from concept files' "How you encounter it" + reference configs in `agents/examples/` | Bet that agent synthesis is sufficient; revisit if consumers complain |
| 14 | Static site generator deferred; public surface renders via GitHub markdown for now | Decide when the doc structure has settled enough that the choice is mechanical |
| 15 | Package-manager work in `docs/future-work/2026-04-26-package-manager.md` is out of scope here | Separate effort, not gated on this work |
