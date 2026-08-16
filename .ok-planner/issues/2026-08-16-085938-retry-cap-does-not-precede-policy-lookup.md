---
issue: retry-cap-does-not-precede-policy-lookup
kind: audit
category: conflicting
artifacts:
  - concept:terminal-resolution
status: verified
opened: 2026-08-16T08:59:38Z
---

# The terminal-resolution concept says the retry cap short-circuits before policy lookup; on the error path it does not

The terminal-resolution concept says the retry cap short-circuits before the error policy is looked up, and then — in the same breath — that a pass action bypasses the cap by exception. Those two sentences cannot both be true: a real short-circuit would settle before ever seeing a pass. The code resolves it: on the error and acquire path the policy is looked up first (with the acquire-prefix fallback) and the cap binds inside evaluation only when the resolved action is retry, so pass bypasses by ordinary precedence; on the infrastructure path there is no policy chain and the counter is compared to the cap first. The ruling makes the text say that.

## Options

- Rewrite the invariant per path — error path: lookup then cap-on-retry; infra path: cap first, no policy; cost: none.

The ruling fixes an editorial self-contradiction.

## Ruling

> Generated ruling (/verify-issues): Rewrite the invariant to state the two paths as they are — on the error path the policy is looked up first and the retry cap binds inside evaluation only for a retry action (which is why pass is unaffected); on the infrastructure path the cap is checked first and no policy runs — dropping the universal short-circuit sentence. Forced by the concept's own internal contradiction and the current-state-only rule. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
