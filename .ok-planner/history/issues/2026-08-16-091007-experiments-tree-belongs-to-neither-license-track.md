---
issue: experiments-tree-belongs-to-neither-license-track
kind: audit
category: unclear
artifacts:
  - decision:licensing-dual-apache-agpl
status: promoted
opened: 2026-08-16T09:10:07Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# The audit's experiments tree carries no license and the license checker rejects it

The project ships under two license tracks. A checker enforces them on every `make lint`: every tracked source file must sit under a mapped prefix and carry the matching header. The audit's maintained experiments sit under no mapped prefix and carry no header, so the checker exits non-zero on the whole tree. The planner estate held 79 tracked files before this run. This run added more. The dual-licensing decision says "everything else" gets the copyleft track, but glosses "everything else" as eight code groups. The module-layout concept places non-code top-level entries outside its grouping, the planner estate among them. The checker's walker descends into everything but version-control directories. The corpus and the tool therefore disagree on whether the estate belongs to the licensing population. The ruling settles that scope.

`make lint` is red at this tree because of the audit's own instruments, and every future audit adds experiments. Without a ruling the checker's verdict and the decision's text keep diverging.

## Options

- Exempt the planner estate in the licensing map, as records and instruments rather than shippable source; cost: experiment code a consumer might copy carries no license statement.
- Put the experiments tree on the copyleft track and stamp every file, keeping the stamp current as audits add probes; cost: header upkeep on a directory the audit rewrites every run.
- Restate the decision's "everything else" as its enumerated code groups and say what governs source outside them; cost: this alone does not reconcile the walker with the text.

The ruling decides whether the estate is licensed source or exempt records.

## Ruling

> Recommended ruling (/verify-issues): Exempt the planner estate from the licensing population. Declare it in the licensing map as records and instruments outside both tracks, and restate the decision's "everything else" as the enumerated code groups so the text and the walker agree.
>
> Rationale: the estate is a record with its own discipline. That discipline keeps it out of context. Ceremonies regenerate it. The module-layout concept already puts it outside the code grouping. Stamping headers onto files a ceremony rewrites every run is upkeep with no consumer. Flip case: if the owner intends nominated experiments to graduate into the test tree, they graduate under the test tree's own track at adoption, which this ruling allows. If instead the owner means to publish the experiments themselves as examples, they need a track and the second option wins.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
