---
assumption: compose-plan-previews-up
commit: PENDING
disposition: held
synthesized: 2026-08-16T05:48:16Z
---

# `rimsky compose plan` shows exactly the changes `rimsky compose up` would apply — a diff against the live stack, mutating nothing.

As operator running compose, I would take it that `rimsky compose plan` shows exactly the changes `rimsky compose up` would apply — a diff against the live stack, mutating nothing.

## Source

name-promise — `plan` beside `up`/`down`/`status` in a manifest-driven verb family, plus the terraform-style convention it invokes

## What a run would observe

change a manifest, run `compose plan`, apply with `compose up`, and compare the reported plan against what actually changed.

## Measured

`.ok-planner/experiments/assumption-compose-plan-previews-up` — built for this
run — drove one compose project through four manifest states against a live
`rimsky-all-in-one` from this tree's image set: a first apply, a no-op
re-apply, the instance entry removed, and the template entry removed. Each
round fingerprints the live world, runs `compose plan`, re-reads the world,
then runs `compose up` and compares the plan's change list against what `up`
reported applying.

The plan matched the apply in all four rounds, operation for operation and
object for object — register/tag-create/deploy/instance-create on the first
pass, nothing on the re-apply, one instance-delete on the third, and
undeploy/tag-delete/template-delete on the fourth — and the world fingerprint
was unchanged across every `plan`, so the preview mutates nothing.

The two renderings differ only in wording, not in content: `plan` truncates
the template hash that `up` prints in full, and `plan` says `tag-delete` where
`up` says `tag-rm` for the same operation. The comparison canonicalises both
so that a label difference is not read as a divergence. 8 checks, 8 pass.
