# Build a "Build an X in Rimsky" Example Generator in Rimsky

A reference walkthrough for a Rimsky cascade that generates new entries
for the `docs/examples/` catalog: searches the web for problem domains,
fans out parallel idea generators with different framings, judges
candidates against the existing catalog and against rimsky-fit
criteria, scaffolds the winner, validates and reviews it, and opens a
PR adding it to the repo.

The recursion is the joke. The demonstration value is real — this
example exercises three patterns that none of the prior catalog
entries do simultaneously: multi-agent fan-out, judge-of-agents
adversarial scoring, and self-input (the cascade reads its own output
directory as an input).

## Status

Sketched. Reference implementation gated behind
`bug-fix-from-tickets.md` and `build-a-gas-town.md` — those primitives
are this example's dependencies. Shipping out of order would be funny
but unhelpful.

## Why this exists

The catalog needs more examples. Generating them by hand:

- Is slow.
- Biases toward what the author thinks of on a particular Tuesday.
- Misses entire problem domains the author isn't immersed in.
- Doesn't scale with the catalog's growing surface area (each new
  primitive landed in rimsky should arguably get a focused example).

A generator does better at all four — and as a side effect demonstrates
what rimsky is for. The catalog's existence becomes a self-justifying
artifact.

## The cascade

```
                     ┌─────────────────┐
                     │  trigger:       │
                     │  cron / manual  │
                     └────────┬────────┘
                              │
                              ▼
                  ┌──────────────────────┐
                  │ discover-sources     │  HN, Twitter/X, GitHub trending,
                  │ (web search)         │  blog feeds, llms.txt registries
                  └──────────┬───────────┘
                             │
                             ▼
                  ┌──────────────────────┐
                  │ extract-candidates   │  parse signal → list of
                  │                      │  problem-domain seeds
                  └──────────┬───────────┘
                             │
        ┌────────────┬───────┴──────┬─────────────┐
        │            │              │             │
        ▼            ▼              ▼             ▼
   idea-gen-      idea-gen-      idea-gen-     idea-gen-
   enterprise    whimsical      devtools       meta
        │            │              │             │
        └────────────┴──────┬───────┴─────────────┘
                            │
                            ▼
                ┌──────────────────────┐
                │ dedupe-against-repo  │  reads docs/examples/
                │                      │  semantic match against existing
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │ judge-candidates     │  score each: rimsky-fit, novelty,
                │                      │  interest, distinctness, depth
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │ pick-top-n           │  deterministic top 1-3 by score
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │ scaffold-example     │  generates walkthrough.md, stub
                │                      │  rimsky.yml + templates/
                └──────────┬───────────┘
                           │
                           ▼
                ┌──────────────────────┐
                │ tester               │  validates YAML, runs
                │                      │  rimsky-conformance on cascade
                └──────────┬───────────┘
                           │
                           ▼   (yaml errors)
                           ├─────────────────► invalidate(scaffold-example)
                           │
                           ▼
                ┌──────────────────────┐
                │ reviewer             │  adversarial: redundancy with
                │                      │  catalog, primitive accuracy,
                │                      │  walkthrough quality
                └──────────┬───────────┘
                           │
                           ▼   (rejected)
                           ├─────────────────► invalidate(scaffold-example)
                           │   (3 rounds)         (with critique as constraint)
                           │
                           ▼  (accepted)
                ┌──────────────────────┐
                │ open-pr              │  creates PR adding the example
                │                      │  to docs/examples/
                └──────────────────────┘
```

## Primitives exercised

### Multi-agent fan-out (parallel cascade nodes)

Four idea-generator nodes run in parallel, each invoking a
`claude-agent` executor with a different system prompt:

| Generator | Framing | Looks for |
|---|---|---|
| `idea-gen-enterprise` | "What are teams in operations / data / SRE struggling to coordinate?" | Production headaches, multi-system sync, compliance flows |
| `idea-gen-whimsical` | "What's a fun, weird, or low-stakes thing to orchestrate?" | Games, contests, generative art, hobbyist projects |
| `idea-gen-devtools` | "What's a developer-experience pain point that needs cascade orchestration?" | CI/CD, doc generation, codegen, refactors |
| `idea-gen-meta` | "What's a self-referential or reflexive cascade worth building?" | Tools that build tools; tools that observe themselves |

Each generator returns 3-5 candidate examples with a short pitch and
the primitives each would exercise. Diversity by construction.

### Judge-of-agents pattern

`judge-candidates` is a single `claude-agent` invocation that receives
all candidates from all generators (typically 12-20 total) plus the
existing catalog entries. It scores each candidate on five rubrics:

- **Rimsky-fit (0-5):** does it exercise primitives load-bearingly?
- **Novelty (0-5):** does it cover ground not in the existing catalog?
- **Interest (0-5):** is it interesting? Would a reader want to read it?
- **Distinctness (0-5):** is it clearly different from adjacent
  catalog entries?
- **Depth (0-5):** can it be a substantive walkthrough, not a
  one-paragraph stub?

Output is structured (Valibot or zod schema enforced). The lowest
scores get filtered before scoring is finalized.

