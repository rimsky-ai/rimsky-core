---
audit: testcontainers-go
artifact: decision:testcontainers-go
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
---

# Whether integration tests boot real backend containers from inside the test process

Supported. Three of the four module manifests require the container helper at one identical version — two of them also its Postgres module — and a manifest fitness test fails if the pin disappears. Seven packages use it, and the shape is the one the choice describes: the test process itself starts and stops the containers, from the Postgres fixtures behind the persistence conformance suite to the services harness that boots a whole rimsky stack. Nothing stands in for a real engine: no mock or fake persistence backend and no in-memory SQL engine appears in place of the shipped ones, and no compose file or externally provisioned database is required for a local run.
