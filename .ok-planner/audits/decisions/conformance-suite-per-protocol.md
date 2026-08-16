---
audit: conformance-suite-per-protocol
artifact: decision:conformance-suite-per-protocol
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:44:00Z
checked: 7
unaccounted: 0
---

# One suite per conformance target under the protocols module, each with its own CLI subcommand

Supported. The binary exposes seven conformance targets as subcommands beside a generic probe — executor, claim-producer, publisher, validation, data-processing, lifecycle-subscriber, and blob-backend — and every one of them runs a suite that lives under the protocols module's conformance package; nothing is inlined in the command layer and nothing spans protocols. Six of the seven have their own sub-package there, beside two shared helper packages for check reporting and stub-mode signalling. The rejected alternatives are both absent: there is no monolithic all-protocol suite, and the kit is packaged for external use rather than existing only as in-tree scenario tests, since each subcommand takes an arbitrary endpoint and transport.

## Remediation

- The lifecycle-subscriber suite is a single file inside the executor sub-package rather than a sub-package of its own, so an implementer certifying only lifecycle-subscriber still imports the executor suite's package — the packaging separation the rationale asks for is one file-move short.
