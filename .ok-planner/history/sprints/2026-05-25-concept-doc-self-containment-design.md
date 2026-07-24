# Concept-doc self-containment + annotation backfill

**Spec:** 2026-05-25-concept-doc-self-containment
**Status:** draft

## Problem

The design docs under `.ok-planner/design/concepts/` are meant to be the
project's canonical noun catalog — a source of truth with the same weight as
code, where **code references the design**, not the other way around. In
practice the concept bodies have accumulated ~313 prefixed codebase citations
(`code:`, `pkg:`, `file:`, `table:`, `col:`, `proto:`, `route:`, `cfg:`,
`env:`, `cmd:`); ~222 bare in-prose file paths; **~190 bare `rimsky_<table>` /
`rimsky_<table>.<col>` identifier references written in prose without any
prefix** (the dominant form — e.g. "the rows in `rimsky_claim_handles`", "a row
in `rimsky_frames`"); and dozens of "Owns / Does NOT own" sections that name
code paths. 55 of 70 live concept files carry a prefixed citation; once the
bare-path, bare-identifier, "Owns / Does NOT own", and external-doc sweeps are
included the compliance check (see Verification) flags **effectively all 70
live files**. The number that matters for scoping is "whatever the check
flags," not the 55.

These citations **rot**. The 2026-05-24 repo reorganization (`spec:2026-05-24-repo-reorganization-design`)
moved whole directories to sibling repos and renamed packages; concept docs
that had cited the moved paths silently went stale, and a targeted audit found
the drift only because someone went looking. Every refactor that moves a file
re-introduces this class of staleness. A concept's definition should not break
when a file moves.

## Goal

Make every live concept body **self-contained** under the B2 rule (below):
the only citations a concept body may contain are ones that survive the
codebase moving. Strip every codebase-surface citation; preserve the
conceptual meaning. Then, so the design→code mapping we remove from the docs
is not lost but inverted into the durable direction, backfill `@concept:`
annotations at the in-repo sites that lost a citation, and clean up dangling
annotations.

This is a self-containment + durability pass, **not** a rewrite of what the
concepts mean. The definitions were written against real code and are presumed
accurate; the work strips the rot and keeps the meaning.

## The B2 rule (concept-body citation policy)

A concept body — every section, **including Notes** — may cite only
references that are stable across a codebase refactor:

**Permitted in concept body:**
- Other concept slugs (`concept:<slug>`, `see also: <slug>`).
- Tension slugs (`tension:<slug>`).
- Annotation tags the codebase uses (`@blessed-invariant`, `@agent-contract`).
  These are stable identifiers, not locations — the code carries the tag and
  `grep` finds the site. This repo uses two coexisting `@blessed-invariant`
  forms, both grep-stable and both permitted: numeric/alphanumeric IDs
  (`@blessed-invariant 4`, `@blessed-invariant 9a`) and a colon-prefixed named
  form (`@blessed-invariant: <name>`, e.g. `@blessed-invariant: ParkReason`).
  Cite whichever form the code carries at that site.
- Spec slugs in dated Notes entries (`spec:YYYY-MM-DD-<topic>`).
- Dates.

**Banned in concept body (every codebase-surface citation):**
- File or directory paths in any form — `code:foo.go::Sym`, `pkg:github.com/...`,
  `file:path`, bare `dir/sub/file.go`, bare `dir/sub/`.
- Persistence / wire / API / config surface citations — `table:`, `col:`,
  `proto:`, `route:`, `cfg:`, `env:`, `cmd:`, **and their bare unprefixed
  forms**: a table/column/identifier name written in prose
  (`rimsky_claim_handles`, `rimsky_node_runs.run_scope_id`) or a bare route
  literal (`/instances/{id}/messages`, `/events`) is the same citation as
  `table:`/`col:`/`route:`, just without the prefix, and is banned the same
  way. These are durable contracts, but under B2 the concept names the role
  abstractly and the *code* carries the concept→surface link via `@concept:`
  annotations.
- References to external documentation — `docs/...`, READMEs, CHANGELOG,
  sibling-repo paths, URLs.
- Quoted code, quoted lint-config allowlists, quoted external prose.
- "Owns / Does NOT own" sections that name code paths. (Lines that name only
  neighbor concept slugs are fine and stay.)

This is the canonical statement for this spec. It matches the "Concept
self-containment rule" in the ok-planner skills, narrowed to B2 (all
codebase-surface citations banned, not only `code:`/`pkg:`/`file:`).

