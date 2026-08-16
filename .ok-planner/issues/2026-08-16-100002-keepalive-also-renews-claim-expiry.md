---
issue: keepalive-also-renews-claim-expiry
kind: audit
category: conflicting
artifacts:
  - decision:keepalive-endpoint
status: verified
opened: 2026-08-16T10:00:02Z
---

# The keepalive decision promises one side effect; the handler has two

An executor calls the keepalive route to say a long dispatch is alive. The decision says the call has one side effect: bumping the dispatch's last-progress timestamp. The handler also renews the expiry of every claim the run holds, in the same transaction — a second mutation on a different table, and the one keepalive behaviour no test exercises (every keepalive test builds the server without a claim-handle table, so the renewal is never reached). A caller choosing a keepalive cadence is also choosing a claim-lease policy without being told. A sibling issue treats the renewal as intended and asks for a supervisor guard on it. The ruling corrects the text and adds coverage.

## Options

- State both effects in the Choice and add a keepalive test with a real claim-handle table asserting the renewal fires; cost: none beyond the test.
- Split the renewal out of keepalive; cost: contradicts the sibling issue's treatment of the renewal as design.

The ruling documents the second effect and covers it.

## Ruling

> Generated ruling (/verify-issues): Rewrite the Choice to name both persisted effects — the last-progress bump and the claim-expiry renewal for every claim the run holds — and add a keepalive test that exercises the renewal against a real claim-handle table. Forced by the current-state-only rule; the renewal is deliberate (the sibling guard issue rests on it). Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
