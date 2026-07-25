---
issue: peer-auth-concept-omits-incremental-callback-bearer
kind: human
category: docs-drift
artifacts:
  - concept:peer-auth
  - concept:executor
status: verified
opened: 2026-07-24T00:00:00Z
---

# The auth document a reader checks first implies a credential doesn't exist — it does

Rimsky's peer-auth concept document describes how the deployment's internal services trust each other's calls: under mutual TLS, peer identity alone authenticates a call and its return leg, and an older per-call token scheme is gone. All true — for the *terminal* callback, the one-time result delivery. What the document never mentions is a second, narrower credential that very much still exists: a per-dispatch bearer token guarding the two *ongoing* mid-dispatch callback channels (keepalive pings and incremental progress updates). A reader landing on peer-auth first concludes no token exists anywhere in the callback path — which is exactly the wrong conclusion a real bug report reached, misdiagnosing the live token check as a defect.

The facts aren't in dispute; the executor concept document states them correctly and completely (terminal callback: peer identity only; incremental channels: bearer token layered on top). The problem is purely that the document most readers consult first is misleadingly incomplete on the same topic — and the decision document backing peer-auth's claim carries the identical omission.

## Options

- **Restate the exception inside peer-auth** — self-contained for every reader, and duplicates the mechanism across two documents, which is how drift like this starts.
- **Add a cross-reference** to the executor document — no duplication; helps only readers who follow the link, not the skimmer the failure actually bit.
- **Rescope the bullet** — say explicitly that peer-identity-replaces-the-token covers the terminal return leg, with a pointer for the incremental channels' credential. Fixes the skim-level misreading without duplicating mechanism.

The ruling decides which fix, and whether the backing decision document gets the same one-line treatment.

## Ruling

> Recommended ruling (/recommend-rulings): Rescope the 'peer identity
> replaces the run-token' bullet in concept:peer-auth to state it
> covers the terminal return leg, with a cross-reference to
> concept:executor for the incremental-callback bearer carve-out.
> Apply the same carve-out note to decision:run-token-swept.
>
> Rationale: Rescoping fixes the misleading-by-omission failure at
> skim level without duplicating the mechanism prose that would drift
> again; both artifacts carry the same gap so both get the same one-
> line fix.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
recommended batch at sign-off. Edit the text to redirect, empty
the section to discuss live, or delete this note to adopt the
ruling as your own. -->
