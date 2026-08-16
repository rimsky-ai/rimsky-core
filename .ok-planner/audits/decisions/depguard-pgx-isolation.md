---
audit: depguard-pgx-isolation
artifact: decision:depguard-pgx-isolation
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 19
unaccounted: 0
---

# Whether the Postgres driver is confined to the packages the choice enumerates

Supported. Enumerating from reality rather than from the config, 19 packages in the tree import the driver, and every one falls in a category the choice names: three in the foundation module's Postgres persistence, pooling, and Postgres test-helper packages, seven bundled services that keep durable state in Postgres (the Postgres claim producer's server and store, all four sensors, and the lineage subscriber), one in the binary group, and eight test-support, scenario-harness, and smoke packages. No graph, runtime, or control package imports it, so the persistence interfaces remain the only seam those layers see. The lint rule matches all files, negates the permitted trees, and denies the driver's three import paths with a message naming the interfaces; a fitness test fails if the denial disappears. One config-versus-text nuance that changes nothing today: the rule's negations exempt the claim-producer, sensor, and subscriber trees wholesale rather than only their Postgres-backed members, so a future non-Postgres service in those trees could import the driver without the lint objecting.
