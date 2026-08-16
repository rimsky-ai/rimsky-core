---
audit: network-binding
artifact: decision:network-binding
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:44:00Z
---

# Control-api and supervisor-callback bind postures, the one-shot's ephemeral port, and the advertise-host fail-fast

Supported. The control-api server defaults its bind host to loopback when none is configured and takes the host from an environment override, and the shipped all-in-one image sets that override to the wildcard address in its own Dockerfile — the split posture the decision names. Its port is a fixed default overridden by a second environment variable, with a non-numeric or non-positive value rejected as a startup error rather than silently defaulted. Both one-shot self-host paths take the other route: each asks the kernel for a free port before starting, and both go through the same start-with-bind-retry wrapper, which on an address-in-use error re-picks a fresh port, rewrites the endpoint and the port variable, and retries up to three times, returning any non-bind error immediately. The supervisor's callback listener defaults its bind to the wildcard address, reads its advertise host from an environment variable falling back to the YAML key, and refuses to start when the advertise host is empty while the bind is a wildcard, with an error naming the config key; a non-wildcard bind supplies the advertise host instead. A test drives that refusal and asserts the error names the key. Neither rejected alternative appears: there is no gateway or ingress layer in the tree, and no bind address is hardcoded per deployment shape.
