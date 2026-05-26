# Concept-doc self-containment + annotation backfill — Implementation Plan

**Spec:** .ok-planner/specs/2026-05-25-concept-doc-self-containment-design.md
**Goal:** Make every live concept doc self-contained (strip all codebase-surface citations, repair dangling concept-slug cross-refs), and invert the lost design→code mapping into `@concept:` annotations in source.
**Architecture:** Two workstreams in one run. WS2 (annotation backfill + stale-slug cleanup, code) runs FIRST because the doc citations are its site map. WS1 (concept-body + tension self-containment, design docs) runs second, in alphabetical batches, then a final pass cleans tensions and regenerates the TOC.
**Tech Stack:** Markdown design docs under `.ok-planner/design/`; Go/TS/proto source annotations (`@concept:` comments); grep-based verification.

This plan executes start to finish in one `execute-plan` run. All work is local edits under `.ok-planner/design/` (markdown) and source-comment edits (Pass 1 only). No commits, no git writes beyond the working tree.

**Read the spec before starting.** The spec is the authoritative source for the B2 rule, the conversion discipline (per citation kind), the dangling-cross-ref mapping, and the backfill/stale-slug discipline. This plan is operational (which files, which order, what to verify); it summarizes the discipline but the spec is the full statement. The implementer for each pass should read the named spec sections.

---

## Pass 1: Annotation backfill + stale-slug cleanup (Workstream 2)

**Goal:** Add `@concept:` annotations at the in-repo sites for concepts cited-but-unannotated, and resolve the 5 dangling annotation slugs — so the design→code mapping survives WS1's citation strip. This pass MUST complete before any WS1 pass strips citations (the doc citations are the site map).
**Scope:** Tasks 1–2
**End state:** working
**Verification:** `go build ./... && make lint` exits 0, AND `grep -rhoE '@concept:[[:space:]]*[a-z0-9-]+' --include='*.go' --include='*.ts' --include='*.proto' . | sed -E 's/@concept:[[:space:]]*//' | sort -u` contains none of: `last-outcome`, `run-tree`, `held-durable`, `aggregation-policy`, `retention` (every remaining slug must match a live `.ok-planner/design/concepts/<slug>.md`).

> Note on `go test`: annotations are comments and cannot change runtime behavior. The deterministic inter-pass gate is `go build ./... && make lint` (no Docker dependency). The full `go test ./...` suite (testcontainers/Docker) is run by `review-work`'s code-review cycle at the end, per the spec's Verification section.

### Task 1: Backfill `@concept:` annotations for cited-but-unannotated concepts

**Files:** Go/TS source under the repo (sites identified per-concept); read `.ok-planner/design/concepts/*.md` for the site map.

Candidate concepts (cited in their doc, no `@concept:` annotation in source as of this plan): `atomic-staging`, `claim`, `claim-producer`, `claim-scope`, `conformance`, `executor`, `graph`, `module-layout`, `named-event`, `persistence-database`, `replica`, `rimsky-yml`, `sdk`, `sensor`, `service`, `supervisor`, `write-semantics`.

**Steps:**
1. Re-derive the candidate set fresh (do not trust the list above blindly): for each live concept slug, check whether `grep -rl "@concept:[[:space:]]*<slug>" --include='*.go' --include='*.ts' --include='*.proto' .` returns anything. The candidates are the cited-in-doc slugs with no hit, where "cited" means the doc carries a **prefixed** `code:`/`pkg:`/`file:`/`table:`/`col:`/`proto:`/`route:`/`cfg:`/`cmd:` citation — the kind that names a specific in-repo site. (Intentional: a bare `rimsky_<table>` mention alone does not imply a distinct annotation site, so the candidate set is ~17, not the full ~25 swept by WS1. Use the prefixed reading.)
2. For each candidate, open its `concepts/<slug>.md` and read the codebase citations still present (they are the site map — this is why this pass runs before the strip). Identify the **most-specific in-repo load-bearing site** the citations point at (a Go type, interface, function head, handler, or schema/migration declaration). Prefer a Go interface/type site over a `.proto` file (avoids a proto-gen round-trip).
3. Add a `@concept: <slug>` comment at that site, same granularity as `@blessed-invariant` — one annotation at the primary site, no carpet-bombing.
4. For candidates with **no clean in-repo site** — meta-concepts (`module-layout`, `rimsky-yml`), the not-modeled `replica`, and concepts whose impls were carved out to a sibling repo (`claim-producer`, `executor`, `sensor`, `conformance`, `sdk` — annotate the in-repo protocol/interface only if it genuinely carries the concept): if there is genuinely no in-repo site, record `<slug>: no in-repo site` in the implementation notes and skip. **Never fabricate a site.**
5. If any annotation lands in a `.proto` file, run `make proto-gen`, then proceed.
6. Run `go build ./...` and confirm it exits 0.

