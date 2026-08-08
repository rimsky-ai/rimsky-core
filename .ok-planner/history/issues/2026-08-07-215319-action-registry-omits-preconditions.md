---
issue: action-registry-omits-preconditions
kind: human
category: doc-drift
artifacts:
  - concept:control-api
  - concept:permission
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:53:19Z
github: https://github.com/rimsky-ai/rimsky-core/issues/96
---

# Three action descriptions omit the precondition that fails the first call

The control API's action registry maps each route to a description, and that
table generates the tool catalog an AI agent reads before calling. Three
descriptions are accurate as far as they go and omit the one thing that makes
the first attempt fail — so an agent that reads the catalog and calls
accordingly gets a 400.

- **Sending a message** requires an idempotency header. Absent, the handler
  refuses with 400. The description doesn't mention it.
- **Reading the wait-set** reads as an unqualified list. The handler requires a
  frame to be named in the query and refuses with 400 otherwise. This one is
  worse on the agent surface than in the docs: the tool's declared argument
  schema is an open object with no properties, so an agent calling it the
  obvious way — with no arguments — always fails.
- **Deleting an asset** releases the claim at its producer before dropping the
  row, and is refused with 409 while any holder is still active. The description
  mentions neither.

A fourth defect sits in the same surface and fails more quietly. The tool schema
for listing parked nodes advertises a `reason` argument that the handler never
reads — the filter behind it was retired. An agent passing it doesn't get an
error; it gets a full unfiltered list and no indication its filter was dropped.

All four were re-verified against the current tree.

## Ruling

> Generated ruling (/verify-issues): add the missing precondition to each of the
> three descriptions — the required idempotency header on message send, the
> required frame parameter on the wait-set read, and the producer-side release
> plus the active-holder refusal on asset delete — and give the wait-set tool a
> schema that declares the frame argument as required rather than accepting
> anything. Drop the retired `reason` argument from the parked-node tool schema:
> an advertised argument the handler ignores is worse than an absent one, because
> the caller gets a plausible answer to a question it didn't ask. The registry
> generates the agent-facing tool catalog, so a description that omits a hard
> precondition is a defect in a machine interface, not a doc nicety, and each
> correction is single-valued against verified handler behavior. Rule with the
> sibling issue on contradicted descriptions — same table, one edit pass.
> Verified against the tree as it stands; nothing was applied.
