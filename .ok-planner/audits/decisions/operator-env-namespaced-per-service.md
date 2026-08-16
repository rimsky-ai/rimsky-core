---
audit: operator-env-namespaced-per-service
artifact: decision:operator-env-namespaced-per-service
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T04:43:41Z
checked: 41
unaccounted: 2
---

# Whether every bundled-service operator env var carries a per-service prefix

Unsupported as a universal, by two members. The tree's generated environment-variable registry, itself pinned by a test, lists forty-three variables read from the bundled services, two of which are callback plumbing the handler writes into its CLI child rather than operator configuration, leaving forty-one operator variables to check. Seven are the generic per-executor set the decision explicitly exempts — the listen host, the two ports, the stub-mode switch, the binary override, and the two dispatch timeouts — and a pin test walks every Go file under the executors tree and fails any host or port variable outside that exempt set, so the unprefixed half of the choice is mechanically held. Thirty-two of the remaining thirty-four carry a per-service prefix: two on the claude-agent executor, four on the outbound-HTTP executor, two on the claim producers, sixteen across the four sensors, and eight on the subscriber. Two do not, and both are behaviour-specific knobs read only by the claude-agent executor, so neither falls in the exempt set and neither has a mechanical check. No test enforces the namespacing half of the choice on anything other than host and port names.

## Unaccounted

- The dispatch budget ceiling variable read by the claude-agent CLI runner as a fallback when node config sets no budget: it carries no service segment at all, not even the executor one.
- The observability HTTP bridge URL read by the claude-agent options loader: it wears the generic executor prefix although only that one executor reads it, while the outbound-HTTP executor's equivalent knob is properly namespaced.
