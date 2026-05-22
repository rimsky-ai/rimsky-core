# Attribute defaults are inert in Rimsky

A single-node template whose `attributes:` schema declares one property as a static-default value (a literal `{{...}}` string). Rimsky must NOT substitute the default's value. The executor receives it verbatim. The verification observes that Rimsky's `attributes_substituted` event lists only properties with `source:` directives — static-default properties are not substitution targets.

This example demonstrates the structural-inertness discipline (see `concept:inertness`) that replaced the retired `userdata` concept under the 2026-05-21 userdata-collapse spec. Pre-collapse, the same demonstration used a `userdata:` block. Post-collapse, the analogous surface is a `default:` value on the unified attribute schema.

**Precondition:** the bundled docker-compose stack is up:

```sh
docker compose -f deploy/docker-compose.yml up -d
```

The bundled `executor-stub` runs in stub mode (`RIMSKY_EXECUTOR_STUB_MODE=1`). The stub ignores attribute values for behavior selection — its job here is simply to receive the dispatch and emit a terminal `Complete`. The proof that Rimsky did not substitute the static default is upstream of the executor: the `attributes_substituted` event in the events log records the fields Rimsky resolved at dispatch.

## 1. The template

Save as `attribute-defaults-demo.yml`:

```yaml
name: attribute-defaults-demo
version: "1.0"
frame_resolution_mode: serial_queue
nodes:
  - type: summarize
    executor: stub
    attributes:
      schema:
        type: object
        properties:
          prompt:
            type: string
            default: |
              Summarize the following document.
              Use Markdown formatting where appropriate. Substitute literal text
              like {{nodes.upstream.attribute.value}} into the output if it appears in the
              source, but do not expect Rimsky to have substituted it on input.
          model:
            type: string
            default: "claude-sonnet-4-6"
        additionalProperties: true
```

The `{{nodes.upstream.attribute.value}}` literal in `properties.prompt.default` is intentional — Rimsky does not substitute `default:` values, so the executor sees the literal text in the resolved attribute bag.

## 2. Register, deploy, instantiate

```sh
rimsky template register attribute-defaults-demo.yml
rimsky template deploy sha256-...
rimsky instance create sha256-...
```

## 3. Inspect the dispatch event log

After the instance settles, fetch the per-instance event log and look at the `attributes_substituted` event for the `summarize` node. That event names exactly the attribute schema fields whose `source:` directives Rimsky resolved at dispatch — it does not list static-default properties because static defaults are not substitution targets.

```sh
curl "http://localhost:8080/events?instance_id=<instance_id>"
```

Expected: events of kind `attributes_substituted` list only schema fields with `source:` directives. In this template no field has a `source:`, so `substituted_fields` is empty. The static-default properties (`prompt`, `model`) flow into the dispatch bag from the schema's `default:` keyword verbatim — never substituted, but persisted alongside source-resolved values in `rimsky_node_attributes.data`.

## Verification

```sh
curl -s "http://localhost:8080/events?instance_id=<instance_id>" \
  | jq -r '[.events[] | select(.kind=="attributes_substituted") | .payload.substituted_fields[]] | length'
```

Expected output: `0` (no schema field had a `source:` directive in this template, so substitution touched nothing — and since static-default values are not substitution candidates, the `{{nodes.upstream.attribute.value}}` literal in `properties.prompt.default` was never even a candidate for substitution).

## See also

- [`../../concepts/attribute.md`](../../concepts/attribute.md)
- [`../../protocols/executor.md`](../../protocols/executor.md)
