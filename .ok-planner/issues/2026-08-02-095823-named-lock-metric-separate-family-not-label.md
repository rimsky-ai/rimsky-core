---
issue: named-lock-metric-separate-family-not-label
kind: audit
category: decision-drift
artifacts:
  - decision:named-lock-metric
status: verified
opened: 2026-08-02T09:58:23Z
---

# Named-lock acquisitions got their own metric family — the exact alternative the decision rejects

Named locks (advisory locks templates take by name, as opposed to claims taken against a producer) are supposed to show up in metrics inside the existing claim-acquisition counter family, distinguished by a label — the decision says so, and explicitly rejects "a new dedicated metric family for named locks" (`decision:named-lock-metric`). The metrics registry does the rejected thing: it registers a separate counter family for named-lock acquisitions, with its own name and its own label schema, alongside the claim-acquisition family (`code:lib/control/observability/metrics.go`).

The practical stakes are wire-surface stakes: both metric names are scrapeable today, so whichever way this resolves changes what dashboards and alerts can rely on. The story that motivated the metric only requires named-lock visibility "alongside" claim acquisitions (`story:named-lock-metric`) — it is satisfied by either shape, which is why this is a decision-drift question rather than a broken promise.

The ruling decides which shape the corpus and code converge on.

## Options

- Fold named-lock counts into the claim-acquisition family under a distinguishing label and retire the separate family. Cost: a breaking metric rename for anyone scraping the current name (legal pre-v1), and the two populations must share a label vocabulary that currently differs.
- Amend the decision to bless the separate family. Cost: concedes the one-family rationale; the Alternatives section must be rewritten since the current text rejects exactly this.

## Ruling

> Recommended ruling (/verify-issues): fold it in — add a distinguishing label to the claim-acquisition family (a sentinel for named locks), retire the separate family, and update the story's cited assertions in the same change.
>
> Rationale: the decision's one-family rationale (one query surface for "what's acquiring," dashboards don't need to know the mechanism split) still holds, nothing in the code suggests the split was a considered reversal rather than convenience, and pre-v1 is the last cheap moment for a metric rename. Flip case: if a real dashboard or alert already depends on the dedicated family's name and label schema (evidence: an actual consumer, not a hypothesis), bless the split in the decision instead and rewrite its Alternatives honestly.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
