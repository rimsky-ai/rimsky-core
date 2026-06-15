---
decision: network-binding
status: adopted
---

# network-binding

## Choice

The control-api HTTP server binds to the loopback interface on a kernel-picked ephemeral port (see `concept:control-api`).

## Rationale

OS-level isolation against other users on the same host; no port-conflict story to write between concurrent compose-run invocations; no firewall rules to negotiate.
