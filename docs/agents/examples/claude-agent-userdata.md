# Userdata is inert in Rimsky

A single-node template whose `userdata:` block contains literal `{{...}}` text. Rimsky must NOT substitute it. The executor receives the bytes verbatim. The verification observes that Rimsky's `attributes_substituted` events list only the schema's `source:` fields — `userdata` is never touched.

**Precondition:** the bundled docker-compose stack is up:

```sh
docker compose -f deploy/docker-compose.yml up -d
```

The bundled `executor-stub` runs in stub mode (`RIMSKY_EXECUTOR_STUB_MODE=1`). The stub ignores `userdata` for behavior selection — its job here is simply to receive the dispatch and emit a terminal `Complete`. The proof that Rimsky did not substitute `userdata` is upstream of the executor: the `attributes_substituted` event in the events log records the fields Rimsky resolved at dispatch.

## 1. The template

Save as `userdata-demo.yml`:

```yaml
name: userdata-demo
version: "1.0"
frame_resolution_mode: serial_queue
nodes:
  - type: summarize
    executor: stub
    attributes:
      schema:
        type: object
        additionalProperties: true
    userdata:
      prompt: |
        Summarize the following document.
        Use Markdown formatting where appropriate. Substitute literal text
        like {{nodes.upstream.attribute.value}} into the output if it appears in the
        source, but do not expect Rimsky to have substituted it on input.
      model: claude-sonnet-4-6
```

The `{{nodes.upstream.attribute.value}}` literal in `userdata.prompt` is intentional — Rimsky does not parse or substitute `userdata`, so the executor sees the literal text. (`userdata` is a YAML map per the template DSL, not a string.)

## 2. Register, deploy, instantiate

```sh
rimsky template register userdata-demo.yml
rimsky template deploy sha256-...
rimsky instance create sha256-...
```

## 3. Inspect the dispatch event log

After the instance settles, fetch the per-instance event log and look at the `attributes_substituted` event for the `summarize` node. That event names exactly the attribute schema fields whose `source:` directives Rimsky resolved at dispatch — it does not list `userdata` because `userdata` is not an attribute and is not a substitution target.

```sh
curl "http://localhost:8080/events?instance_id=<instance_id>"
```

Expected: events of kind `attributes_substituted` (Rimsky-side substitution at dispatch) list only schema fields with `source:` directives. In this template no schema field has a `source:`, so `substituted_fields` is empty. `userdata` never appears anywhere in the substitution events.

## Verification

```sh
curl -s "http://localhost:8080/events?instance_id=<instance_id>" \
  | jq -r '[.events[] | select(.kind=="attributes_substituted") | .payload.substituted_fields[]] | length'
```

Expected output: `0` (no schema field had a `source:` directive in this template, so substitution touched nothing — and since `userdata` is not an attribute, the `{{nodes.upstream.attribute.value}}` literal it contains was never even a candidate for substitution).

## See also

- [`../../concepts/userdata.md`](../../concepts/userdata.md)
- [`../../protocols/executor.md`](../../protocols/executor.md)
