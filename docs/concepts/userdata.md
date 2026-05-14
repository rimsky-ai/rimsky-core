---
concept: userdata
definition: |
  Free-form opaque bytes a template author attaches to a node's executor invocation. Rimsky never inspects, parses, substitutes, or validates `userdata`. The executor receives the bytes verbatim. This is distinct from `attributes`, which are typed, substituted, and schema-validated.
proto_symbol: ExecuteRequest in protocols/proto/v1/executor.proto
config_field: (none)
api_surface: (none)
related: [attributes, executor, node]
deprecated_terms: []
---

# Userdata

## Definition

Free-form opaque bytes a template author attaches to a node's executor invocation. Rimsky never inspects, parses, substitutes, or validates `userdata`. The executor receives the bytes verbatim. This is distinct from `attributes`, which are typed, substituted, and schema-validated.

## Why it exists

Different executors want different shapes of input. A claude-agent executor wants a free-form prompt; an http-node executor wants a URL plus body; a generic shell executor wants arbitrary script text. Rimsky cannot impose a single shape on all of them, and shouldn't try.

Userdata is the escape hatch. The bytes go through Rimsky verbatim — no parsing, no substitution, no validation. The executor receives whatever the template author wrote. If the executor's protocol expects userdata in a specific shape (JSON of a particular schema, or a YAML doc, or a raw prompt string), that's a contract between the executor and the template author; Rimsky is uninvolved.

The "no substitution" rule is load-bearing. A `{{...}}` directive in `userdata` reaches the executor literally — no template author can accidentally trigger Rimsky's substitution machinery against opaque bytes. The substitution boundary stays at the typed `attributes:` schema's `source:` fields.

## How you encounter it

- **Templates**: a `userdata:` block on a node declaration. Any bytes — JSON, YAML, plain text, whatever the executor expects.
- **Wire**: `ExecuteRequest.userdata` carries the bytes verbatim to the executor.
- **Verification**: inspecting the per-instance event log via `GET /events?instance_id=<id>` (or via the dashboards) shows that `attributes_substituted` events never list `userdata`-derived fields — Rimsky's substitution pass never touches `userdata`. To see the literal bytes the executor received, use the executor's `ExecutorObservability` protocol (`GetTrace`) where supported.

## Consumer-visible guarantees

- Rimsky never inspects, parses, substitutes, decrypts, hashes, indexes, pattern-matches, or otherwise acts on `userdata`. The bytes traverse Rimsky's address space unchanged.
- `userdata` is not subject to attribute schema validation; the schema validates only the `attributes:` block.
- A `{{...}}` literal in `userdata` reaches the executor as a literal `{{...}}`. If you want substitution, use `attributes:`.

## Common mistakes

- **Rimsky's userdata ≠ cloud-init userdata.** Cloud-init userdata IS parsed by the cloud provider (interpreted as a script or cloud-config YAML). Rimsky's userdata is bytes-in, bytes-out — Rimsky never parses or interprets it.
- Putting `{{nodes.foo.attribute.bar}}` in userdata and expecting it to substitute. It won't — substitution applies only to attribute schema `source:` fields. If you need the value, declare it as an attribute.
- Encrypting userdata for transport. Rimsky transports it as opaque bytes regardless; encryption is the operator's call (and the executor's responsibility to decrypt).

## See also

- [`attributes.md`](attributes.md)
- [`executor.md`](executor.md)
- [`node.md`](node.md)
