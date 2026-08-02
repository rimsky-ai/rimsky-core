---
audit: local-orchestrator-zero-config
artifact: story:local-orchestrator-zero-config
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:22Z
---

# `rimsky run <file>` drives a template to terminal with no standing infrastructure

Supported. Given a template file and no configured endpoint, `rimsky run` boots an in-process all-in-one role stack (no Docker, no separate compose stack), writes its own synthetic `rimsky.yml`/`supervisor.yml` so no operator-authored config file is required, and explicitly ignores any sibling `rimsky.yml` present in the working directory. A test drives a template naming the bundled in-process `http-node` executor through this self-hosted path to a terminal exit code of 0, exercising the real bundled-executor dispatch code (registered as an in-proc handler, not a stub of the CLI layer) rather than a canned result. The whole run is one binary and one command with no separately-launched infrastructure.
