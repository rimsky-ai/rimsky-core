---
decision: host-agent-proxy-error-vocabulary-reuse
---

# The proxy reports failures in the platform's error vocabulary

## Choice

Every host-agent-proxy dispatch failure surfaces on the supervisor-facing protocol as an ordinary executor-error or claim-producer-unavailable terminal. The proxy adds no supervisor-side error class of its own (see `concept:host-agent-proxy`, `concept:error-policy`).

## Rationale

The proxy is a transparent forwarder, and a template must behave the same whether its service runs in the deployment or behind an agent (see `story:portable-template-across-modes`). An author declares error policy against the classes the platform already has. A proxy-specific class would leave every existing policy silent on the proxy path, so a failure an author handled would fall to the unknown-class default instead. The cost is that the terminal does not name the proxy as the origin. The failure detail carries that.

## Alternatives

- Add proxy-specific acquire and dispatch error classes — rejected: a template that handled a producer outage stops handling it the moment that producer moves behind the proxy.
