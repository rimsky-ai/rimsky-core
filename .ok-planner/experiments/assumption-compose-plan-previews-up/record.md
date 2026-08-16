---
experiment: assumption-compose-plan-previews-up
commit: d977250c
---

# `compose plan` against what `compose up` applies

## What it ran against

One `rimsky-all-in-one` container from this tree's image set and one compose
project, driven through four manifest states: a first apply, a no-op re-apply,
the instance entry removed once the instance is terminal, and the template
entry removed. In each round the run fingerprints the live world (templates,
tags, instances), runs `compose plan`, re-reads the world to confirm the plan
changed nothing, then runs `compose up` and compares the plan's change list
against the operations `up` reported applying.

The comparison canonicalises two cosmetic differences between the renderings
so they do not read as a divergence in what was planned: `plan` truncates a
template hash where `up` prints it in full, and `plan` calls one operation
`tag-delete` where `up` calls the same operation `tag-rm`.

## What was observed

The plan matched the apply in all four rounds, operation for operation and
object for object: 4 changes planned and 4 applied on the first pass
(register, tag-create, deploy, instance-create); nothing planned and nothing
applied on the re-apply; one instance-delete each on the third; and undeploy,
tag-delete, template-delete each on the fourth. The world fingerprint was
unchanged across every `compose plan`, so the preview mutates nothing.

4 rounds, 8 checks (a plan-purity check and a plan-equals-apply check each),
8 pass.
