# `rimsky-compose` multi-template project

A `rimsky-compose.yml` declaring two templates and one persistent instance. `rimsky compose up` reconciles the manifest against a running rimsky deployment.

**Precondition:** the bundled docker-compose stack is up:

```sh
docker compose -f deploy/docker-compose.yml up -d
```

## 1. The compose manifest

Save as `rimsky-compose.yml`:

```yaml
project: project-alpha

context: local

templates:
  - path: ./templates/items-pipeline.yml
    tag: items-pipeline@1.0
    state: deployed
  - path: ./templates/analytics-rollup.yml
    tag: analytics-rollup@1.0
    state: deployed

instances:
  - template: items-pipeline@1.0
    name: items-driver
    params:
      mode: continuous
    restart: never
```

`templates:` is a list of `{path, tag, state}` entries; `instances:` is a list of `{template, name, params, restart}` entries. The `template` field of an instance may be either a manifest tag (matched against `templates[].tag`) or a literal `sha256-<64-hex>` hash. The `tag` field automatically gets the `compose:<project>:` prefix applied (so `items-pipeline@1.0` is registered as `compose:project-alpha:items-pipeline@1.0`); same for `instances[].name`.

## 2. Apply

```sh
rimsky compose up -f rimsky-compose.yml
```

`compose up` registers both templates, deploys them per `state:`, creates / moves the project-prefixed tags, and creates the `items-driver` persistent instance under the `compose:project-alpha:items-driver` instance key.

## 3. Observe

```sh
rimsky template list
rimsky tag list
rimsky instance list
```

Expected: the two templates, the two project-prefixed tags pointing at the registered hashes, and the `items-driver` instance.

## Verification

Re-running `compose up` is idempotent:

```sh
rimsky compose up -f rimsky-compose.yml
```

Expected output: no-op messages for templates that are already deployed, tags that already point at the right hash, and the instance that already exists.

## Notes

- The `compose:<project>:` prefix on instance keys and tags is reserved for `rimsky compose up`. Manual creation under this prefix is rejected client-side.
- Removing a template from the manifest does not deregister it from the running deployment — `compose up` does not delete resources it owns; it reconciles forwards only.

## See also

- [`../../concepts/template.md`](../../concepts/template.md)
- [`../../concepts/tag.md`](../../concepts/tag.md)
- [`../../concepts/instance.md`](../../concepts/instance.md)
