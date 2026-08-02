---
issue: stories-bundled-sensor-collapse
kind: sprint
category: stories-collapses
artifacts:
  - story:sensor-cron
  - story:sensor-http
  - story:sensor-object-store
  - story:sensor-webhook
  - concept:sensor
status: promoted
sprint: 2026-08-01-ruled-intake-drain.md
opened: 2026-08-01T22:31:10Z
---

# Are four bundled-sensor stories one story per substrate — and where does the webhook ack contract live?

Four stories cover the bundled sensors (shipped services that watch an external source and inject messages into workflows): cron, HTTP polling, object-store deposits, and webhooks. Each reads "operator wires a <substrate>-driven message into a workflow," which looks like one outcome told four times — the pattern the story rules collapse. But re-verification shows the substrates differ in what they promise, not just where they run: cron is time-driven with a single-replica idempotency posture, HTTP polling adds change-detection, object-store is deposit-driven, and webhook is push-driven with mandatory per-subscription auth. The sensor concept (`concept:sensor`) itself lists "the per-substrate dialect" as content the concept owns — the corpus already treats substrate differences as substantive, unlike the fan-out list pair (`issue:stories-fanout-partition-collapse`) where only the backend differs.

Two of the four stories also carry prose past their sentence. The cron tail restates the replica posture the sensor concept already owns — deletable. The webhook tail commits to acknowledging the HTTP request only after the message is durably persisted — a real durability contract (the sender's retry logic depends on what a 200 means) stated nowhere else in the corpus.

## Options

- Collapse to one bundled-sensor story with the substrate list in a decision — loses per-substrate commitments (auth, ack semantics, durability posture) the concept itself treats as substantive.
- Keep the four stories, reduce their tails, and give the webhook ack-after-persist contract a durable home in the sensor's documented contract — one commitment moves, nothing collapses.

The ruling decides whether substrate is surface or substance here, and where the ack contract lives. Rule together with `issue:stories-fanout-partition-collapse`, `issue:stories-claim-producer-backend-collapse`, and the stray-commitment siblings (`issue:story-claude-agent-restructure`, `issue:story-bundled-park-resume-recipe-mechanism-home`).

## Ruling

> Recommended ruling (/verify-issues): keep the four per-substrate stories and reduce their tails to the canonical sentence; make ack-only-after-persist part of the webhook sensor's documented contract rather than a story aside. Substrate is substance here — each sensor promises different behavior, not the same behavior against a different backend.
>
> Rationale: the collapse test that forces the fan-out pair together fails here — these four differ in outcome (what a sender may rely on, what auth is mandatory, what a tick means), and the corpus's own sensor concept already claims per-substrate dialect as real content. The ack contract is a wire-visible durability promise external senders build retry logic on, so it belongs with the sensor's other invariants, not in story prose the format rules will eventually delete. Flip case: if the sensors converge on one uniform contract (same auth, same delivery guarantee, substrate as pure config), the collapse becomes the honest shape.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
