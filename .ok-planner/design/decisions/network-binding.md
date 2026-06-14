---
decision: network-binding
status: adopted
---

# network-binding

## Choice

Control-api HTTP server binds to `127.0.0.1:0` (loopback only, kernel-picked ephemeral port).

## Rationale

OS-level isolation against other users on the same host; no port-conflict story to write between concurrent `compose run` invocations; no firewall rules to negotiate.
