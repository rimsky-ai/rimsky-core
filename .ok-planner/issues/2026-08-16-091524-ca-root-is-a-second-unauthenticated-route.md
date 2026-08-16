---
issue: ca-root-is-a-second-unauthenticated-route
kind: audit
category: conflicting
artifacts:
  - concept:control-api
status: verified
opened: 2026-08-16T09:15:24Z
---

# The control-API concept names one unauthenticated route; the registry has two

The control-API concept's posture invariant enumerates three cases: the health probe needs no token; the identity echo needs a token but no permission; everything else needs both. The action registry — which the same invariant names as the record of postures and of whether a route is mounted unconditionally or only when its dependency is configured — carries a second unauthenticated entry: the CA-root fetch, mounted only when the deployment runs a peer-auth CA, unauthenticated by necessity (a service must fetch the root before it can trust anything). The prose enumeration is stale by one route; the general-property sentence beside it is right. The ruling decides how concrete the invariant stays.

## Options

- Add the CA-root route to the enumeration, marked mounted-conditionally; cost: a list that can go stale again.
- Drop the enumeration and let the general-property sentence (the registry records every posture) stand alone; cost: less at-a-glance readability.

The ruling decides whether the invariant lists postures or defers to the registry.

## Ruling

> Recommended ruling (/verify-issues): Keep the enumeration and add the CA-root route as the second unauthenticated posture, marked as mounted only under a deployment CA — the invariant's value is that a reader can see the unauthenticated surface without opening the registry, and two entries is still readable.
>
> Rationale: the audit exists to catch exactly this drift, and a concrete list a reader can refute in seconds is what the corpus prefers to a pointer. Flip case: a third unauthenticated route would tip the balance — at that point the list should give way to the registry sentence.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
