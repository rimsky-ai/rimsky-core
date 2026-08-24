---
decision: default-port-allocation
---

# Default port allocation

## Choice

Shipped default listening ports come from two reserved blocks. Ports 8080 through 8099 belong to rimsky's core listeners: the control API, the supervisor's async-callback listener, and the host-daemon proxy's daemon-facing and service-facing listeners. Ports 9000 through 9199 belong to the bundled services: every claim producer, sensor, subscriber, and executor default, gRPC and HTTP alike. No two shipped defaults share a number. A listener that carries no built-in default falls outside both blocks and takes its port from the operator; a metrics endpoint and a claim producer's admin listener are two such listeners. A fitness check enumerates every shipped default. It fails when a default falls outside its block, and it fails when two defaults coincide.

## Rationale

An operator starts the core stack and every bundled service at their defaults without a port conflict. The blocks keep that true as the project adds services: whoever adds the twelfth reads the rule instead of scanning the tree for a free number. The check reports a collision when the tree builds, rather than leaving an operator to meet it as a container that cannot bind. Two blocks keep each population's numbers contiguous, so an operator who reads a port number knows which side opened it.

## Alternatives

- Require only that no two shipped defaults coincide, with no blocks — rejected: it catches the next collision but gives no rule for picking the next default.
- Move the colliding defaults and add nothing — rejected: leaves the next collision to chance.
- Give every listener a default of zero and require the operator to set each port — rejected: destroys the zero-config bring-up the bundled images exist to provide.
- Let the host operating system pick every port and publish the choices — rejected: an operator cannot write a compose file or a firewall rule against a port that changes at each start.
