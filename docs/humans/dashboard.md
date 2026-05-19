# Dashboard UI guide

The bundled `dashboards/rimsky-dashboard/` is a read-only reference UI for observing Rimsky deployments. It composes Rimsky's three observability protocols (orchestrator, executors, stores) into a single coherent view.

## What it is

A read-only browser UI for "what is the system doing right now" and "why did this run fail." The dashboard is officially maintained but architecturally separate from Rimsky core — same isolation as the bundled executors and claim producers.

What the dashboard is *not*:

- Not a write-action UI in v1. No force-fire, invalidate, register, deploy. Those endpoints exist on the control-api; the dashboard does not call them.
- Not a replacement for operational tooling (logs/metrics/traces backends like Grafana, Datadog). The dashboard composes the three Rimsky-collection observability surfaces; OTel forwarding remains the operator's choice.
- Not auth-mediated in v1. Inherits the per-project deployment / network-perimeter model from the rest of the v1 stack.

## Launching

### docker-compose (development)

The dashboard ships under the `dashboard` profile in the bundled compose stack:

```sh
docker compose -f deploy/docker-compose.yml --profile dashboard up
```

Renders at `http://localhost:8090` by default.

### Standalone container

For production-style deployments where the dashboard runs separately from the rest of the stack, run the dashboard image and point it at the rimsky control-api endpoint via the dashboard's own config.

## Screens

### Instance list

The default landing screen. Lists every instance Rimsky knows about, with current frame state, node-state breakdown, and last activity timestamp. Click an instance to drill in.

Common diagnostic patterns:

- An instance with a long-running `frame_state: running` and many `stale` nodes — the cascade is working through the dependency graph; check whether progress is being made by watching the `running` count over time.
- An instance with `failed` nodes and a settled frame — the run is stuck pending the operator's response. Check the failed nodes' resolved error actions; nodes resolved to `give_up` won't auto-recover (those resolved to `retry`, `discard_then_retry`, or `resume_then_retry` will, as will any receiver that subscribed to the failure via a `subscribes: [{node, on: state, when: failed, error_class}]` entry).

<!-- @source: concepts/instance.md -->
> A running execution of a template, identified by a Rimsky-generated UUID. Instances bind to a specific template content hash at creation. An optional `instance_key` is a caller-supplied dedup key. Tag movement does not migrate live instances.

### Node graph view

Drill-down into one instance. Shows the subscription graph with current node states overlaid. Each node renders with its state, its subscriptions, and (where applicable) its claim/lock declarations.

Common diagnostic patterns:

- A `running` node downstream of `fresh` nodes — work is progressing.
- A `stale` node with `running` ancestors — the node is gated waiting for upstream values.
- A `stale` node with no `running` ancestors and no claim contention — likely a scheduler-tick or capacity-lock issue. Check the executor capacity and claim/lock acquisition events.

<!-- @source: concepts/node-state.md -->
> The five named runtime states a node can occupy: `fresh`, `stale`, `running`, `failed`, `parked`. The state-machine vocabulary covers every legal combination of "do we have a value?" and "is work pending?" plus the `failed` distinction (work attempted, no value, no auto-recovery scheduled) and the `parked` distinction (non-terminal hold awaiting time-based wake or external invalidate).

### Frame timeline

Per-instance timeline of frames. Each frame is a horizontal bar; the bar's color indicates outcome. Hovering shows the trigger (operator, schedule, executor cascade) and the cascade reach.

<!-- @source: concepts/frame.md -->
> The unit of cascade resolution. A frame begins when a node receives an invalidate and ends when no node remains in `stale` or `running` for the instance. The template's `frame_resolution:` field decides how concurrent invalidates are handled — `serial_queue` (each invalidate produces its own frame; frames run one at a time) or `coalesce` (new invalidates merge into a single pending row).

Common diagnostic patterns:

- Frame durations growing over time — symptomatic of capacity pressure or upstream bottlenecks. Cross-reference with the claim-handle inspector.
- Frequent overlapping frames in `serial_queue` mode — high invalidate volume; consider whether `coalesce` mode would fit the workload.

### Claim-handle inspector

Lists current claim handles per producer and per instance. Each handle row shows holder, scope (rendered hex-ish for opaque bytes), realized write semantics, and held vs. active status.

<!-- @source: concepts/claim-handle.md -->
> The persistent row asserting "holder H has acquired scope S for purpose P." Implementation of an acquired claim. Carries the rimsky-generated `claim_id`, holder identity, scope bytes, producer-returned address and payload, the realized write semantics, and a held flag.

Common diagnostic patterns:

- A held claim with all subgraph members in `failed` state — Rimsky's automatic resolution will fire `Abandon` shortly; the producer cleans up its own state.
- A long-lived claim with no apparent holder activity — heartbeat may have stalled; cross-reference the supervisor/executor heartbeat events.
- A claim whose scope bytes look unusual — the producer's canonicalization is suspect; check the producer's logs for selector parsing.

## What it does NOT do (v1)

No write actions. The dashboard does not call any control-api endpoint that mutates state.

For write operations, use:

- **The control-api directly** — `POST /nodes/{id}/invalidate`, `POST /admin/scheduled-nodes/{node_id}/force-fire`, `POST /templates`, `POST /instances`, etc.
- **`rimsky`** — the thin client that wraps the control-api for human use.
- **`rimsky compose up`** — for declarative reconciliation of templates, tags, and instances against a manifest.

## See also

- [`landing.md`](landing.md)
- [`concepts.md`](concepts.md)
