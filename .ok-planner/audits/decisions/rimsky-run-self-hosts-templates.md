---
audit: rimsky-run-self-hosts-templates
artifact: decision:rimsky-run-self-hosts-templates
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# The ephemeral-run verb's endpoint discriminator, its self-host escape hatch, and the one-shot-only usage errors

Supported. The verb resolves an endpoint from its flag, then the environment, then the current CLI context, and treats the nothing-configured case as an empty endpoint; a non-empty endpoint routes to the remote dev-loop path unchanged, and an empty one falls through to self-hosting. The explicit self-host flag skips that resolution entirely, so a stale context endpoint is bypassed, and passing it together with an explicit endpoint is rejected as a usage error before anything starts. The self-host branch reads the template file, discovers the artifact root, creates the run directory, writes the synthetic config, boots the same role stack the compose one-shot boots, registers and deploys the template against the loopback control API, creates and wakes the instance, waits for terminal, and drains the stack through the shared shutdown coordinator, so nothing survives the process. Both options that presuppose survival are usage errors under self-host: the keep option is rejected with an error saying the stack exits with the process, and the named-template option is rejected because a self-hosted stack starts with an empty registry. Neither rejected alternative is present — no new top-level verb, and no default local endpoint is guessed.
