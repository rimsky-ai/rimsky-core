---
issue: no-instance-terminal-promotion-exists
kind: audit
category: conflicting
artifacts:
  - decision:termination
status: promoted
sprint: 2026-08-21-intake-drain-and-concept-repair.md
opened: 2026-08-16T09:30:02Z
---

# The termination decision depends on an instance-terminal promotion the platform does not have

The platform has no instance-terminal promotion, and the termination decision depends on one. The one-shot verbs run a manifest or template to terminal and clean up. The termination decision says they wait on the supervisor's existing instance-terminal promotion, where the platform stamps an instance terminal when its work is done. No such path exists: only the administrative terminate route writes the terminated stamp. The self-hosting one-shot verbs poll a client-side quiescence check, then terminate the instances themselves. The check passes when no frames run and no messages are pending. The remote one-shot run trusts the decision and polls the stamp nothing sets, so without an explicit timeout it never returns. The park half of the rationale holds. The ruling decides whether instance-terminal promotion becomes a platform primitive or the verbs' client-side gate becomes the documented design.

## Options

- Rewrite the decision to what the verbs do, namely poll quiescence then self-terminate, define that quiescence in the terminal-resolution concept, and fix the remote path separately; cost: two termination strategies stay live unless the remote path is aligned.
- Build the promotion path, so the platform stamps an instance terminal when it quiesces and every verb waits on one real gate; cost: a platform feature with its own design, which must say who decides quiesced and how it races with late messages.
- Keep the self-hosting gate and fix the remote path narrowly, so it terminates the instances itself or bounds its wait; cost: two strategies, documented.

The ruling decides whether instance terminality is a platform fact or a verb's act.

## Ruling

> Recommended ruling (/verify-issues): Make it a verb's act and say so. Rewrite the decision around client-side quiescence followed by self-termination, define quiescence in the concept, and align the remote run onto the same gate: poll quiescence, terminate, exit. Then no verb waits on a stamp nothing sets.
>
> Rationale: instances are meant to live until an operator or a manifest ends them. A platform that decides "done" on its own would need a definition the corpus does not have, and it would race with late messages by design. Flip case: if a story arrives for instances that end themselves, such as a one-shot template that should terminate without a driver, the platform promotion becomes the right primitive and the second option wins.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