This is the same adversarial-judge pattern from
`build-a-gas-town.md` — applied to the *generation pipeline itself*
rather than to code review.

### Cascade reads its own output

`dedupe-against-repo` walks `docs/examples/*.md` and uses semantic
embedding similarity (small model, fast) plus an LLM judgement on the
top-K closest matches to filter candidates that overlap existing
entries. This is the load-bearing self-referential primitive: the
catalog is an input to the cascade that grows the catalog.

The implementation gotcha: the reading-the-repo path has to be
deterministic and audit-friendly. The reference impl ships a small
peer (`docs-indexer`) that hashes the catalog state at the start of
each generator run, attaches the hash to the cascade frame, and
guarantees the dedupe decision is reproducible against that hash.

### Reverse cascade from reviewer to scaffolder

Two reverse-invalidation edges:

```yaml
nodes:
  - id: tester
    error_types:
      yaml_invalid:
        action: invalidate
        targets: [scaffold-example]
        budget: 3

  - id: reviewer
    error_types:
      quality_below_threshold:
        action: invalidate
        targets: [scaffold-example]
        budget: 3
      redundant_with_existing:
        action: invalidate
        targets: [pick-top-n]      # try a different candidate from the top-N
        budget: 2
      factually_wrong_about_rimsky:
        action: invalidate
        targets: [scaffold-example]
        budget: 3
```

Reviewer's critique is structured and gets attached as constraint
input to the next `scaffold-example` invocation. The agent literally
sees "your previous draft was rejected because of X; address X
specifically" in its prompt.

### Held claim around the draft branch

Same shape as bug-fix-from-tickets:

```yaml
locks:
  - kind: scope
    scope:
      branch: "examples/${candidate.slug}"
    mode: serial
    held_by: [scaffold-example, tester, reviewer, open-pr]
```

The git claim producer commits the branch on accept (`open-pr`
succeeds, branch is now an open PR for human review) and abandons on
reject-after-budget (branch deleted, no orphan branches accumulate).

### Named lock for LLM budget

Speculative generation burns tokens. A counting named lock caps
concurrent agent invocations across all in-flight generator runs:

```yaml
named_locks:
  - name: "generator-budget"
    mode: counting
    limit: 6        # at most 6 concurrent claude-agent calls system-wide
```

A second lock caps cost-per-day:

```yaml
named_locks:
  - name: "daily-token-budget"
    mode: counting
    limit: 1_000_000   # tokens/day; held_by every claude-agent node
```

(The token-budget pattern is a rough fit for counting locks — held-by
ratio is approximate. A more honest implementation tracks tokens via a
producer that owns the budget ledger; counting lock is the
demonstrative shortcut.)

### Polyglot peers

| Peer | Language | Why |
|---|---|---|
| `discover-sources` | Go (http-node) | Web fetching + RSS, no heavy lifting |
| `extract-candidates` | TS (claude-agent) | LLM extraction from search results |
| `idea-gen-*` (×4) | TS (claude-agent) | All four are different prompts on the same executor |
| `dedupe-against-repo` | Go + TS | Go peer reads filesystem and computes embeddings; TS judge does the LLM-side decision on close matches |
| `judge-candidates` | TS (claude-agent) | Structured-output rubric scoring |
| `pick-top-n` | Go | Deterministic |
| `scaffold-example` | TS (claude-agent) | Long-form writing |
| `tester` | Go | Runs `rimsky-conformance` and YAML validation |
| `reviewer` | TS (claude-agent) | Adversarial critique |
| `open-pr` | Go | gh CLI or octokit |

## What the do-it-without-Rimsky baseline looks like

A representative implementation circa 2026:

- A Python script that:
  - Calls a search API.
  - Calls Claude four times in parallel with different prompts.
  - Calls Claude once to judge.
  - Writes a markdown file.
  - Calls Claude to review.
  - Loops on rejection.
  - Opens a PR.
- A separate cron job that triggers it weekly.
- Some hand-rolled state machine to handle the rejection loop.
- Whatever ad-hoc dedup-against-existing logic the author writes.

This works. For a single solo-author catalog, it's probably the right
implementation. The Rimsky version pulls ahead when:

- You want to compare *generator strategies* over time. Each strategy
  is a content-addressed template; performance metrics (acceptance
  rate, reviewer-round count, win-by-domain breakdown) accumulate per
  template hash. Strategy R&D becomes data-driven.
- The cascade triggers from non-cron sources — a webhook from a
  newsletter, an HN front-page event, a maintainer's manual
  invalidate. Reactive cascade naturally accommodates all of these.
- Multiple maintainers run their own generators against the same
  catalog. Per-branch and per-PR locks prevent collision; the
  generator pool is multi-tenant by construction.
- You want the rejection loop to be *visible* — not a Python loop
  variable, but a queryable cascade history. "Show me every example
  that was rejected three times and what the reviewer's complaint was"
  is a SQL query in Rimsky and a custom log-parser otherwise.

If those properties don't matter, write the Python script.

## Failure modes the example deliberately exhibits

