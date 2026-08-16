---
audit: registry-hub-rimskyai-namespace
artifact: decision:registry-hub-rimskyai-namespace
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# Whether images publish under the hyphenless Hub namespace, distinct from the hyphenated organization

Supported. The build orchestrator declares one default registry value, the hyphenless namespace on Docker Hub, and every one of the 15 push invocations composes its tags from that single variable, so no image can land elsewhere. The distinction the choice draws holds in the tree: the hyphenated form is what the module paths and the npm scope use, and it appears in no image reference. A fitness test reads the default registry value and fails both if the hyphenless namespace disappears and if the hyphenated form creeps in.
