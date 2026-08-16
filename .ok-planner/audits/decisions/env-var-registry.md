---
audit: env-var-registry
artifact: decision:env-var-registry
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:41:50Z
checked: 86
unaccounted: 0
---

# Whether every operator environment variable read by live code is registered, and endpoint variables name their service

Supported on both halves. The registry is a generated markdown table carrying 86 entries, each pairing a variable with a read site, and the fitness test enforces the correspondence in both directions: it re-scans the tree for read sites and fails naming the variable and its site when one is missing from the table, and fails again when the table lists a variable no live code reads, so the table cannot drift stale in either direction. The scanner's notion of live code is the shipped roots, excluding test files and test, fixture, and generated directories, and it matches whole quoted variable names — which covers this codebase completely, because every one of these variables appears somewhere as a whole string literal, either inline or as the right-hand side of a named constant; nothing constructs a variable name by concatenation, so the pattern has no blind spot here. One variable sits outside the scanned roots by design, the test-runner guard's no-progress interval in the development tooling directory, which is harness configuration rather than operator surface. The endpoint half holds everywhere in shipped code: the control API's endpoint variable names that service and is the one the CLI's run, template, auth, and compose verbs, the conformance runner, and the proxy all read; the host-agent proxy's endpoint variable names that service and is what the agent verb reads. No generic endpoint name is read anywhere in shipped code, and the retired generic alias has a purpose-built negative test asserting the peer-auth loader must not read it and that the service-named variable is the one name.
