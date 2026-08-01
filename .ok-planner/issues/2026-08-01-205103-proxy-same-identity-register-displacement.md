---
issue: proxy-same-identity-register-displacement
kind: human
category: unspecified
artifacts:
  - concept:host-agent-proxy
  - concept:host-agent
status: verified
opened: 2026-08-01T20:51:03Z
---

# Should the corpus state the same-identity registration takeover rule?

When a host agent (a machine-local worker that dials into rimsky) registers with the proxy (the rendezvous the platform routes dispatches through) under an identity that already has a live connection, the code resolves the conflict decisively: the latest registration wins, the prior connection is closed gracefully, and the newcomer's acknowledgment carries a displaced-prior flag it logs a warning from (`code:cmd/rimsky-host-agent-proxy/agent_server.go`, `code:lib/runtime/hostagent/run.go`). This is what makes agent restart safe — a restarting agent takes over from its own stale half-dead connection instead of being locked out by it.

The design corpus never states this rule. It states two negatives that presuppose it: an unauthenticated client "can neither displace a registered agent," and displacement between two anonymous agents is "impossible by construction" (`concept:host-agent-proxy`). A reader learns who *cannot* displace whom, but the affirmative behavior — latest wins, prior closed, takeover surfaced to the incoming agent — lives only in code and tests. If a future change flipped the policy to reject-new-keep-old, no corpus artifact would object, yet the restart story would silently break.

The question is placement, not behavior: nobody disputes what the code does. The choices are writing the rule as an invariant on the proxy concept (beside the negatives that presuppose it), recording it as a standalone decision (framing latest-wins as a chosen policy among alternatives), or ruling it below corpus altitude entirely.

## Options

- Add the affirmative rule as an invariant on the proxy concept. Cost: an intent-level concept amendment — sprint work.
- Record it as a decision (registration-conflict policy: latest-wins over reject-new), cited from the code. Cost: splits the displacement story across two artifact kinds — the negatives stay in the concept, the affirmative lives elsewhere.
- Leave it test-pinned only. Cost: the restart-takeover story has no design-level protection, and the concept's negative statements keep presupposing a rule the corpus never states.

The ruling decides whether — and where — the takeover rule becomes a corpus commitment.

## Ruling

> Recommended ruling (/verify-issues): State the rule as an invariant
> on the proxy concept: a new authenticated registration under an
> identity that already has a connected agent supersedes the prior
> connection, which is closed gracefully, and the new agent is told
> it displaced one.
>
> Rationale: the concept already carries both negative halves of the
> displacement story, so the affirmative half belongs beside them —
> the third option leaves the negatives dangling from an unstated
> premise, and the second scatters one story across two artifact
> kinds for no gain (latest-wins isn't a contested library-style
> choice needing an alternatives ledger; it's how the proxy behaves).
> Flip case: if the owner actually considers reject-new a live policy
> alternative — say, for deployments where a second registration
> under one key more likely signals a leaked credential than a
> restart — then it is a real choice and deserves the decision form
> instead.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
