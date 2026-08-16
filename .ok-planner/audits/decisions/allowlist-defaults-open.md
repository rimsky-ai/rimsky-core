---
audit: allowlist-defaults-open
artifact: decision:allowlist-defaults-open
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# Whether an absent operator allowlist env var leaves every template reference accepted

Unsupported. The decision speaks universally about a bundled service's policy allowlists, and the tree carries four allowlist-shaped operator env vars across the bundled services, of which only two behave as the decision describes. The claude-agent executor's two allowlists — one for declared MCP servers, one for exposed environment variable names — are exactly the decision's shape: absent means open and every template reference is accepted, a set-but-empty value is an explicit closed boundary, and a set value admits only the named entries; three unit tests in that service pin all three cases and a fourth pins the same behaviour through the real options loader. The other two — the outbound-HTTP executor's egress allowlist and the HTTP poll sensor's egress allowlist — have the opposite polarity: absent yields a guard that blocks loopback, private, link-local, unique-local, and multicast destinations at connect time, so a template that names a private-address URL is refused with no operator config present, and the env var is an opt-back-in exception list rather than an open boundary. The guard's own suite asserts that default-blocking behaviour directly. No other decision in the catalog records that opposite polarity, so as the corpus stands this decision is the only statement covering it and it says the wrong thing for half of the population.
