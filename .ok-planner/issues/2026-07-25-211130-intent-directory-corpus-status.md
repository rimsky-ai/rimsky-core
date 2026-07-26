---
issue: intent-directory-corpus-status
kind: audit
category: compliance
status: verified
opened: 2026-07-25T21:11:30Z
---

# design/intent/ is a finished campaign's dossier still sitting in the live corpus

`design/intent/` holds per-concept dossiers distilled on 2026-07-13 for a drift-remediation campaign, written deliberately without consulting the then-current code. That campaign completed and its results are archived, but the directory stayed under `design/` — where the planner recognizes exactly three live catalog kinds (concepts, stories, decisions) plus one named point-in-time exception. Its content has begun to rot: two dossiers still describe the graph-scheduler import exemption that the corpus has since eliminated. One live reference exists — a fitness test's error message cites `design/intent/claim-scope.md` as the naming-convention source — showing the misplacement actively misleads sessions into treating it as durable.

Because the planner's model doesn't admit a fourth live kind, the "declare it live corpus" reading isn't actually available; the frozen-record reading is forced. The only judgment left is how to handle the one load-bearing citation.

## Options

- Move `intent/` to `history/`, first folding the claim-scope naming rationale into the live claim concept and repointing the citing test. Cost: a small sprint pass.
- Leave it in place — continued rot and continued misleading citations.

## Ruling

> Generated ruling (/verify-issues): a sprint moves `design/intent/` to
> `history/`, after folding the claim-scope naming rationale its one citing test relies
> on into the live concept and repointing that test. The planner's three-kind model of
> `design/` forces the frozen-record reading.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
