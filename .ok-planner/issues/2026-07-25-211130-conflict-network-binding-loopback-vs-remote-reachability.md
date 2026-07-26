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

The control API — rimsky's operator-facing HTTP surface — binds to loopback (`127.0.0.1`) by default, and the network-binding decision presents that as the posture for "normal single-role or split deployments." But several other artifacts require the control API to be reachable over a network: the host-agent proxy fails closed without a control-API endpoint it can dial from another container, every standing service enrolls against the control plane under mTLS, and the health-check story promises probes work behind a load balancer.

There is no real contradiction in the code: the bind address is configurable via an environment variable, and the shipped all-in-one image already sets it to `0.0.0.0` (`file:dockerfiles/Dockerfile.all-in-one`) precisely so other containers can reach it. The decision simply never mentions the override, so the corpus reads as if loopback were the only sanctioned posture while the shipped images and the dependent concepts assume otherwise. No ingress or gateway layer exists anywhere in the codebase to point at instead.

## Options

- Amend `decision:network-binding` to name the bind-address override as the split/production posture, with loopback as the local-dev default — matches what ships. Cost: sprint work only.
- Keep the decision silent and treat reachability as deployment folklore — leaves the contradiction standing.

## Ruling

> Generated ruling (/verify-issues): amend `decision:network-binding` to document
> the configurable bind address as the sanctioned split-deployment posture (loopback
> remains the local default), citing the all-in-one image's existing wide bind as the
> shipped example. The dependent concepts' reachability requirements force the
> decision to acknowledge the mechanism that satisfies them.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
