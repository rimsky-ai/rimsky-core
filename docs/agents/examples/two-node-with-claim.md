# Two-node template with a claim dependency

Two nodes; the first declares a claim on the bundled stub claim producer; the second subscribes to the first via `subscribes:` and consumes the captured address through `{{nodes.<source>.attribute.<field>}}` (which also auto-subscribes the receiver to the sender's `attribute` topic).

**Precondition:** the bundled docker-compose stack is up:

```sh
docker compose -f deploy/docker-compose.yml up -d
```

The bundled `executor-stub` runs in stub mode (`RIMSKY_EXECUTOR_STUB_MODE=1`). The stub keys behavior on `node_type` only — it always emits a `Complete` event with `changed: true` and an empty `attributes_delta` (or a small fixture for known node types). This example demonstrates the all-success path: both nodes commit, the claim is auto-`Commit`ted at the acquirer's terminal.

## 1. The template

Save as `two-node.yml`:

```yaml
name: two-node-claim
version: "1.0"
frame_resolution_mode: serial_queue
params_schema:
  type: object
  additionalProperties: true
nodes:
  - type: acquirer
    executor: stub
    stores:
      - { name: stub, selector: "items/{{params.item_id}}", intent: rw, alias: workspace }
    attributes:
      schema:
        type: object
        properties:
          captured_address:
            type: string
            source: "{{claim.workspace.address}}"

  - type: consumer
    executor: stub
    # The `{{nodes.acquirer.attribute.captured_address}}` substitution
    # below auto-subscribes consumer to acquirer's `attribute` topic;
    # the explicit `subscribes:` entry below makes that coupling
    # obvious to readers.
    subscribes:
      - { node: acquirer, on: state }
    attributes:
      schema:
        type: object
        properties:
          relayed_address:
            type: string
            source: "{{nodes.acquirer.attribute.captured_address}}"
```

The `stub` claim producer must be configured under `claim_producers:` in `rimsky.yml` (the bundled docker-compose ships one).

## 2. Register, deploy, instantiate

```sh
rimsky template register two-node.yml
# returns: template_hash=sha256-..., tags=

rimsky template deploy sha256-...

rimsky instance create sha256-... \
    --params '{"item_id":"42"}'
# returns: instance_id=..., template_hash=sha256-..., node_count=2
```

## 3. Observe both nodes settle

```sh
curl http://localhost:8080/instances/<instance_id>/nodes
```

Expected output: both nodes in `fresh` state.

## Verification

```sh
curl -s http://localhost:8080/instances/<instance_id>/nodes \
  | jq '[.nodes[] | {node_name, state}]'
```

Expected output:

```json
[
  { "node_name": "acquirer", "state": "fresh" },
  { "node_name": "consumer", "state": "fresh" }
]
```

The acquirer's claim was committed at its terminal (success → `Commit`); the held-claim listing for that handle is empty:

```sh
# After settling, the claim handle is gone — listing returns an empty holders array.
curl -s http://localhost:8080/lock-holders/<claim_handle_id>/claim-holders | jq '.holders | length'
# Expected: 0
```