## Scope

**In scope:**
- Every live `.ok-planner/design/concepts/<slug>.md` body + Notes (codebase
  citations stripped + dangling concept-slug cross-refs repaired).
- Every live `.ok-planner/design/tensions/<slug>.md` — `## Resolution candidates`
  only (light touch; see Workstream 1).
- `@concept:` annotation backfill + stale-annotation cleanup in rimsky source
  (Workstream 2).
- `.ok-planner/design/concepts.md` regenerated after concept bodies are clean.

**Out of scope:**
- `.ok-planner/design/_discover/`, `concepts/_retired/`, `tensions/_resolved/`
  (and `tensions/_rejected/` if it exists), `review-notes*.md` — scaffolding /
  terminal-state / workflow scratch.
- Any change to what a concept *means* (Definition / Purpose / Boundaries /
  Invariants stay semantically identical; only their citation form changes).
- The conversion is surgical: no holistic re-authoring.

## Workstream 1 — concept-body + tension self-containment

### Conversion discipline (per citation kind)

Surgical: remove the offending citation and minimally repair the surrounding
sentence so it stays accurate and self-contained. Preserve conceptual meaning;
losing implementation specificity (which exact table / proto / route) is the
accepted B2 trade.

- `code:path::Sym` **enforcing an invariant** → cite the `@blessed-invariant`
  tag instead, in whichever form the code carries at that site (numeric ID like
  `9a` or named like `: ParkReason`). **Not** enforcing an invariant → state the
  behavior as a property of the concept in prose; drop the path.
