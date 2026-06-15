# In-process loop_counter — no external executor required

This example demonstrates STORY-inproc-utility-executor: a template
references `kind: loop_counter`, deploys to a rimsky stack with **no
external executor service configured for loop_counter**, and dispatches
the node to terminal entirely in-process.

The `kind: loop_counter` field is the kind-sugar shorthand. At template
registration, rimsky resolves it to the rimsky-bundled inproc executor
alias via a static kind-alias map that the supervisor and the
control-API seed at startup. There is no operator-side configuration
for this — the alias is part of the rimsky binary.

## What you get

- `template.yml` — the minimal template, one node, `kind: loop_counter`,
  one input attribute `max` defaulted to 3. The counter self-subscribes
  on `event/loop` with `frame: next` so the cascade re-fires it within
  the same RunScope: dispatches 1 and 2 emit `event/loop` (count
  carries forward 0 → 1 → 2 on the incoming bag, writebacks
  count = 1 → 2); dispatch 3 emits `event/done` (count reaches max=3).
- A node referring to a utility kind that runs entirely inside the
  supervisor process. No HTTP-bridge sidecar. No gRPC executor pod. No
  matching entry in the operator's `executors:` block.

## How rimsky dispatches it

1. **Registration.** The control-API's `POST /v1/templates` route
   validates the template, resolves `kind: loop_counter` to its
   pre-registered executor alias, and persists the canonicalized
   spec.
2. **Instance creation.** Cohesive with any other template — `POST
   /v1/instances` materializes the node row.
3. **Dispatch.** The supervisor's dispatch loop claims the row, looks
   up the executor alias on its in-process resolver (seeded at
   startup with the rimsky-bundled inproc aliases), routes the
   request to the in-process registry's `loop_counter` handler, and
   streams the handler's events back through the same dispatch path
   the gRPC / HTTP-bridge executors use.

Result: the node terminates without any external service running.

## Running it

The example is exercised by the automated scenario test at
`test/scenarios/inproc_utility_executor_e2e_test.go`. The test boots
the in-process scenario harness, parses this YAML from disk, registers
it through the same `POST /v1/templates` HTTP route the production
control-API serves, creates an instance, and waits for the counter's
`done` event to surface on the events feed.

To run it under the standard test harness (Docker socket required for
the testcontainers postgres):

```
go test ./test/scenarios/... -count=1 -run InprocUtilityExecutor -v
```

## Why this matters

Utility nodes (counters, gates, simple computations) are
disproportionately expensive to ship as their own service: each one
is a deploy, an image, an IPC hop, an extra unit to monitor. The
in-process executor transport lets rimsky bundle a small library of
these primitives directly into the supervisor binary, where they
share the same protocol surface (`Execute` + the four StreamClose
outcomes) as the out-of-process executors but skip the deploy and
IPC overhead.

`loop_counter` is the first such utility. The shape generalises: any
inert, deterministic, short-running utility that benefits from
running close to the supervisor is a candidate.