**Verification:** `go build ./...` exits 0; `grep -rl "@concept:" --include='*.go' --include='*.ts' --include='*.proto' .` shows the new annotations; implementation notes list any "no in-repo site" skips.

### Task 2: Resolve the 5 dangling annotation slugs

**Files:** the source files carrying `@concept: last-outcome | run-tree | held-durable | aggregation-policy | retention`.

**Steps:**
1. For each of the 5 slugs, run `grep -rn "@concept:[[:space:]]*<slug>" --include='*.go' --include='*.ts' --include='*.proto' .` to find every site.
2. Determine the live successor by reading the candidate concept docs (do not guess): `run-tree` → `run-scope`; `held-durable` → `claim-lifetime`; `aggregation-policy` → `cancel-siblings` or `terminal-resolution` (read both, pick the one the annotated code is about); `retention` → `claim-lifetime` or `orphan-reaper` (same); `last-outcome` → `signal` (per its retirement note, which names `signal`/`settling_signal_type`).
3. Repoint each annotation to the confirmed live successor slug. If a site maps to nothing live and no successor fits, remove the annotation.
4. Confirm zero dangling slugs remain: every `@concept:` slug in source resolves to a live `concepts/<slug>.md`.

**Verification:** the Pass-1 dangling-slug grep (see pass Verification) returns none of the 5; `go build ./... && make lint` exits 0.

---

## Pass 2: Concept conversion — batch A (advisory-lock … cascade) (Workstream 1)

**Goal:** Strip codebase-surface citations and repair dangling cross-refs in the first alphabetical batch of concept docs.
**Scope:** Task 3
**End state:** working
**Verification:** the batch-compliance grep (below) returns no output for batch-A files.

### Task 3: Convert batch-A concept files

**Files (all under `.ok-planner/design/concepts/`):** `advisory-lock.md`, `anonymous-mode.md`, `api-key.md`, `asset.md`, `atomic-staging.md`, `attribute.md`, `auto-terminal.md`, `backfill.md`, `blob-backend.md`, `breakpoint.md`, `cancel-siblings.md`, `cascade.md`.

**Conversion procedure (apply to each file; full discipline in spec §"Workstream 1 → Conversion discipline" and §"Dangling concept-slug cross-refs"):**
1. Read the file in full.
2. Remove every codebase-surface citation, minimally repairing the sentence to stay accurate and self-contained:
   - Prefixed `code:`/`pkg:`/`file:` and bare paths (`x/y.go`, `dir/sub/`) → state the behavior/role in prose; if it enforces an invariant, cite the existing `@blessed-invariant` tag (numeric or named form) instead. Drop the path.
   - Prefixed `table:`/`col:` AND bare `rimsky_<table>` / `rimsky_<table>.<col>` in prose → "a persisted … ledger/record/row" / describe the field's role. Drop the name. (Bare `rimsky_` tokens are the dominant form — catch them all.)
   - `proto:`/`route:`/`cfg:`/`env:`/`cmd:` **and bare route literals in prose** (`/events`, `/instances/{id}/messages`, `/observability/*`) → describe the wire message / endpoint / setting / command by role. Drop the literal.
   - External-doc refs (`docs/...`, README, CHANGELOG, sibling-repo paths, URLs) → prose or drop.
   - "Owns / Does NOT own" lines naming paths → restate naming neighbor concepts by slug or in prose. Slug-only Owns lines stay untouched.
   - `## Annotation sites` sections (present in ~20 files, almost entirely `code:` citation bullets) → **delete the entire section.** It is the design→code map, which Pass 1's in-code `@concept:` annotations now carry in the durable direction. If the section holds any non-citation prose worth keeping, fold that into the relevant section (Boundaries/Purpose) before deleting.
   - Notes entries citing paths → keep the date + `spec:` slug, replace the path with path-free prose.
