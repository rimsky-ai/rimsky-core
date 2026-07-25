---
issue: decision-release-chain-toc-omits-race-gate
kind: audit
category: inconsistent
artifacts:
  - decision:release-chain
  - decision:race-gate-split
status: answered
opened: 2026-07-25T03:18:31Z
---

# decisions.md release-chain summary omits the repeated race-detection gate the body includes

## Problem

TOC chain: lint → license lint → images → tests → scan → push; body inserts the dedicated race gate (decision:race-gate-split) between tests and scan.

## Candidates

- Regenerate the TOC line to include the race gate
- Amend the body if the gate left the chain

## Discussion

`decision:release-chain`'s own body is right, and the Makefile confirms it: "Lint → license lint → build the core images → build the bundled-service images → run the full test suite → run the dedicated repeated race-detection gate (see `decision:race-gate-split`) → scan the built images → push the images."

Code: `code:Makefile::release#433` is `release: lint core-images service-images test-all test-race scan push-images` — `test-race` sits between `test-all` and `scan`, exactly where the decision body places the race gate. `decision:race-gate-split`'s own body is consistent: "The release chain requires the full repeated-race gate."

`decisions.md`'s TOC one-liner for `release-chain` — "Lint → license lint → build the core images → build the bundled-service images → run the full test suite → scan the built images → push the images" — simply drops the race-gate step the decision's own body and the Makefile both carry. Since `decisions.md` is a generated index and not authoritative content, this is a mechanical drift, not a live design question.

Closing this issue as answered by `decision:release-chain`'s own body, cross-confirmed by `decision:race-gate-split` and by `code:Makefile::release#433`. A future sprint should regenerate the TOC line to insert the race-detection gate between the test suite and the image scan.
