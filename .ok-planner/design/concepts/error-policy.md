---
concept: error-policy
status: as-is
aliases:
  - error-types policy chain
---

# Error policy

## What it is

A template-level error-routing surface that maps error classes to a closed vocabulary of runtime actions. The runtime resolves the error class through the policy chain at terminal-error dispatch. A no-progress cap forces a synthetic failure: every dispatch tracks consecutive retries without progress, and when the count exceeds the effective cap (per-node setting or supervisor default), the runtime forces a synthetic retry-loop failure.

Error routing is the **single** decision surface for runtime error handling: every error variant arrives carrying an error class and is dispatched through the policy chain. A reserved class-name namespace covers runtime acquisition failures (one synthetic class for unavailable claims, another for unclassified producer faults); operators wanting retry-on-acquire declare a policy keyed by the relevant class. A producer may name a more specific class on an acquisition failure (declared in its capabilities vocabulary); the policy lookup for acquisition failures then falls back exact producer class → reserved family → unknown-class default, so a template declaring only the generic family still catches classified failures. Without any matching declaration the chain falls through to a give-up action with an unknown-class reason — fail-fast is the default; retry is opt-in.

Error-class keys are range-checked at registration against the union of the declared vocabularies a key may legitimately come from: the node's executor's declared error classes, the runtime-synthesized classes (including the reserved acquisition-failure family), and the declared error classes of every claim producer reachable from the node's claims (producers advertise their vocabulary in the capabilities handshake; declaring nothing remains legal). A key attributable to no declared vocabulary registers as an advisory warning, never a hard rejection — the validator must accept whatever the runtime is able to route, and undeclared peer vocabularies must not lock operators out of their own routing.

## Purpose

Different errors warrant different responses. A declarative policy spares every executor from reinventing retry/cascade semantics, lets the platform uniformly bound runaway retry loops, and treats executor `Error{class}` and runtime acquisition failure under one chain.

## Boundaries

Owns: the closed action vocabulary, the per-class policy chain entry point (covering both executor-emitted failures and acquisition failures), the no-progress retry cap.

Does NOT own:
- The signal type-path taxonomy (lives in `concept:signal`).
- Cascade firing (lives in `concept:cascade`).
- Terminal-resolution stitching from terminal event to producer verb (lives in `concept:terminal-resolution`).

Adjacent: `signal` (signal-type changes reset the retry counter), `frame` (sibling observe-only mechanism that fires no-progress observations), `terminal-resolution`.

## Invariants

- The no-progress counter resets on any settling-signal-type change between consecutive retries (the retry-cap gate compares the most recent two terminals' canonical signal type-paths; identical signals across N retries trigger the cap).
- The per-node no-progress cap supports explicit disabling, a supervisor-default fallback, and an explicit positive bound.
- The discard-claims retry action releases held claim handles (firing each producer's abandon verb) before retry; the plain retry action preserves them by default.
- The pass action settles the run with a fresh color and advances the policy chain's action index so a subsequent same-class error in the same dispatch does not pass again.
- The give-up action settles the run with a failed color.
- The reserved acquisition-failure namespace lets operators declare policies keyed by an acquisition-failure class. The acquisition-failure policy lookup resolves in fallback order: the exact producer-declared class first (when the producer named one on the failure), then the synthetic family class for that failure kind (one for unavailable claims, another for unclassified producer faults), then the unknown-class default — a give-up action with an unknown-class reason (fail-fast; retry is opt-in). An exact-class entry always wins over the family entry. The fallback affects policy lookup only; the emitted signal carries the most specific class.
- A terminal-verdict metric records when the cap forces a failure, tagged with the synthetic no-progress class.
