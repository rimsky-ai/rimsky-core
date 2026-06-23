---
concept: error-policy
status: as-is
aliases:
  - error-types policy chain
---

# Error policy

## What it is

A template-level error-routing surface that maps each error class to one of four runtime actions: `retry`, `give_up`, `pass`, or `release_and_requeue`. The runtime looks up the per-class action at terminal-error dispatch. The node-level `MaxRetries` cap bounds the total number of retries across a single dispatch (counted across all error classes that occur in that dispatch); when retries exhaust the cap, the runtime synthesizes a give-up settling the run as failed.

Error routing is the **single** decision surface for runtime error handling: every error variant arrives carrying an error class and is matched against the per-class action map. A reserved class-name namespace covers runtime acquisition failures (one synthetic class for unavailable claims, another for unclassified producer faults); operators wanting retry-on-acquire declare a policy keyed by the relevant class. A producer may name a more specific class on an acquisition failure (declared in its capabilities vocabulary); the policy lookup for acquisition failures falls back from the exact producer-declared class to the synthetic family class. Without any matching entry the lookup returns nil and the runtime gives up with an unknown-class reason — fail-fast is the default; retry is opt-in.

Error-class keys are range-checked at registration against the union of the declared vocabularies a key may legitimately come from: the node's executor's declared error classes, the runtime-synthesized classes (including the reserved acquisition-failure family), and the declared error classes of every claim producer reachable from the node's claims (producers advertise their vocabulary in the capabilities handshake; declaring nothing remains legal). A key attributable to no declared vocabulary registers as an advisory warning, never a hard rejection — the validator must accept whatever the runtime is able to route, and undeclared peer vocabularies must not lock operators out of their own routing.

## Purpose

Different errors warrant different responses. A declarative policy spares every executor from reinventing retry/cascade semantics, lets the platform uniformly bound runaway retry loops, and treats executor `Error{class}` and runtime acquisition failure under one chain.

## Boundaries

Owns: the closed action vocabulary, the per-class action lookup (covering both executor-emitted failures and acquisition failures), the per-dispatch retry-budget cap (`MaxRetries`).

Does NOT own:
- The signal type-path taxonomy (lives in `concept:signal`).
- Cascade firing (lives in `concept:cascade`).
- Terminal-resolution stitching from terminal event to producer verb (lives in `concept:terminal-resolution`).

Adjacent: `signal`, `frame`, `terminal-resolution`.

## Invariants

- The per-node `MaxRetries` cap on operator-side errors defaults to disabled (unbounded retries); an explicit positive value enables it. Infra-class errors apply an internal supervisor cap when no operator policy is declared for the class.
- The release-and-requeue action releases the run's held claim handles (firing each producer's abandon verb) and re-enqueues the run for a fresh acquire on the next dispatch; the plain retry action preserves claims and re-invokes the executor in place.
- The pass action settles the run with a fresh color (treating the error as benign for downstream cascade purposes). The per-class action map is a flat lookup; a class mapped to pass always passes — there is no per-attempt action-chain advance.
- The give-up action settles the run with a failed color.
- The retry action is in-place on the existing node-run row (see `decision:in-place-retry`): the runner sleeps the policy's retry delay and re-invokes the executor against the same dispatch context. Claims stay acquired, the persisted attribute bag stays unchanged, the dispatch id is preserved, and the node-run stays in state `running` across the loop. No new node-run row is created for retry; only the per-dispatch retry counter advances.
- The policy-evaluation cursor is a single per-dispatch retry counter, persisted on the node-run row (see `concept:node-run`). A new node-run for the same node starts at zero — the retry budget is per-dispatch, not cross-dispatch.
- The reserved acquisition-failure namespace lets operators declare policies keyed by an acquisition-failure class. The acquisition-failure policy lookup resolves in fallback order: the exact producer-declared class first (when the producer named one on the failure), then the synthetic family class for that failure kind (one for unavailable claims, another for unclassified producer faults). Without any matching entry the runtime gives up with an unknown-class reason (fail-fast; retry is opt-in). The fallback affects policy lookup only; the emitted signal carries the most specific class.
