---
issue: cli-conformance-verbs-outside-capability-surfaces
kind: audit
category: conflicting
artifacts:
  - concept:rimsky
status: verified
opened: 2026-08-16T08:47:34Z
---

# The CLI's conformance verbs belong to none of the six capability surfaces the rimsky concept names

The rimsky concept describes the operator binary as six capability surfaces: dev-loop, compose, resource, context, authentication, and host-agent control. It states an invariant that the binary is an HTTP+JSON client with no protobuf. The conformance verb group fits none of the six and speaks protobuf. That group is eight subcommands. Each one dials a peer service over gRPC to prove it implements a protocol. The group ships as its own image. The concept explicitly declines to enumerate verb membership, so nothing in it settles whether conformance is a seventh surface, out of scope, or misplaced in this binary. The "no proto" invariant was already false at binary scope for the self-host modes the concept blesses.

## Options

- Add a seventh surface, protocol conformance, and scope the no-proto invariant to the control-API client; cost: the binary's identity widens.
- Declare conformance out of the concept's model and point at the artifact that owns it; cost: a shipped verb group with no home in the operator concept.
- Move the verb group out of the operator binary into the conformance image's own binary; cost: a binary split and a CLI change for implementers.

The ruling decides whether conformance is part of the operator binary's model.

## Ruling

> Recommended ruling (/verify-issues): Add a seventh surface for protocol conformance and narrow the no-proto invariant to the control-API client. The binary already ships the verbs and the conformance image already runs them.
>
> Rationale: the concept's coverage claim should match the binary as shipped, and the invariant's true subject was always the operator client, not every code path in the binary. Flip case: if the owner wants the operator binary free of protobuf dependencies for size or licensing reasons, the third option is the one that delivers it.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
