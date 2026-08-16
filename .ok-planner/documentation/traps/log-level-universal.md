---
trap: log-level-universal
release: d977250c
demonstration: experiment:assumption-log-level-universal
---
## Assumption

As operator debugging a stack, I would take it that `RIMSKY_LOG_LEVEL` is honored by all eight binaries and all eleven bundled service images, with the same level vocabulary.

sibling-symmetry — a single un-namespaced `RIMSKY_LOG_LEVEL` in an env set where everything else is namespaced per service

## Actual behavior

Experiment `assumption-log-level-universal` (eleven checks, none failing) ran
every binary and image at this tree's tag that can be reached through the
public surface: five `rimsky-all-in-one` containers, one per level value; the
`rimsky-host-agent-proxy` image; the host agent through the public
`rimsky agent start --foreground` verb; and all eleven bundled service images.
Each reading waits on an external milestone — a completed observability refresh
visible on `GET /v1/observability/executors`, a port accepting a connection, a
postgres table appearing — never on a clock.

The prior does not hold, in two ways. First, the population: the core process
honors the variable (at `error` it silences every INFO and WARN line it
otherwise emits; at `debug` it adds DEBUG lines) and so does the proxy image,
but **none of the eleven bundled services does** — all ten with listeners had
logged their INFO startup line before they accepted a connection while told
`error`, and `subscriber-openlineage` logged `openlineage.starting` at INFO
under the same setting. The host agent started through `rimsky agent start`
ignores it too, and prints stdlib plain-text lines rather than the JSON records
the core roles emit, so the format is not uniform either. The standalone
`rimsky-host-agent` binary ships in no image and this run did not exercise it.

Second, the vocabulary: the accepted values are `debug`, `warn`, `error` and
nothing else. `DEBUG` and `trace` both fell back to the default level silently —
53 INFO lines, no DEBUG lines, and the offending value named nowhere in the log.

An operator who sets `RIMSKY_LOG_LEVEL=debug` across a stack gets more detail
from the core roles and the proxy, unchanged output from every bundled service,
and — if the value is capitalized or borrowed from another product's vocabulary
— nothing at all, without being told.
