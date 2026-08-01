---
issue: coverage-gap-decisions-bulk-160-uncited
kind: audit
category: proof
artifacts:
  - design/decisions/*
status: verified
opened: 2026-07-24T00:00:00Z
---

# Two-thirds of the decision catalog has no annotation pointing at its enforcement

Rimsky's design corpus records technical decisions in a catalog, and code that enforces a decision is supposed to carry a citation comment (`@decision: <slug>`) at the enforcement site so the next reader can grep instead of re-deriving. Re-counting today finds 165 of 239 live decisions with no such citation anywhere in the tree. Under the current guidance annotations carry exactly one job — navigation — with no coverage floor: rollout is incremental, left by every session as it works, never a bulk pass. So bare non-coverage is no longer a rule violation. What makes the gap still worth ruling on is the audit corpus: the periodic implementation audit (`/verify-corpus`, not yet run here) navigates by exactly these annotations when it decides whether each decision is supported by the codebase. A decision that is enforced but untagged is the case most likely to come back falsely `unsupported` on the first run, generating intake noise about gaps that don't exist.

The 165 split three ways. A bucket is already enforced by an obvious mechanism that never got tagged — the dependency-boundary lint rules, the license checker, pinned library versions (these overlap with the config-file-enforcement question in `issue:build-file-enforced-decisions-uncitable`, since lint and manifest files can't carry code comments). A middle bucket probably has an enforcement site findable only by per-decision search. A tail has no single code site at all. The disposition this file carried previously — retire into the Proof-section sweep — is void: that sibling closed `answered` when the Proof requirement itself was retired, so nothing sequences this work anymore.

## Options

- **Tag the already-enforced bucket, leave the rest to incremental rollout** — one bounded pass over the decisions whose enforcement is known (landing the config-enforced ones via whatever mechanism `issue:build-file-enforced-decisions-uncitable` settles on); the middle and tail buckets accrete annotations as sessions touch them, per the incremental rule. Cost: the first audit run still mislabels some enforced-but-untagged decisions in the middle bucket.
- **Full 165-decision search-and-tag** — cleanest first audit; a multi-session project for a navigation aid the rules deliberately declined to mandate.
- **Do nothing** — legal under the no-coverage-floor rule; maximizes false `unsupported` findings on the first audit run.

The ruling decides how much tagging happens before the first `/verify-corpus` run.

## Ruling

> Recommended ruling (/verify-issues): tag the already-enforced bucket
> now — one bounded pass over the decisions whose enforcing mechanism
> is already known, with the config-enforced subset landing through
> whatever mechanism the build-file-citability ruling picks — and
> leave the rest to the incremental leave-the-annotation rule.
>
> Rationale: annotations are navigation with no coverage mandate, so
> a full sweep buys compliance nothing; but the first audit run reads
> annotations to find enforcement, and the known-enforced bucket is
> exactly where cheap tags prevent false unsupported findings. The
> flip case: if the first audit run proves better than expected at
> finding untagged enforcement on its own, the remaining buckets need
> no pass at all.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
