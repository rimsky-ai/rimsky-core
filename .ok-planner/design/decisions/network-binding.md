---
decision: network-binding
status: adopted
---

# network-binding

## Choice

The control-api HTTP server binds to the loopback interface by default (see `concept:control-api`), and the bind address is itself configuration-overridable: a split or containerized deployment sets a wide bind so other containers and enrolled services can reach the control plane — the sanctioned split/production posture, which the shipped all-in-one image uses. Loopback remains the local-dev default. In normal single-role or split deployments the port is a fixed, configuration-overridable default; the one-shot self-host launcher (compose-run and equivalent single-invocation flows) instead binds a kernel-picked ephemeral port and retries on bind conflicts, since it may run concurrently with other local invocations.

The supervisor's async-callback HTTP listener binds to all interfaces, since executors dispatched from another container or host must be able to reach it. Because a wildcard bind carries no externally-reachable hostname on its own, the supervisor requires a configured callback advertise host to be stamped into the callback URL handed to executors, and fails fast at startup rather than silently advertising an unreachable address.

## Rationale

Loopback binding by default gives OS-level isolation against other users on the same host and needs no firewall rules to negotiate; the one-shot launcher's ephemeral port plus bind-conflict retry removes the port-conflict story between concurrent local invocations without imposing that cost on every deployment shape. The bind-address override is part of the Choice because the platform's own components depend on it: the host-agent proxy fails closed without a control-API endpoint it can dial from another container, and standing services enroll against the control plane over the network — the wide bind is the posture every split deployment already runs, so a decision that named only loopback misdescribed its own scope. The supervisor's callback listener binds wider than loopback out of necessity — it accepts inbound requests from executors that may not share a network namespace with it — so its correctness turns on an explicit, correct advertise host rather than on the bind address itself; failing fast on a missing advertise host beats silently handing executors a URL nothing can reach.

## Alternatives

- Loopback-only binding with an ingress or gateway layer providing remote reachability — rejected: no gateway exists anywhere in the codebase, and the proxy registration and enrollment surfaces need a directly dialable control plane.
- Hardcoded per-deployment-shape bind addresses — rejected: the same image serves single-role, split, and all-in-one shapes; only configuration can know which is running.
