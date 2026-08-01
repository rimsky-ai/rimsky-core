---
issue: conflict-network-binding-loopback-vs-remote-reachability
kind: audit
category: conflicting
artifacts:
  - decision:network-binding
  - concept:host-agent-proxy
status: verified
opened: 2026-07-25T21:11:30Z
---

# The network-binding decision omits the override that every remote consumer of the control API relies on

The control API — rimsky's operator-facing HTTP surface — binds to loopback (`127.0.0.1`) by default, and `decision:network-binding` presents that as the posture, calling out only the *port* as configurable. But several artifacts require the control API to be reachable over a network: the host-agent proxy fails closed without a control-API endpoint it can dial from another container (`concept:host-agent-proxy`), and every standing service enrolls against the control plane under mutual TLS (`concept:peer-auth`).

There is no contradiction in the code — only in the corpus. The bind address is configurable (`env:RIMSKY_CONTROL_API_HOST`, consumed in `code:lib/control/launch/controlapi.go`), the shipped all-in-one image sets it to `0.0.0.0` precisely so other containers can reach it (`file:dockerfiles/Dockerfile.all-in-one`), and the split-topology test harness does the same — the wide bind is exercised, working infrastructure, not folklore. The decision simply never names the override, so the corpus reads as if loopback were the only sanctioned posture while the shipped images and the dependent concepts assume otherwise. Widening the decision's Choice is a claim-widening — an intent-level mutation only a sprint may make, which is the one reason this isn't a same-session repair.

## Options

- **Amend `decision:network-binding`** to name the bind-address override as the sanctioned split/production posture, loopback remaining the local-dev default, citing the all-in-one image's wide bind as the shipped example.
- **Scope the fix to the dependent concepts instead** — leaves the decision claiming a scope narrower than its own subject, against the self-containment expectation that the artifact making a choice states its scope.
- **Leave the decision silent** — reachability stays deployment folklore.

The ruling decides whether the decision acknowledges the mechanism its dependents rely on.

## Ruling

> Generated ruling (/verify-issues): amend decision:network-binding
> to document the configurable bind address as the sanctioned
> split-deployment posture — loopback remains the local-dev default —
> citing the all-in-one image's existing wide bind as the shipped
> example. The dependent concepts' reachability requirements force
> the decision to acknowledge the mechanism that satisfies them;
> only the claim-widening makes this sprint work rather than a
> repair.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
