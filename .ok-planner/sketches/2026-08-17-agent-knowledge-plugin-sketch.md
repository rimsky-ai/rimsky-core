# Agent-knowledge plugin — Design Sketch

**Date:** 2026-08-17
**Status:** Sketch (not a sprint; not authorization to build)

## Idea

Ship rimsky's agent-facing knowledge as an in-tree Claude Code plugin whose skill is mechanical and stable, and whose corpus is the release's placed documents under `docs/`, vendored into each consuming project's estate at the rimsky release that project runs. The plugin bundles no corpus. `/document` produces the corpus at each release; `/release` publishes it as a release asset; the plugin's converge fetches the asset for the project's pinned release and stamps it. A plugin update never moves a project's docs. Two consequences follow: `../rimsky-docs` and its reconcile machinery retire, and the plugin lives here, beside the documents it vendors.

## Shape

### The two stories

- **Expert agent.** As an engineer building on rimsky, I want my agent to be an expert in rimsky so that it can use rimsky to effectively solve problems in my project.
- **Release fidelity.** As an engineer building on rimsky, I want my agent's rimsky knowledge to match the rimsky release my project runs, so that what it builds works against that release.

The stories name wants, not mechanisms. The audit builds the experiments for them; those experiments are the navigability benchmark, and nothing else maintains one. The wording lets the audit box a subagent with only the installed skill, hand it a rimsky task in a project, and check the result against the pinned release.

### The decisions

| decision | choice |
|---|---|
| knowledge ships as a skill | Agent-facing rimsky knowledge is a Claude Code skill: a mechanical router over a vendored docs corpus. |
| the plugin lives in rimsky-core | `plugins/rimsky/` in this repo; the repo is its own marketplace (`.claude-plugin/marketplace.json` at the root). One less repo; the documents ride with the core, and so does the skill that uses them; the root skill may widen its scope later. |
| the plugin is rimsky's administration front door in a consuming project | Diagnose, converge, switch version. Mechanical, stable, release-agnostic. It bundles no corpus and carries no release-specific prose. |
| estate-driven vendoring | Converge fetches the docs bundle for the pinned release into `.claude/skills/rimsky/` — `SKILL.md` plus `docs/` — and writes a stamp. Diagnose compares stamp to pin. |
| the pin is declared; detection proposes | The estate config carries `rimsky: vX.Y.Z`. First converge detects a candidate from `go.mod` (`github.com/rimsky-ai/rimsky-core`) or a compose image tag, proposes it, and writes it on the owner's yes. Thereafter diagnose reports `declared = detected` or names the drift, and never rewrites without consent. Switching the project's rimsky version is "rewrite the declaration, re-converge". |
| the corpus is a release asset | `/release` packs the placed `docs/` at the release commit into `rimsky-docs-vX.Y.Z.tar.gz` with a manifest (release, `/document` stamp, entry file) and attaches it to the GitHub Release. Converge fetches by tag; a pin with no asset fails loudly naming the release. |
| the plugin's skill and the corpus split at one file | The plugin's router says "read the vendored corpus's index first" and nothing release-specific. The corpus's index (`docs/llms.txt`, the `llms-index` document type) carries the read-first layer and the route-by-task. |
| `rimsky-docs` retires | Its corpus, `/update-docs`, the `cmd/rimsky-docs-*` generators, and the ledger have no role: `/document` is the maintenance path. The owner archives the repo. |

### Layout in this repo

```
plugins/rimsky/
  .claude-plugin/plugin.json      name: rimsky; version: the plugin's own
  skills/rimsky/SKILL.md          the router: "read .claude/skills/rimsky/docs/llms.txt first"
  skills/rimsky-version/SKILL.md  echoes plugin version + the estate's stamped release
  admin/converge                  diagnose | converge | switch <version>
  admin/ADMINISTRATION.md         the judgment the core cannot encode
.claude-plugin/marketplace.json   the repo as marketplace, listing plugins/rimsky
```

The `admin/converge` + `admin/ADMINISTRATION.md` pair follows the ok-suite family convention so the vocabulary carries; nothing in ok-plugins needs to know rimsky exists.

### Layout in a consuming project

```
.rimsky/config.json               { "rimsky": "v0.15.0" }        the pin, owner-declared
.claude/skills/rimsky/
  SKILL.md                        materialized from the plugin (release-agnostic)
  docs/                           the release asset, unpacked
  .stamp                          release + /document stamp + fetch time
```

### Converge, in four steps

1. Read the pin from `.rimsky/config.json`; if absent, detect from `go.mod` / compose image tags, propose, write on consent.
2. Fetch `rimsky-docs-<pin>.tar.gz` from the GitHub Release for that tag; fail loudly naming the release when the asset is missing.
3. Unpack into `.claude/skills/rimsky/docs/`; materialize `SKILL.md`; write `.stamp`.
4. Diagnose is steps 1–2 without writing: compare `.stamp` to the pin and to detection.

