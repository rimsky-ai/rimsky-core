---
decision: host-agent-proxy-uniform-routing-identity
---

# One routing-identity slot serves owned and anonymous agents

## Choice

Every dispatch resolves its serving agent by the instance's stamped routing identity. That identity is an api-key id for an owner-instantiated instance and the assigned silly-name for an instance created in anonymous mode. Instance creation fills the one slot in both cases, and the proxy carries no separate anonymous routing rule (see `concept:host-agent-proxy`, `concept:anonymous-mode`).

## Rationale

Both cases answer one question: which connected agent serves this instance. Filling one slot keeps routing, displacement, and the agent-not-connected terminal identical in both modes, so anonymous mode inherits every routing property rather than restating it. A second path would reproduce each of those rules and could then drift from the first. The proxy assigns each anonymous agent a distinct silly-name so that the single slot names one agent.

## Alternatives

- Route anonymous dispatches by a separate rule keyed on the anonymous sentinel — rejected: every connected anonymous agent presents that same sentinel, so the rule cannot say which agent an instance meant, and every routing rule then exists twice.
