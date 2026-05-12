---
tension: claim-producer-method-count-framing
category: inconsistent
status: resolved
spec: 2026-05-11-design-log-convergence
affects:
  - claim-producer
resolution:
  shape: unify-on-4-verbs-plus-capabilities
  summary: |
    Unified claim-producer.md on "4 verbs (Open / Commit / Abandon /
    Release) plus the Capabilities() startup handshake" framing in
    both the "What it is" section (which previously listed
    Capabilities as a 5th method) and the Invariants block (which
    previously said "five-method protocol plus Capabilities()
    handshake," double-counting Capabilities). Matches CLAUDE.md
    framing.
---

# `claim-producer` is framed as "five-method protocol" and "4 verbs + Capabilities handshake" in adjacent sentences

## What is muddy

`concepts/claim-producer.md` Invariants section reads "five-method protocol plus `Capabilities()` startup handshake" and then lists 5 methods that *include* `Capabilities` — double-counting the handshake. CLAUDE.md frames it as "4 verbs + the `Capabilities()` startup handshake" (lifecycle: `Open / Commit / Abandon / Release`, plus the separate startup-handshake call).

The "4 verbs" framing is structurally accurate: `Capabilities` is a one-shot startup negotiation, the other four are per-claim lifecycle operations. The "5 methods" framing flattens these into one count.

## Why it matters

This is structurally the same shape as `handler-slot-count-drift` (4 handlers + `on_event` map vs. 5 slots). A reader builds two different mental models depending on which doc they read first. Worse: the concept file's own Invariants section contradicts itself in one paragraph.

## Resolution candidates (do NOT pick)

- **Unify on "4 verbs + Capabilities() startup handshake"** everywhere; reword `concepts/claim-producer.md` Invariants to match CLAUDE.md framing.
- **Unify on "5 RPC methods"** with a structural note that `Capabilities` is startup-only; reword CLAUDE.md to match.
- **Avoid the count** entirely; enumerate each method's role individually.

## Evidence

- `concepts/claim-producer.md` Invariants block.
- CLAUDE.md "What this repo is" / "Reference deployment".
- `protocols/proto/v1/claim_producer.proto`.
- `review-notes.md` "Possible merges / splits to reconsider" / "claim-producer five-method count drift" bullet.

