# Holding-subgraph template demonstrating held-claim resolution

Three nodes: an acquirer and two inheritors. Both inheritors complete successfully; the held claim's automatic resolution computes the aggregate outcome (all-success → `Commit`) and fires `ClaimProducer.Commit`.

The bundled `executor-stub` keys behavior on `node_type` only and ignores the request's `attributes` bag. To demonstrate the all-success path, every node in this example reaches a terminal `Complete` event from the stub. To exercise the any-failure → `Abandon` path, you would need an executor that drives the desired outcome (e.g. `claude-agent`, or a scripted stub registered programmatically by a scenario test).

**Precondition:** the bundled docker-compose stack is up:

```sh
docker compose -f deploy/docker-compose.yml up -d
```

## 1. The template

Save as `holding.yml`:

```yaml
name: holding-subgraph
version: "1.0"
frame_resolution_mode: serial_queue
params_schema:
  type: object
  additionalProperties: true
nodes:
  - type: acquirer
    executor: stub
    stores:
      - { name: stub, selector: "snapshots/{{params.snapshot_id}}", intent: rw, alias: snapshot }
    attributes:
      schema:
        type: object
        properties:
          handle:
            type: string
            source: "{{claim.snapshot.address}}"

  - type: inheritor-a
    executor: stub
    subscribes:
      - { node: acquirer, on: state }
    inherits:
      - { claim: snapshot }
    attributes:
      schema:
        type: object
        properties:
          read_via:
            type: string
            source: "{{claim.snapshot.address}}"

  - type: inheritor-b
    executor: stub
    subscribes:
      - { node: acquirer, on: state }
    inherits:
      - { claim: snapshot }
    attributes:
      schema:
        type: object
        properties:
          read_via:
            type: string
            source: "{{claim.snapshot.address}}"
```

## 2. Register, deploy, instantiate

```sh
rimsky template register holding.yml
rimsky template deploy sha256-...
rimsky instance create sha256-... --params '{"snapshot_id":"snap-1"}'
```

## 3. Observe held-claim resolution

While the inheritors are running, the acquirer's claim handle is in held state. List the holding-subgraph members for that handle:

```sh
curl http://localhost:8080/lock-holders/<claim_handle_id>/claim-holders
```

After both inheritors terminate, the held claim's automatic resolution computes the aggregate outcome (all completed → `Commit`) and fires `ClaimProducer.Commit`. The claim handle is then deleted.

## Verification

```sh
curl -s http://localhost:8080/instances/<instance_id>/nodes \
  | jq '[.nodes[] | {node_name, state}]'
```

Expected output:

```json
[
  { "node_name": "acquirer", "state": "fresh" },
  { "node_name": "inheritor-a", "state": "fresh" },
  { "node_name": "inheritor-b", "state": "fresh" }
]
```

Once the held claim has been resolved, listing the (now-deleted) handle's holders returns an empty array (the route returns `200 OK` with `{"holders": []}` for any UUID-shaped id whose holders are gone — it does NOT return `404`):

```sh
curl -s http://localhost:8080/lock-holders/<claim_handle_id>/claim-holders \
  | jq '.holders | length'
# Expected: 0
```

## See also

- [`../../concepts/holding-subgraph.md`](../../concepts/holding-subgraph.md)
- [`../../concepts/inheritance.md`](../../concepts/inheritance.md)
