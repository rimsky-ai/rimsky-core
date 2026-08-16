---
issue: in-frame-sweep-cutoff-is-structural
kind: audit
category: conflicting
artifacts:
  - concept:orphan-reaper
status: verified
opened: 2026-08-16T08:47:39Z
---

# The in-frame orphan sweep reclaims claims by structure, not by the deadlines the concept names

The orphan reaper has two sweeps. The node-run sweep asks whether a dispatch has gone quiet past its configured deadlines. The in-frame sweep asks a different question — did this claim outlive the frame that authorized it — and reads no timestamp or deadline at all: it selects every claimed run whose frame has ended and releases all of them. The concept says both sweeps key on per-dispatch quiet-period and runtime deadlines. A run dispatched moments before its frame ended, well inside every deadline, is reclaimed anyway. The ruling decides whether that eager reclaim is the design or a latent defect, and fixes the concept to it.

## Options

- Rewrite the invariant to give the in-frame sweep its own structural cutoff (frame ended → claim released) and say why; cost: leaves the eagerness unexamined.
- Add deadline checks so an in-deadline claim survives an ended frame; cost: a behaviour change with its own questions (can a claim then be held past frame end on purpose?).
- Restate the invariant around what each cutoff protects — a stalled dispatch vs. an authorization that no longer exists — recording the absent timestamp as deliberate; cost: same behaviour, better words.

The ruling decides whether a fresh claim under an ended frame should be released.

## Ruling

> Recommended ruling (/verify-issues): Keep the structural cutoff and rewrite the concept to say each sweep guards a different thing — the node-run sweep a stalled dispatch, the in-frame sweep a claim whose authorizing frame is gone — so the absence of a timestamp on the second is stated as design.
>
> Rationale: a frame that has ended has settled its outcome; a claim still held under it is not work in flight but a lease with no owner, and holding it longer only delays reclaim. Flip case: if a settled frame can legitimately leave a child dispatch running that must finish (a fan-out straggler whose result still lands somewhere), the sweep is destroying live work and the second option is right.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