1. **All idea generators propose dupes of existing examples.**
   Dedupe filters out everything; pick-top-n receives an empty set;
   policy fires `invalidate(['extract-candidates'])` with reason
   `all_candidates_redundant` to broaden the search next round.

2. **Reviewer rejects three drafts.** Budget exhausts; cascade routes
   to a human-escalation queue with the candidate, all three drafts,
   and all three critiques attached. The maintainer can either write
   the example by hand (using the drafts as starting points) or mark
   the candidate as a permanent reject (added to a deny-list the
   dedupe node consults).

3. **Tester finds the proposed cascade YAML references a primitive
   that doesn't exist.** This is the "agent hallucinated a feature"
   case. Reviewer's critique includes the conformance-test output
   verbatim; next scaffold attempt sees it as constraint.

4. **Two generator invocations propose the same candidate.** Per-slug
   branch lock serializes; second invocation either gets the same
   accepted output (cached, cheap) or sees a now-existing example and
   short-circuits to dedupe-rejection.

5. **The cascade proposes building a "Build a 'Build a' generator"
   recursion.** Dedupe node has a hard depth cap (configurable; default
   2). Recursion deeper than the cap is rejected by name as
   `infinite-recursion-cap-exceeded`. The cap is itself a content-
   addressed config so changing it is a deployable strategy decision.

6. **Source discovery returns garbage.** Extract-candidates produces no
   viable seeds; cascade terminates as no-op for the round; budget
   isn't burned on idea-generators. (This is enabled by the
   pre-fanout `extract-candidates` node — it's a cheap
   filter that prevents expensive nodes from running on bad inputs.)

7. **The catalog grows enough that dedupe gets slow.** Embedding
   computation is per-doc; the doc-indexer peer caches embeddings keyed
   by file content hash. Catalog growth is logarithmic in cost.

## Reference implementation outline

```
examples/
  example-generator/
    rimsky.yml
    templates/
      generator-cascade.yaml
      generator-strategies/
        v1-broad.yaml             # default strategy
        v2-domain-focused.yaml    # variant: bias toward enterprise problems
        v3-meta-only.yaml         # variant: only generates self-referential examples
    peers/
      source-discoverer/          # Go, http-node based
      extract-candidates/         # TS, claude-agent
      idea-generators/            # TS, claude-agent (one binary, four configs)
      docs-indexer/               # Go, reads docs/examples/, computes embeddings
      candidate-judge/            # TS, claude-agent with rubric prompt
      example-scaffolder/         # TS, claude-agent with structured-output schema
      cascade-tester/             # Go, validates YAML + runs rimsky-conformance
      adversarial-reviewer/       # TS, claude-agent
      pr-opener/                  # Go, octokit/gh
    fixtures/
      mock-search-results.json
      mock-existing-catalog/      # for offline testing
    LIMITATIONS.md
    README.md                     # quickstart + budget warnings
```

`mock-search-results.json` and `mock-existing-catalog/` let the
generator be exercised end-to-end without hitting live APIs or
spending real LLM tokens. Live mode flips a config switch.

## Limitations and responsible-use notes

- **Quality is gated by reviewer quality.** If the reviewer agent is
  lenient, the generator produces low-signal examples. The reference
  impl ships with a strict reviewer prompt and recommends the human
  catalog maintainer occasionally re-tunes it against examples that
  shipped vs. examples that should have been rejected.
- **Token budget is real.** Default config caps daily tokens; weekly
  cron schedule produces ~1-3 candidate examples per run; budget burn
  is bounded. Live mode without a budget cap can spend serious money.
- **The generator does not merge PRs.** It opens them. Human review
  remains gating. The cascade's `open-pr` is the terminal — review
  and merge are out-of-scope by design.
- **Self-referential cascades are entertaining but should be
  occasional.** A catalog dominated by "build a generator that
  generates generators" examples is less useful than a catalog with
  one such example and many concrete ones. The judge should learn this
  preference; if it doesn't, the deny-list mechanism is the brake.
- **The first example a deployed generator should produce is a
  better version of itself.** This is a load-bearing test of the
  recursive case and a real feature of the system. The bootstrap
  walkthrough (this file) is hand-written; the v2 walkthrough should
  be generator-produced. If it isn't, the generator isn't working.

## Why this is a good catalog fit

Three reasons beyond the recursion joke:

1. **Demonstrates patterns no other catalog example does.** Multi-
   agent fan-out, judge-of-agents, cascade-reads-its-own-output. Even
   the gas-town example doesn't have all three.
2. **Justifies the catalog's continued existence.** A catalog that
   grows itself is a working demonstration of rimsky's pitch. The
   example is the proof.
3. **It's funny.** Self-referential demos earn their place when they
   also do something real. This one does.

## Cross-references

- Underlying primitives: `../architecture.md`,
  `../specs/2026-05-04-foundation-contract.md`.
- Adjacent examples: `bug-fix-from-tickets.md` (the held-claim and
  reverse-cascade primitives this example uses), `build-a-gas-town.md`
  (the multi-agent fan-out and judge-of-agents patterns this example
  applies recursively).
- Strategic positioning: `../2026-05-02-rimsky-vs-landscape.md` §10.3
  (the "templates as contracts" framing this example operationalizes).
