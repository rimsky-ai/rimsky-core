---
audit: http-router-chi
artifact: decision:http-router-chi
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
---

# Whether chi on a pinned major line is the project's HTTP router

Supported as a library choice. Two module manifests — root and services — require the router on its v5 major line at one shared version, a manifest fitness test fails if the pin disappears, and no competing router library exists in any of the four manifests: searches for the common alternatives returned nothing. It is the router wherever routing composition is needed: 49 files import it, across the control layer's API, configuration, MCP, observability and launch packages, the runtime layer, the webhook sensor, and the CLI's test server. Nine production HTTP surfaces route with the standard-library multiplexer instead — the protocols module's conformance callback receiver, four claim-producer server and admin surfaces, two claude-agent internal surfaces, and the http-node entrypoint — each a fixed handful of paths with no grouping or middleware need, and the protocols one is obliged to, since that module's dependency budget excludes the router.
