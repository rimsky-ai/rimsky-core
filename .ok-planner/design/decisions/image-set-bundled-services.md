---
decision: image-set-bundled-services
status: as-is
---

# Bundled-service image set

## Choice

One image per bundled service (claim producers, sensors, subscribers, executors).

## Rationale

Each bundled service is a pre-packaged reference implementation; a deployment pulls only the services it runs.

## Alternatives

- A single combined bundled-services image — rejected: every deployment pulls every service to run one, and any one service's runtime needs bloat all of them.
- Ship bundled services as source only — rejected: forfeits the pull-and-run path the bundled set exists to provide.