### Release-side changes

- `/release` gains a step after `/document`'s placement: pack `docs/` and attach the asset. goreleaser owns GitHub Release creation today and uploads the CLI archives and SBOMs; the docs asset joins as an extra file or a `gh release upload` after creation.
- The plugin's `plugin.json` version bumps in the release commit like `package.json` does.

### Document-type changes (`.ok-planner/surface/documents/`)

- **Add `decisions`** → `docs/decisions/`: choice, rationale, rejected alternatives, per decision. The current skill's router tells the agent to read the decision before working around platform behavior; this is the highest-value content for an agent about to build the wrong thing.
- **Add `comparison`** → `docs/comparison.md`: rimsky against queues, workflow engines, and build systems — the "does rimsky fit" route. Seed the target by copying `../rimsky-docs`' `comparison.md` before the first run; the writer revises from there. The type carries a **Method** naming the comparators, the questions per comparator, and what "current" means; `/document` runs it as a handful of `sonnet` research dispatches before the writer and hands the findings over. (The Method mechanism landed in ok-plugins on 2026-08-17: a type may carry `## Method`; the ceremony runs it as `sonnet` leaf dispatches before the `opus` writer; a Method covers only facts outside the tree.)
- **Extend `cookbook`'s Covers** with the system-altitude patterns (`domain-stores`, `operational-health`) as recipes.
- **Grow `llms-index`'s Purpose** to be the agent's read-first router: the one-paragraph mental model, the read-first layer (concepts, capabilities, decisions), and route-by-task (fit / design / implement / deploy / debug), each row naming the handful of documents to open. This is where the current `SKILL.md`'s routing content goes.

### What the vendored corpus contains

The placed documents only — the publishable layer in shipped vocabulary. Concepts (`docs/concepts.md`), stories as capabilities with traps (`docs/capabilities.md`), the references, cookbook, examples, errors, protocols, and after the changes above decisions and comparison. Not the design corpus, not the audits: their user-facing content already reaches the agent through the assessments and traps projected into `capabilities.md`.

## Open questions

- **Dev builds have no asset.** `make dev-release` cuts no GitHub Release, so a project pinned to a `v<next>.0-dev.<date>.g<sha>` version has nothing to fetch. Options: dev builds also publish an asset somewhere; the plugin refuses dev pins; the plugin falls back to `git archive` of `docs/` at the sha. Unruled.
- **Estate config path.** `.rimsky/config.json` is a guess; the pin could also live in the stamp file or under `.claude/`. The ok-suite convention is `.ok-<name>/config.json`; a product estate has no fixed convention yet.
- **Consent for the first fetch.** Converge writes into `.claude/skills/`; the ok-suite treats vendored skills as suite-owned and writes them without asking. The same holds here, but the first pin write is owner-declared configuration and needs the owner's yes.
- **Whether `llms-index` grows or a new `skill-router` type is declared.** Growing the existing type keeps one entry file; a separate type keeps `llms.txt` a plain index for non-Claude agents.
- **The `rimsky-version` skill.** It echoes literals today (plugin version, `reconciledAgainst`). Under this model the second number is the estate's stamp, read from `.stamp` at invocation — a read, not a literal.
- **Marketplace source for existing users.** Users who installed `rimsky@rimsky-docs` must re-point to the rimsky-core marketplace; the old marketplace can carry a final release whose only content is that pointer.

## Risks / unknowns

- The audit's experiment for the expert-agent story is the only navigability benchmark. If the audit's instrument cannot box a subagent with a skill and a task, the story comes back `unsupported` and the benchmark does not exist. This is a suite question, not a rimsky one.
- A vendored corpus at the wrong pin is worse than none: the one navigability defect on record (`rimsky-docs-feedback.md`) was version skew that no page announced. The stamp and diagnose's drift line are the mitigation; they only work if the owner runs diagnose.
- `docs/` is generated and placed at each release, but the release asset is only as good as the last `/document` run. A release cut without `/document` ships a stale asset silently unless `/release` checks the placement stamp against the release commit.
- The comparison document's Method depends on web research by `sonnet` subagents; its currency is bounded by what those searches find, and the document must say what date it reflects.
- Repo growth: `plugins/` and a root `.claude-plugin/` join a repo whose `CLAUDE.md` currently says it carries no docs sources. That line is stale already (`docs/` exists under `/document`) and goes in the same sprint.

## What this is not

- Not a change to `/document`'s spine beyond the Method mechanism already landed in ok-plugins.
- Not a general "product estate" convention for ok-plugins; the plugin borrows the converge/diagnose/stamp shape without joining the suite.
- Not a migration path for `../rimsky-docs`' history: the owner archives the repo; its ledger and generators stay behind.
- Not a benchmark artifact: the stories are the lever, the audit's experiments are the instrument.
- Not the plugin's future scope (running rimsky, scaffolding templates); the sketch covers vendoring and version administration only.
