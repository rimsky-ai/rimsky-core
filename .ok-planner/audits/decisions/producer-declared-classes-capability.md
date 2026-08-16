---
audit: producer-declared-classes-capability
artifact: decision:producer-declared-classes-capability
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:34:05Z
---

# Claim producers may declare an error-class vocabulary, stored beside the executor vocabularies

Supported. The claim-producer capabilities message carries a declared-error-classes field of the same repeated-string shape the executor-observability capabilities message uses, and those two are the only capability surfaces in the protocol set that declare one. The handshake probes a producer for it as a separate call and writes the result into the same discovery-cache capabilities field an executor entry uses, so both vocabularies sit side by side in one cache under one shape; a producer that is otherwise unreachable still keeps its declared classes if the class probe succeeded. Declaring nothing stays legal in both directions: an empty result is simply not written, and a failed class probe is logged at information level and does not mark the producer unreachable or fail the handshake. The other half of the contract the rationale names is present too — the template validator carries a producer-declared-classes lookup wired from the discovery cache and range-checks producer-class policy keys against it at two sites.