- `pkg:...` → describe the thing by role in prose ("a bundled reference
  implementation", "an out-of-process service"); if it names a neighbor
  concept's territory, cite the concept slug; drop the path.
- `file:path` / bare path → prose by role; drop.
- `table:rimsky_X` **or bare `rimsky_X` in prose** → "a persisted …
  ledger/record/row"; drop the name. The bare unprefixed form is the most
  common and must be caught — search for `rimsky_` tokens, not just `table:`.
- `col:rimsky_X.y` **or bare `rimsky_X.y`** → describe the field's role; drop
  the name.
- `proto:X::Msg` → describe the wire message / RPC by role; drop.
- `route:METHOD /p` **or bare route literal `/p` in prose** (`/events`,
  `/instances/{id}/messages`) → describe the endpoint by role ("the operator
  message-emit endpoint"); drop the literal.
- `cfg:key` / `env:VAR` → describe the setting by role ("a deployment-level
  cap"); drop.
- `cmd:make X` → describe the command's purpose in prose; drop.
- **"Owns / Does NOT own"** lines naming paths → restate the boundary naming
  neighbor concepts by slug, or in prose; drop paths. Slug-only lines stay
  untouched.
- **`## Annotation sites` sections** (present in ~20 files, almost entirely
  `code:` citation bullets) → delete the section entirely. It is the
  design→code map, now carried in the durable direction by the in-code
  `@concept:` annotations (Workstream 2). Fold any non-citation prose worth
  keeping into Boundaries/Purpose before deleting.
- **Notes** path citations → keep the date and the `spec:` slug; replace the
  path with a path-free prose description of what changed.

**Hard rule for the implementer: never fabricate** an annotation tag or a
concept slug. If no stable token exists for what a citation pointed at, render
it in prose. Do not invent a `@blessed-invariant` ID or name; only cite tags
that already exist in the code (in either the numeric or named form).

### Affected files (authoritative set = the compliance check)

The authoritative set is "every live concept file the compliance check flags"
(see Verification) — **not** a fixed count. The prefixed-citation sweep alone
flags 55 files; the bare-path sweep adds ~10 more (≈65); and once the
"Owns / Does NOT own" and external-doc sweeps are included the set approaches
all 70 live files. Do not mentally cap scope at 55. The implementer runs the
full check, gets the list, and converts each flagged file. No file is exempt;
a file with one citation gets the same treatment as `sdk.md` (17 citations).

### Tension treatment (light touch)

For each live tension: strip file paths, symbol citations, and external-doc
references from `## Resolution candidates` only — restate each resolution
shape at the concept level (which concept's Definition / Boundaries /
Invariants would change, what property the code would hold). `## What is
muddy` and `## Evidence` are point-in-time snapshots and **keep** their code
citations untouched.

This subsumes the prior audit's §3 finding (11 tensions whose resolution
candidates pointed at `docs/concepts/...` paths now in a sibling repo).

### Dangling concept-slug cross-refs (cross-ref hygiene)

While converting each concept file, also repair any **dangling
design-internal cross-ref**: a slug in a `see also:` / `Adjacent:` /
`concept:<slug>` position that does not resolve to a live concept file. These
are the inverse of the codebase-citation problem — they point *inside* the
design docs, but at a concept that was renamed or retired. The known dangling
slugs and their resolution:

- `scope` → repoint to `claim-scope` (renamed 2026-05-22).
- `rimsky-cli` → repoint to `rimsky` (it is an alias of that concept).
- `node-state` → repoint to `node-run` (state lives on the node-run now).
- `schedule` → repoint to `sensor` (cron is a sensor kind).
- `on-event-handler` → repoint to `node-subscription` (the `on_event:` map was
  replaced by `subscribes:`).
- `last-outcome` → repoint to `signal` (the retirement successor).
- `quality-rule` → no live successor concept; reword the reference to prose
  (the verifier-executor pattern) and drop the slug.
- `held-claim` → confirm against `claim-handle` / `claim-lifetime` and repoint
  to whichever the context means; drop if neither fits.

Discipline mirrors the stale-annotation cleanup: **confirm the successor by
reading the candidate concept before repointing; drop (reword to prose) if no
successor fits; never invent a slug.** Only touch slugs in cross-ref positions
— backticked prose words that merely look like slugs (`main`, `payload`,
`kind`, `strict`, `sender`, `target`, …) are not cross-refs and stay.

## Workstream 2 — annotation backfill + stale cleanup (isolated phase)

This is a code-side workstream, logically separable from Workstream 1, and is
to be implemented and reviewed as its own isolated phase. It exists to preserve
the design→code mapping that Workstream 1 strips from the docs, by inverting it
into the durable direction: a `@concept: <slug>` annotation at the code site,
which `grep` finds.

### Ordering dependency

The doc citations are the **site map** for the backfill — `claim-producer.md`'s
citation tells the implementer where claim-producer is expressed. Therefore the
annotation phase must read the concept docs **while their citations are still
present** (i.e., before or independently of Workstream 1's strip). Sequencing is
write-plan's concern, but the dependency is firm: do not strip a concept's
citations before the backfill has used them as the site map. (Simplest
satisfying order: annotation phase first, doc-strip second.)

### Backfill

For each concept currently **cited in its doc but unannotated in code**, ensure
at least one `@concept: <slug>` annotation exists at the most-specific in-repo
load-bearing site (the site the doc's citations point at). As of this spec the
candidate set is 17 concepts: `atomic-staging`, `claim`, `claim-producer`,
`claim-scope`, `conformance`, `executor`, `graph`, `module-layout`,
`named-event`, `persistence-database`, `replica`, `rimsky-yml`, `sdk`,
`sensor`, `service`, `supervisor`, `write-semantics`.

Discipline:
- Annotate the **most-specific** load-bearing site (type, function head, or the
  protocol/interface declaration), same granularity as `@blessed-invariant`.
  No carpet-bombing.
- Several candidates have **no clean in-repo site**: meta-concepts
  (`module-layout`, `rimsky-yml`), the explicitly-not-modeled `replica`, and
  concepts whose implementations were carved out to a sibling repo
  (`claim-producer`, `executor`, `sensor`, `conformance`, `sdk` — their only
  in-repo expression is the protocol/interface). For these: annotate the
  in-repo protocol/interface site if one genuinely carries the concept;
  otherwise record "no in-repo site" in the implementation notes and skip.
  **Never fabricate** a site to hit a count. The real backfill count will be
  under 17.

### Stale-annotation cleanup

Five annotation slugs exist in code with no live concept file:
`last-outcome` (retired), `run-tree`, `held-durable`, `aggregation-policy`,
`retention`. For each, resolve the dangling link:
- **Repoint** to the correct live concept if there is an obvious successor
  (likely: `run-tree` → `run-scope`, `held-durable` → `claim-lifetime`,
  `aggregation-policy` → `cancel-siblings` or `terminal-resolution`,
  `retention` → `claim-lifetime` or `orphan-reaper`). The implementer confirms
  the successor by reading the candidate concept docs before repointing — do
  not guess.
- **Remove** the annotation if it maps to nothing live and no successor fits.
- `last-outcome` is retired (replaced by `signal` / `settling_signal_type`):
  repoint to the live successor concept the retirement note names, or remove.

## Verification

**Workstream 1 (per-file checkable):**
- Concept bodies + Notes: grep each live `concepts/*.md` for codebase-citation
  prefixes (`code:`/`pkg:`/`file:`/`table:`/`col:`/`proto:`/`route:`/`cfg:`/`env:`/`cmd:`),
  bare paths (`\b[a-z_]+/[a-z_/]+\.(go|ts|sql|ya?ml|proto|txt)\b`), **bare
  identifier names (`rimsky_[a-z_]+`)**, **backtick-wrapped route literals
  (`` `/[a-z] ``)**, and external-doc references (`docs/`, README, CHANGELOG,
  sibling-repo paths) → must return **zero**.

  **The grep is a floor, not a ceiling.** It catches the high-volume,
  mechanically-detectable citation forms. It cannot reliably distinguish some
  tail forms from prose — a bare proto message-type name (`ExecuteRequest`), a
  bare CEL identifier, a dependency library name. Those are caught by the
  per-file hand-conversion (the implementer reads each file and applies the
  conversion discipline) and by the `review-work` design-doc compliance cycle,
  which is the authoritative backstop. A clean grep is necessary, not
  sufficient.
- Cross-ref integrity: every slug in a `see also:` / `Adjacent:` /
  `concept:<slug>` position resolves to a live `concepts/<slug>.md` file →
  **zero** dangling slugs.
- Open tensions: grep each live `tensions/*.md` `## Resolution candidates`
  section for paths / symbol citations / external-doc refs → **zero**.
  (Evidence / What-is-muddy untouched.)

**Workstream 2:**
- Every backfill-target concept that has an in-repo site carries ≥1
  `@concept:` annotation; concepts recorded as "no in-repo site" are
  documented in the implementation notes.
- Every `@concept: <slug>` annotation in code resolves to a live concept file
  (zero dangling slugs) — i.e., the 5 stale slugs are gone.
- `go build ./... && go test ./... && make lint` clean (annotations are
  comments; this confirms no accidental breakage).

**Both:**
- `.ok-planner/design/concepts.md` regenerated (execute-plan step 5a) and its
  one-sentence definitions also satisfy B2.
- Meaning preservation (no Definition / Boundary / Invariant semantically
  altered) is covered by the execute-plan divergence audit and the review-work
  code-review cycle. The review-work design-doc compliance cycle re-runs the
  Workstream-1 checks above.

## Testing strategy

No new automated tests. Verification is the grep-based compliance checks above
plus the standard rimsky build/test/lint gate for the annotation phase. The
two review-work cycles (code-review + design-doc compliance) are the
correctness gate; the divergence audit guards meaning preservation.

## Design changes

These mutate the design docs and are applied by execute-plan (Workstream 1).
Workstream 2 (code annotations) is ordinary implementation, not a design-doc
mutation, and is covered by the spec body above.

- **Concepts:** for every live `.ok-planner/design/concepts/<slug>.md` flagged
  by the Workstream-1 compliance check (the full union sweep — prefixed
  citations, bare paths, bare `rimsky_` identifiers, Owns/external-doc lines,
  and dangling cross-refs; effectively all 70 live files; the authoritative
  set is whatever the check flags, not a fixed count), rewrite the body **and**
  Notes in place to (a) remove all codebase-surface citations per the
  Conversion discipline and (b) repair any dangling concept-slug cross-ref per
  the Dangling cross-refs subsection, preserving conceptual meaning. Append a
  Notes entry to each touched file:
  `2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.`
- **Tensions:** for every live `.ok-planner/design/tensions/<slug>.md` whose
  `## Resolution candidates` section cites a path, symbol, or external doc,
  rewrite that section to state resolutions at the concept level (per the
  Tension treatment). Leave `## What is muddy` / `## Evidence` untouched. No
  tension files move state (none are being resolved or rejected here).
- **TOC:** regenerate `.ok-planner/design/concepts.md` after the concept
  bodies are clean (execute-plan step 5a), so its one-sentence definitions
  inherit the self-contained first sentences.

No concept is created, retired, split, or merged. No tension changes status.
The catalog's shape is unchanged; only its citation surface is.
