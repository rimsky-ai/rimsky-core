# Build a Skillprompting Player

A reference walkthrough for an autonomous agent that participates in
[skillprompting.com](https://skillprompting.com) writing contests
end-to-end on Rimsky.

This is the catalog's "for giggles" entry. It's included because it's
genuinely fun and demonstrates the polyglot / reactive / held-claim
primitives in a non-enterprise context — not because Rimsky is uniquely
suited to it.

## Status

Concept walkthrough. Reference implementation TBD; the
bug-fix-from-tickets and gas-town examples land first.

## What skillprompting is, briefly

Per [skillprompting.com/llms.txt](https://skillprompting.com/llms.txt):

- Online writing contest platform judged by an AI judge.
- Each contest **round** posts a topic, character limits (`min_chars`,
  `max_chars`), entry fees, judging criteria.
- Round states transition `open → closing → completed`.
- Three entry paths:
  - **Paid:** signed Solana transaction transferring lamports to an
    ephemeral wallet.
  - **Puzzle:** argon2id proof-of-work; solving earns a free entry.
  - **AMOE:** physical postcard, explicitly allowed for "humans or
    agents."
- The judge sees only the prompt text and submission timestamp;
  wallet addresses are hidden during evaluation.
- Winners receive payouts in SOL from the prize pot.

Agent participation is anticipated by the platform (the AMOE
"humans or agents" language). This example is in good faith — it
doesn't try to game the platform, evade rate limits, or impersonate
humans. It just plays the game programmatically.

## Honest read on whether this needs Rimsky

Short answer: no, not uniquely. A Temporal workflow or a Python
script with a clean state machine would work. The platform's round
state machine is small enough that the orchestration complexity is
modest.

What this example *does* demonstrate that justifies the walkthrough:

1. **Polyglot in a whimsical context.** Most polyglot demos are
   enterprise-shaped. This one chains a Rust Solana signer, a
   tuned-in-C argon2id solver, a TS LLM submission generator, and a
   Go contest poller into a single cascade. The peer protocol's
   reach is more visible when the components are diverse.

2. **Reactive cascade on a real third-party state machine.** The
   `open → closing → completed` round lifecycle drives cascade
   invalidations naturally. A round closing while you're still
   generating a submission invalidates the submission node and
   short-circuits to a no-entry terminal. A round transitioning to
   `completed` invalidates the outcome-tracking node and triggers
   payout collection.

3. **Held claim for wallet lifecycle.** The ephemeral wallet
   exists across submission generation, transaction signing,
   submission, and confirmation. A claim producer (Solana wallet
   manager) opens the claim at strategy-decision time and resolves
   it at confirm-or-cancel. On commit: tx is on-chain, wallet is
   reusable for next round. On abandon: tx never broadcast (or is
   replaced by a no-op transfer back to home wallet); funds aren't
   leaked to a half-state ephemeral wallet.

4. **Per-round serial lock.** One submission per round per
   identity. The platform may or may not enforce this server-side;
   the lock makes it impossible at the player level regardless.

5. **Content-addressed strategy templates.** "I tried template
   `sha256-abc...` for haiku rounds; it had a 12% win rate. Template
   `sha256-def...` is the v2 with sharper rubric prompting." Each
   strategy is a deployable, comparable artifact rather than a
   prompt buried in a script.

That's the substantive demonstration value. It's a showcase, not a
justification.

## The cascade

```
contest-watcher ──→ evaluate-round ──→ generate-submission ──→ pick-entry-path ──┐
                          │                     │                                │
                          │                     ▼                                │
                          │              (round closing)                         │
                          │                     │                                │
                          │                     ▼                                │
                          └──→ skip-this-round (no-entry terminal)               │
                                                                                 │
                ┌────────────────────────────────────────────────────────────────┘
                │
                ▼
       ┌────────┴────────┐
       │                 │
   pay-path          puzzle-path
       │                 │
       ▼                 ▼
  sign-and-submit    solve-puzzle ──→ submit-with-puzzle
       │                                  │
       └─────────────┬────────────────────┘
                     ▼
              await-judging ──→ collect-outcome ──→ update-strategy-stats
```

- `contest-watcher` polls (or webhooks if available) the platform's
  round listing endpoint. Each new round fires a cascade instance.
- `evaluate-round` decides whether to enter. ROI estimate based on
  pot size, entry fee, character limits, and the topic's match against
  the player's strategy template repertoire. Output: `enter` or `skip`.
- `generate-submission` is a `claude-agent` invocation with the
  player's strategy template. Multi-turn: draft, self-critique against
  the round's stated judging criteria, refine, finalize.
- `pick-entry-path` is a small deterministic peer that compares
  entry-fee economics (Solana tx fee vs. expected pot share) against
  puzzle-solve cost (estimated argon2id time × electricity). Output:
  `pay` or `puzzle`. (AMOE is a stub for the demo; the postcard
  executor returns `not_implemented` with a friendly message.)
- `sign-and-submit` (paid path): the Solana wallet manager peer
  drafts the transaction, signs it, broadcasts it, watches for
  confirmation.
- `solve-puzzle` (puzzle path): the argon2id solver peer chews on the
  proof-of-work; returns the solution.
- `submit-with-puzzle`: posts the submission with the puzzle solution
  attached.
- `await-judging`: the round's `closing → completed` transition fires
  this node from async-wait to active. Outcome (win / loss / placement)
  is the node's commit payload.
- `collect-outcome`: if won, claim the payout from the prize pot. If
  lost, no-op.
- `update-strategy-stats`: append outcome to the strategy template's
  performance log. Future rounds can use this to inform `evaluate-round`.

## Reverse-cascade edges

Two of them, both actually useful:

1. **Round closes mid-generation.** `contest-watcher` receives the
   `closing` event; policy fires `invalidate(['evaluate-round'])` which
   re-evaluates and transitions to `skip-this-round`. The
   in-flight `generate-submission` is preempted because its parent
   went stale.

2. **Strategy underperforming.** After N losses with a given strategy
   template, `update-strategy-stats` can fire
   `invalidate(['evaluate-round'])` on the *next* matching round with
   reason `strategy_below_threshold` — forcing a strategy switch (or
   a skip) rather than continuing to enter losing rounds. This is
   reverse-cascade across cascade *instances*, sharing strategy state
   via the player's persistent store.

## Primitives exercised

### Per-round serial lock

```yaml
locks:
  - kind: scope
    scope:
      round_id: "${round.id}"
      identity: "${player.identity}"
    mode: serial
    held_by: [evaluate-round, generate-submission, pick-entry-path,
              sign-and-submit, solve-puzzle, submit-with-puzzle]
```

One submission per (round, identity). Idempotent against duplicate
contest-watcher events.

### Held claim around the wallet

The ephemeral Solana wallet is a held resource from
`pick-entry-path` (when the wallet is allocated) through
`sign-and-submit` (when the tx is on-chain) — auto-terminal on the
wallet claim:

- `Commit`: tx confirmed, wallet entry recorded in the player's
  wallet ledger as "spent for round X, refundable if round refunds."
- `Abandon`: tx never broadcast; wallet is returned to the pool of
  unused ephemerals or burned (cheap on Solana).

The Solana wallet manager peer is a small claim producer that owns
wallet allocation and tx broadcast. Rimsky doesn't try to know
anything about Solana — the producer's `Commit` and `Abandon` verbs
encapsulate the chain interactions.

### Polyglot peers

| Peer | Language | Why |
|---|---|---|
| `contest-watcher` | Go (http-node) | Standard HTTP polling, no heavy lifting. |
| `evaluate-round` | Go or TS | Small deterministic logic; either works. |
| `generate-submission` | TS (claude-agent) | LLM peer is already TS. |
| `pick-entry-path` | Go | Deterministic economics calc. |
| `sign-and-submit` | Rust | Solana SDK is best-in-class in Rust; wraps `solana-sdk`. |
| `solve-puzzle` | C or Rust | argon2id is CPU-bound; tuning matters. |
| `await-judging` | Go (http-node) | Long-poll or webhook receiver. |
| `collect-outcome` | Rust | Same wallet manager peer; payout is also a Solana tx. |
| `update-strategy-stats` | Go or TS | Just writes to a small Postgres table. |
| `submit-amoe` | (stub) | Returns `not_implemented`. The postcard-mailer-as-rimsky-executor is the joke; the stub demonstrates the protocol shape without committing to actually mailing postcards. |

This is the most languages of any catalog example. The point is to
show that a polyglot agent fleet isn't an enterprise burden — it's
just "register a peer that speaks the protocol."

### Content-addressed strategy templates

A "strategy" is a Rimsky template hash. Each strategy bundles:

- The `evaluate-round` criteria (which round types this strategy
  enters).
- The `generate-submission` prompt for the agent.
- Hyperparameters (target character count, refinement loop depth,
  etc.).

A strategy library is a directory of templates with movable tags
(`strategy:haiku:latest`, `strategy:essay:v3`, `strategy:limerick:v1`).
Comparing strategies is a SQL query over `update-strategy-stats`'s
output keyed by template hash.

## What the do-it-without-Rimsky baseline looks like

A representative Python implementation:

```
- Cron job polls the contest API every minute.
- A state machine in code (or Temporal workflow) handles the round
  lifecycle.
- `solana-py` for transactions; `argon2-cffi` for puzzles.
- The LLM submission is a synchronous OpenAI/Anthropic API call.
- Strategy stats live in a small SQLite database.
- Per-round dedup is a Postgres UNIQUE constraint or a Redis SETNX.
- Wallet management is custom code; refund/cancel paths are bug-prone.
```

This works. It's even cleaner than the Rimsky version for a single
strategy. The Rimsky version pulls ahead when:

- You want to run multiple strategies as parallel cascade instances
  that share global state (wallet pool, total budget, win statistics).
- You want to swap puzzle-solver implementations without touching the
  rest of the code (peer protocol).
- You want the strategy templates to be deployable, addressable
  artifacts with their own performance histories.
- You want the round-closing-mid-generation reverse cascade rather
  than a custom watchdog.

If you're building one player with one strategy, write the Python
script. If you're building a strategy R&D platform with cross-strategy
analytics, the Rimsky shape becomes more appealing.

## Failure modes the example deliberately exhibits

1. **Round closes while submission is being generated.** Reverse
   cascade preempts; skip-this-round terminal.
2. **Solana RPC is down during `sign-and-submit`.** `infra_failure`
   error class with `retry` action; held wallet claim survives across
   retries; abandons if retry budget exhausts.
3. **Puzzle solve takes longer than round window.** Solver peer
   advertises an estimated time; `pick-entry-path` checks against
   round-closing timestamp; routes to `pay` instead.
4. **Two strategy templates target the same round.** Per-(round,
   identity) lock serializes; second strategy waits, then sees the
   round is already submitted and terminals as no-op.
5. **Player runs out of SOL in home wallet.** `evaluate-round` checks
   wallet balance against expected entry fee + safety margin; routes
   to `skip-this-round` if insufficient. Strategy stats record the
   skip with reason.
6. **Judging is slow.** `await-judging` is async; the cascade holds
   no resources besides the strategy stats reservation. Player can run
   many concurrent rounds awaiting judgment.
7. **Player wins a round.** `collect-outcome` triggers a Solana
   payout claim from the prize pot to the home wallet; updates
   strategy stats.
8. **The platform changes its API.** `contest-watcher` peer fails
   with `infra_failure`; the cascade halts at the watcher; player
   author updates the peer (independent of the rest of the system)
   and redeploys. Other peers unaffected.

## Reference implementation outline

```
examples/
  build-a-skillprompting-player/
    rimsky.yml
    templates/
      player-cascade.yaml
      strategies/
        haiku-v1.yaml
        essay-v3.yaml
        limerick-v1.yaml
    peers/
      contest-watcher/             # Go, http-node-based
      evaluate-round/              # Go
      pick-entry-path/             # Go
      generate-submission/         # TS, claude-agent
      sign-and-submit/             # Rust, Solana SDK
      solve-puzzle/                # C or Rust, argon2id-tuned
      submit-amoe/                 # stub, any language
      collect-outcome/             # Rust, same as sign-and-submit
      update-strategy-stats/       # Go
      wallet-claim-producer/       # Rust, owns ephemeral wallet lifecycle
    fixtures/
      mock-rounds.json             # for offline testing
      mock-judge.py                # local AI judge for end-to-end test
    LIMITATIONS.md
    README.md                      # quickstart + budget warnings
```

Local-test mode runs against the `mock-rounds.json` fixture and the
`mock-judge.py` so the player can be exercised end-to-end without
spending real SOL or hitting the live platform. Live mode flips a
config switch and requires a funded home wallet.

## Limitations and responsible-use notes

To live in `LIMITATIONS.md` of the reference implementation, but
worth stating up front:

- **Real money.** Solana transaction fees are real; entry fees are
  real; losing rounds is a real cost. The reference impl ships with
  a daily-spend cap as a `named_lock` (counting mode, limit = budget
  in lamports) and the `evaluate-round` node refuses to enter when
  the cap would be exceeded. Don't disable this without thinking
  about it.
- **Platform ToS.** Read [skillprompting.com](https://skillprompting.com)
  's terms before deploying. Agent entry appears anticipated (the
  AMOE "humans or agents" language) but verify directly.
- **No win guarantees.** This is a writing contest judged by an AI
  judge; LLM output quality is variable; small character limits
  reward judgment that LLMs may not reliably exhibit. The example
  is fun, not a printing press.
- **AMOE postcard executor is a stub.** The protocol affordance is
  the joke; actually mailing postcards is out of scope.
- **The argon2id solver advertises real CPU time.** Don't run this
  on a metered cloud host without expecting a bill.
- **Strategy template performance varies.** The reference impl ships
  three sample strategies (haiku, essay, limerick) for demonstration
  only. They are not winning strategies.

## Cross-references

- Underlying primitives: `../architecture.md`,
  `../specs/2026-05-04-foundation-contract.md`.
- Adjacent examples: `bug-fix-from-tickets.md` (the headline single-
  agent shape), `build-a-gas-town.md` (the multi-agent shape).
- The platform: [skillprompting.com](https://skillprompting.com),
  [llms.txt](https://skillprompting.com/llms.txt).
