# Minimal template and instance

Register a one-node template, deploy it, create an instance, observe the node settle into `fresh`.

**Precondition:** the bundled docker-compose stack is up:

```sh
docker compose -f deploy/docker-compose.yml up -d
```

The bundled `rimsky/executor-stub` image is always in stub mode: it returns a canned `Complete` event keyed only on `node_type`, ignoring the request's `attributes` bag for behavior selection. (The `RIMSKY_EXECUTOR_STUB_MODE=1` env var is a separate mechanism on the bundled `rimsky/executor-http-node` and `rimsky/executor-claude-agent` images that short-circuits their network paths for testing.)

## 1. The template

Save as `minimal.yml`:

```yaml
name: minimal
version: "1.0"
frame_resolution_mode: serial_queue
nodes:
  - type: hello
    executor: stub
    attributes:
      schema:
        type: object
        additionalProperties: true
```

## 2. Register and deploy

```sh
rimsky template register minimal.yml
# returns: template_hash=sha256-abc...def, tags=

rimsky template deploy sha256-abc...def
```

## 3. Create an instance

```sh
rimsky instance create sha256-abc...def
# returns: instance_id=01HZ..., template_hash=sha256-abc...def, node_count=1
```

## 4. Observe completion

```sh
curl http://localhost:8080/instances/01HZ.../nodes
```

Expected output (after the stub executor returns):

```json
{
  "nodes": [
    {
      "node_name": "hello",
      "state": "fresh"
    }
  ]
}
```

## Verification

```sh
curl -s http://localhost:8080/instances/01HZ... | jq '.frame_state'
```

Expected output: `"resolved"` (frame ended; instance settled).