3. Repair dangling concept-slug cross-refs (slugs in `see also:`/`Adjacent:`/`concept:` positions that don't resolve to a live concept file) per the spec mapping: `scope`→`claim-scope`, `rimsky-cli`→`rimsky`, `node-state`→`node-run`, `schedule`→`sensor`, `on-event-handler`→`node-subscription`, `last-outcome`→`signal`, `quality-rule`→reword to prose (no successor), `held-claim`→confirm and repoint or drop. Confirm each successor by reading it; never invent a slug. Leave backticked prose words (`main`, `payload`, `kind`, `strict`, …) that are not cross-refs.
4. **Never fabricate** a `@blessed-invariant` tag or a concept slug; if no stable token exists, use prose.
5. If the file was modified, append a Notes entry: `2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.` If a file needed no change, leave it untouched (no Notes entry).

**Verification (batch-compliance grep — run from repo root):**
```
for f in advisory-lock anonymous-mode api-key asset atomic-staging attribute auto-terminal backfill blob-backend breakpoint cancel-siblings cascade; do
  grep -nE '`(code|pkg|file|table|col|proto|route|cfg|env|cmd):|`/[a-z]|[a-z_]+/[a-z_/]+\.(go|ts|sql|ya?ml|proto|txt)|rimsky_[a-z_]+|docs/|README|CHANGELOG' ".ok-planner/design/concepts/$f.md" && echo "VIOLATION: $f";
done
```
Must print nothing. (`rimsky_[a-z_]+` catches bare table/identifier names — `rimsky` without an underscore is fine; `` `/[a-z] `` catches backtick-wrapped route literals and `/etc/...`-style paths.)

**The grep is a floor, not a ceiling.** It catches the high-volume mechanical forms. It cannot reliably distinguish some tail citations from prose — a bare proto message-type name (e.g. a request-type identifier), a bare CEL identifier, a dependency library name. Those are caught by reading each file during conversion (step 1–5 above) and by `review-work`'s design-doc compliance cycle. A clean grep is necessary, not sufficient — actually read each file; don't grep-and-go.

---

## Pass 3: Concept conversion — batch B (cascade-graph … delegation) (Workstream 1)

**Goal:** Same procedure, batch B.
**Scope:** Task 4
**End state:** working
**Verification:** batch-compliance grep returns no output for batch-B files.

### Task 4: Convert batch-B concept files

**Files:** `cascade-graph.md`, `claim.md`, `claim-co-holdership.md`, `claim-handle.md`, `claim-lifetime.md`, `claim-producer.md`, `claim-scope.md`, `claim-tree.md`, `conformance.md`, `control-api.md`, `data-processing.md`, `delegation.md`.

**Steps:** Apply the Conversion procedure from Task 3 to each file above.

**Verification:** the batch-compliance grep (Task 3 form) with the batch-B file list prints nothing.

---

## Pass 4: Concept conversion — batch C (discovery-cache … lifecycle-subscriber) (Workstream 1)

**Goal:** Same procedure, batch C.
**Scope:** Task 5
**End state:** working
**Verification:** batch-compliance grep returns no output for batch-C files.

### Task 5: Convert batch-C concept files

**Files:** `discovery-cache.md`, `dry-run.md`, `error-policy.md`, `event-log.md`, `executor.md`, `fan-out.md`, `frame.md`, `graph.md`, `inertness.md`, `instance.md`, `invalidate.md`, `lifecycle-subscriber.md`.

**Steps:** Apply the Conversion procedure from Task 3 to each file above.

**Verification:** the batch-compliance grep (Task 3 form) with the batch-C file list prints nothing.

---

## Pass 5: Concept conversion — batch D (lineage … parked-state) (Workstream 1)

**Goal:** Same procedure, batch D. Includes the heaviest files (`lineage-record`, `message`).
**Scope:** Task 6
**End state:** working
**Verification:** batch-compliance grep returns no output for batch-D files.

### Task 6: Convert batch-D concept files

**Files:** `lineage.md`, `lineage-record.md`, `message.md`, `module-layout.md`, `named-event.md`, `named-lock.md`, `node.md`, `node-run.md`, `node-subscription.md`, `observability.md`, `orphan-reaper.md`, `parked-state.md`.

**Steps:** Apply the Conversion procedure from Task 3 to each file above.

**Verification:** the batch-compliance grep (Task 3 form) with the batch-D file list prints nothing.

---

## Pass 6: Concept conversion — batch E (permission … service) (Workstream 1)

**Goal:** Same procedure, batch E.
**Scope:** Task 7
**End state:** working
**Verification:** batch-compliance grep returns no output for batch-E files.

### Task 7: Convert batch-E concept files

**Files:** `permission.md`, `persistence-database.md`, `publisher.md`, `publisher-subscription.md`, `replica.md`, `rimsky.md`, `rimsky-yml.md`, `role-template.md`, `run-scope.md`, `sdk.md`, `sensor.md`, `service.md`.

**Steps:** Apply the Conversion procedure from Task 3 to each file above.

**Verification:** the batch-compliance grep (Task 3 form) with the batch-E file list prints nothing.

---

## Pass 7: Concept conversion — batch F (signal … write-semantics) (Workstream 1)

**Goal:** Same procedure, final concept batch.
**Scope:** Task 8
**End state:** working
**Verification:** batch-compliance grep returns no output for batch-F files.

### Task 8: Convert batch-F concept files

**Files:** `signal.md`, `sub-graph.md`, `supervisor.md`, `tag.md`, `template.md`, `terminal-resolution.md`, `transition-reason.md`, `validation.md`, `wait-set.md`, `write-semantics.md`.

**Steps:** Apply the Conversion procedure from Task 3 to each file above.

**Verification:** the batch-compliance grep (Task 3 form) with the batch-F file list prints nothing.

---

## Pass 8: Tensions + TOC regen + catalog-wide verification (Workstream 1)

**Goal:** Clean tension Resolution-candidate paths, regenerate the concept TOC, and run the catalog-wide compliance + cross-ref-integrity gate.
**Scope:** Tasks 9–11
**End state:** working
**Verification:** all three checks in Task 11 return clean.

### Task 9: Strip paths from tension Resolution candidates (light touch)

**Files (under `.ok-planner/design/tensions/`):** the authoritative set is whatever the Task-9 verification grep flags — **derive it fresh, don't trust a hardcoded list.** As of this plan that is **15** tensions: `blob-backend-conformance-fixture-asymmetry.md`, `coalesced-fire-observability-gap.md`, `compose-prefix-client-side.md`, `control-api-version-prefix.md`, `events-kind-no-enum.md`, `force-fire-204-hides-asynchrony.md`, `frame-lookup-on-every-enqueue.md`, `quality-rule-custom-handler-ordering.md`, `quality-rule-severity-string-footgun.md`, `reaper-vs-bail-abandon-asymmetry.md`, `serial-queue-per-instance.md`, `state-count-drift.md`, `stub-mode-signature-no-proto-surface.md`, `substitution-grammar-count-drift.md`, `timeout-policy-asymmetry.md`. (The set spans Resolution-candidate citations of external `docs/...` paths, bare code paths, bare `rimsky_` table names, and symbols — broader than the prior audit's `docs/`-only subset; that's why the grep, not a hardcoded list, is authoritative.)

**Steps:**
1. Run the Task-9 verification grep (below) to get the authoritative list. For each flagged tension, edit **only** the `## Resolution candidates` section: restate each resolution shape at the concept level (which concept's Definition/Boundaries/Invariants would change, what property the code would hold). Remove file paths, symbol citations, bare `rimsky_` names, and external-doc refs (`docs/...`).
2. Leave `## What is muddy` and `## Evidence` untouched — they are point-in-time snapshots and keep their code citations.
3. Do not change any tension's `status`; do not move any tension file.

**Verification:**
```
for f in .ok-planner/design/tensions/*.md; do
  awk '/^## Resolution candidates/{flag=1;next}/^## /{flag=0}flag' "$f" | grep -nE '`(code|pkg|file):|`/[a-z]|docs/|[a-z_]+/[a-z_/]+\.(go|ts|sql|ya?ml|proto)|rimsky_[a-z_]+' && echo "VIOLATION: $f";
done
```
Must print nothing.

### Task 10: Regenerate the concept TOC

**Files:** `.ok-planner/design/concepts.md`

**Steps:**
1. Regenerate `concepts.md` per the format in `ok-planner:discover-design`'s SKILL.md: a sorted alphabetical bullet list, one per live concept, `` `<slug>` `` (with `(aliases: …)` if the concept's frontmatter lists aliases) `— <first sentence of the concept's lead section>`. The lead section is `## What it is` for ~49 files and `## Definition` for ~21 (e.g. `asset`, `graph`, `claim-lifetime`, `sensor`, `service`) — use whichever a given file leads with. Append the "Retired concepts" section listing `_retired/` entries. Because the concept bodies are now self-contained, the pulled first sentences will be too.
2. Confirm `concepts.md` itself contains no codebase-surface citation (run the batch-compliance grep against it).

**Verification:** `grep -nE '`(code|pkg|file|table|col|proto|route|cfg|env|cmd):|[a-z_]+/[a-z_/]+\.(go|ts|sql|ya?ml|proto|txt)|rimsky_[a-z_]+|docs/|README|CHANGELOG' .ok-planner/design/concepts.md` prints nothing; the TOC entry count matches the live concept-file count.

### Task 11: Catalog-wide final verification

**Files:** none modified; verification only.

**Steps:**
1. **Full compliance grep** across every live concept body + Notes:
   ```
   grep -rnE '`(code|pkg|file|table|col|proto|route|cfg|env|cmd):|`/[a-z]|[a-z_]+/[a-z_/]+\.(go|ts|sql|ya?ml|proto|txt)|rimsky_[a-z_]+|docs/|README|CHANGELOG' .ok-planner/design/concepts/*.md
   ```
   Must print nothing. (Grep is a floor — see Task 3's note; the hand-read conversion and `review-work` design-doc compliance cycle catch tail forms a regex can't distinguish from prose.)
2. **Cross-ref integrity** — every `see also:`/`Adjacent:`/`concept:` slug resolves to a live concept file:
   ```
   ls .ok-planner/design/concepts/*.md | xargs -n1 basename | sed 's/\.md$//' | sort > /tmp/live.txt
   grep -rhoE '(see also:|Adjacent:|`concept:)[^.]*' .ok-planner/design/concepts/*.md | grep -oE '`[a-z][a-z0-9-]+`' | tr -d '`' | sort -u > /tmp/refd.txt
   comm -23 /tmp/refd.txt /tmp/live.txt
   ```
   Inspect output: any slug printed that is a genuine cross-ref (not a backticked prose word) is a dangling ref and must be repaired (return to the relevant batch). Known prose-word false positives (`main`, `payload`, `kind`, `strict`, `sender`, `target`, `cancelled`, `alias`) are acceptable.
3. **Tension Resolution-candidates** clean (re-run Task 9's grep).

**Verification:** step 1 prints nothing; step 2 prints only acknowledged prose-word false positives; step 3 prints nothing.

---

## Manual checks after completion

None. All verification is grep- and build-based and runs autonomously. Meaning-preservation (that no concept's Definition/Boundaries/Invariants changed semantically) is covered by `execute-plan`'s divergence audit and `review-work`'s two cycles — not a manual step here.
