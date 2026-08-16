---
audit: process-role-unified-message-covers-rimsky-run
artifact: decision:process-role-unified-message-covers-rimsky-run
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:26:42Z
---

# Whether the memory-blob gate's error text names the three deployment paths that set the unified marker, and omits the conformance runner

Supported. The blob-configuration validator's rejection message, raised when the memory backend is configured outside the unified topology, names exactly three setters in prose — the entrypoint's no-command all-in-one path, the compose run verb, and the ephemeral run verb in self-host mode — and names no fourth. Enumerating the setters from reality rather than from the decision: four sites in the tree write the process-role marker to the unified value — the entrypoint's no-command branch, the compose run verb, the template run verb (which is the `run` verb dispatched from the binary's argument switch, the self-host path), and the conformance runner's memory-blob-backend branch. The first three are exactly the three the message names; the fourth is the conformance runner, which the decision says is deliberately left unnamed, and it is. The gate itself is covered on both sides by a validator table test and by two loader and backend-open tests; no test pins the message wording, so the wording rests on the reading, which for a static format string is complete rather than sampled.
