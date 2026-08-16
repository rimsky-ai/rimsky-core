---
audit: launch-config-injection
artifact: decision:launch-config-injection
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:44:00Z
---

# Two synthetic YAML files written per run and discovered by the role runners through the standard surfaces

Supported. Both self-hosting paths write exactly two YAML files into the run directory before starting anything: a unified config matching the shape the standard loader parses, carrying the run's persistence driver, its state-database path, its blob root, and any merged service entries, and a separate supervisor-tuning file spliced from a baked default with the run's callback port. Both paths then set the two config-discovery environment variables to those file paths, alongside the topology marker and the control-API host and port, before the role stack starts; the supervisor role reads its tuning file from that variable and fails without it, and the unified config is read back through the same loader every deployed path uses. The files land beside the state database and the blob root inside the same run directory, so they travel with the artifact. No programmatic config-injection seam was added to the role-runner surface — the stack entry point takes the config value the standard loader produced, which is the same call shape the deployed entrypoint uses. Tests assert the generated paths, the merged service entries, the callback-port splice, and that the baked supervisor default round-trips.
