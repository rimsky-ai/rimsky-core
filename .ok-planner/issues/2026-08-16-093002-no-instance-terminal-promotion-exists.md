---
issue: no-instance-terminal-promotion-exists
kind: audit
category: conflicting
artifacts:
  - decision:termination
status: verified
opened: 2026-08-16T09:30:02Z
---

# The termination decision leans on an instance-terminal promotion the platform does not have

The one-shot verbs run a manifest or template to terminal and clean up. The termination decision says they wait on the supervisor's existing instance-terminal promotion — the platform stamping an instance terminal when its work is done. No such path exists: the terminated stamp is written only by the administrative terminate route. The self-hosting one-shot verbs poll a client-side quiescence check (no running frames, no pending messages) and then terminate the instances themselves. The remote one-shot run trusts the decision: it polls the stamp nothing sets, so without an explicit timeout it never returns. The park half of the rationale holds. The ruling decides whether instance-terminal promotion becomes a platform primitive or the verbs' client-side gate becomes the documented design.

## Options

- Rewrite the decision to what the verbs do (poll quiescence, then self-terminate), define that quiescence in the terminal-resolution concept, and fix the remote path separately; cost: two termination strategies stay live unless the remote path is aligned.
- Build the promotion path — the platform stamps an instance terminal when it quiesces — so every verb waits on one real gate; cost: a platform feature with its own design (who decides quiesced, races with late messages).
- Keep the self-hosting gate and surgically fix the remote path (terminate itself, or bound its wait); cost: two strategies, documented.

The ruling decides whether instance terminality is a platform fact or a verb's act.

## Ruling

> Recommended ruling (/verify-issues): Make it a verb's act and say so — rewrite the decision around client-side quiescence followed by self-termination, define quiescence in the concept, and align the remote run onto the same gate (poll quiescence, terminate, exit) so no verb waits on a stamp nothing sets.
>
> Rationale: instances are meant to live until an operator or a manifest ends them; a platform that decides "done" on its own would need a definition the corpus does not have and would race with late messages by design. Flip case: if a story arrives for instances that end themselves (a one-shot template that should terminate without a driver), the platform promotion becomes the right primitive and the second option wins.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
