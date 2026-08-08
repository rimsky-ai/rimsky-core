---
issue: action-registry-descriptions-contradict-handlers
kind: human
category: inconsistent
artifacts:
  - concept:control-api
  - concept:permission
  - decision:node-reset-clears-failure-marker
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:53:18Z
github: https://github.com/rimsky-ai/rimsky-core/issues/95
---

# Six action-registry descriptions contradict their handlers

The control API keeps an action registry: one table mapping every route to an
action name and a one-line description of what it does. That table is not
decoration — it generates the tool catalog an AI agent reads to decide which
call to make, and it is the source the published REST reference is generated
from. A wrong description there is wrong in three places at once, and the agent
consuming it has no other source to check against.

Six descriptions say something the handler contradicts. All six were
re-verified line by line against the current tree.

| Action | Description says | Handler does |
|---|---|---|
| terminate an instance | "Terminate an instance" | refuses with 409 *unless already terminal*, then deletes the row and cleans up; the verb that terminates is the kill action |
| reset a node | resets a failed node "back to stale so it can be re-attempted" | clears one persisted marker on the failed run; no state change, no re-dispatch |
| read parked nodes | lists nodes "parked in the wait-set" | lists node-runs in the parked state; the wait-set is a different structure with its own route |
| read breakpoints | reads "pending" breakpoint hits | returns every hit since a cursor, resumed and unresumed alike, with no field distinguishing them |
| undeploy a template | "new instances rejected", implying existing ones run on | refuses with 409 while any active instance exists — the exact case the wording invites |
| rotate a key | mints a new plaintext with "same identity" | same *name*, permissions and expiry, but a **new key id** — and the key id is the principal |

The node-reset row is the sharpest, because the project already ruled on it. The
decision governing that behavior (`decision:node-reset-clears-failure-marker`)
explicitly considered and rejected the retry-budget framing the registry still
prints. The key-rotation row matters for the same reason from the other
direction: the key id is stamped as the actor on everything the key does and is
what the host-agent proxy routes by, so "same identity" is not a loose synonym —
it is the opposite of what happens. The concept doc for api keys already words
this correctly.

Two further descriptions in the same table are imprecise rather than false: the
kill action describes force-failing runs in claim vocabulary rather than run
vocabulary and omits that it also cancels pending messages and ends open frames;
the lineage action's "read lineage graphs" fits four of its eight routes, the
other four returning a single record or a filtered list.

## Ruling

> Generated ruling (/verify-issues): rewrite all six descriptions to state what
> the handler does — the terminate action as a terminal-only cleanup that refuses
> otherwise, the reset action as clearing the failure marker with no state
> transition and no re-dispatch, the parked read as listing parked node-runs
> rather than the wait-set, the breakpoint read as returning all hits since the
> cursor, the undeploy as refusing while any instance is active, and the rotation
> as preserving the name while issuing a new key id. Tighten the two imprecise
> ones in the same pass. The registry is the canonical route-to-purpose mapping
> and generates two downstream surfaces, so a description its own handler
> contradicts has exactly one compliant correction; for the reset action the
> wording is further forced by the standing decision that rejected the framing
> still printed. Rule this together with the sibling issue on omitted
> preconditions — same table, one edit pass.
> Verified against the tree as it stands; nothing was applied.
