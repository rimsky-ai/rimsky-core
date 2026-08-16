---
issue: three-service-paths-are-http-not-grpc
kind: audit
category: conflicting
artifacts:
  - decision:grpc-internal-protocols
status: verified
opened: 2026-08-16T09:10:06Z
---

# One decision forbids HTTP-JSON between services while two others choose it

The decision on internal protocols says every service-to-service protocol is gRPC, no exceptions. Three shipped paths are HTTP-JSON by design, each with its own decision: the supervisor's HTTP executor transport and the bundled executor's HTTP bridge (the bridge-preserved decision), and the executor-to-supervisor callback, keepalive and attribute-writeback routes (the async-callback decision, which rejects a gRPC callback in as many words). Someone choosing a transport for a new peer surface gets opposite instructions depending on which artifact they open first. The ruling decides how the general decision names its exceptions.

Nothing is broken in code — the nine declared peer protocols are gRPC and the three HTTP paths work as their own decisions describe. The defect is a corpus self-contradiction on the transport rule.

## Options

- Restate the gRPC decision as "gRPC for the declared peer protocols, with these HTTP-JSON surfaces as named exceptions", citing the two decisions that own them; cost: none beyond the edit.

The ruling decides the wording; the two exception-owning decisions already fix the intent.

## Ruling

> Generated ruling (/verify-issues): Rewrite the internal-protocols decision so its Choice is gRPC for the nine declared peer protocols, with the executor HTTP bridge (both sides) and the executor-to-supervisor callback routes named as the HTTP-JSON exceptions and cited to the decisions that chose them. The current-state-only rule and the corpus's own sibling decisions force it — a decision cannot forbid what two live decisions deliberately choose. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
