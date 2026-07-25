---
issue: conflict-concept-instance-vs-anonymous-routing-model
kind: audit
category: conflicting
artifacts:
  - concept:instance
  - concept:host-agent-proxy
  - concept:anonymous-mode
status: verified
opened: 2026-07-24T00:00:00Z
---

# One design doc still describes a routing model the system no longer uses

Three of rimsky's design documents disagree about how the system decides which connected agent should receive a workflow instance's dispatched work. The document describing instances still says routing keys off the creator's API-key linkage — and that instances created under anonymous mode (a fresh, credential-less deployment) therefore have no routing key at all. The two documents describing the routing proxy and anonymous mode say the opposite, correctly: every instance, anonymous or not, is stamped at creation with a routing identity (the credential's ID normally, a machine-generated name for anonymous agents), and routing resolves uniformly on that field. The recent work that built the uniform model updated those two documents but never touched the instance document, which now asserts a model the code has left behind.

The code and schema confirm it: an instance's stored row now carries *two* fields — the original credential linkage (still absent for anonymous instances, exactly as the stale document says, but now used only for ownership and audit) and the newer, always-present routing-identity field that dispatch routing actually uses. The stale document's specific claim — that the credential linkage "is the routing key" — is simply false today, and it never mentions the field that took the job.

## Options

- **Rewrite the invariant to describe both fields** — linkage for audit, routing identity for routing — bringing all three documents into agreement, at the cost of restating mechanics the routing-proxy document is supposed to own exclusively.
- **Minimal fix** — strike the false "it is the routing key" clause and point at the routing-proxy document for the mechanics, matching how this document already defers on things it doesn't own. Cheapest; a reader of this document alone won't learn the new field exists.
- **Additionally list the new field in the document's "owns" inventory** — a separate call; this corpus's ownership lists aren't exhaustive field inventories, so it may be unwarranted either way.

The ruling decides: full rewrite or minimal redirect, and whether the ownership list changes.

## Ruling

> Recommended ruling (/recommend-rulings): Minimal fix: rewrite
> concept:instance's invariant 28 to separate the creator's api-key
> linkage (ownership/audit, absent for anonymous) from routing — which
> points to concept:host-agent-proxy's target-routing-identity model
> rather than restating it. No Owns-list addition.
>
> Rationale: Point-elsewhere is concept:instance's own established
> pattern, and this corpus's Owns lists are not column inventories;
> the contradiction dies without duplicated prose that could drift
> again.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
