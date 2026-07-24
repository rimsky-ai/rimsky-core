# Fan-out implementation across the bundled stores

## User outcomes

### STORY-fs-fanout-list-array

As a template author, I can declare a fan-out node whose claim is held against the bundled filesystem store and whose `partition_request` is a list of items I produced upstream, so that I can run one parallel work unit per item with no custom claim-producer to write.

> **Acceptance:** I author a template with a `fan_out:` node holding a filesystem-store claim, `partition_request` substituted from an upstream source (a list of `{key, payload}` objects). Template registers; I deploy; an instance triggers and produces N items upstream. The fan-out node opens N sub-claims, dispatches N children in parallel, each child sees its `{{child.partition_key}}` matching the upstream item key and its `{{claim.<alias>.payload}}` matching the upstream payload.
>
> **Falsifier:** Template registration rejects with the cap-advertisement error; OR the instance dispatches only one child when N were expected; OR children dispatch with empty / wrong key or payload.
>
> **Proof:** example — a runnable example under `examples/` with a 3-item upstream list, the fan-out node, and an observable terminal where 3 children processed their distinct items.

### STORY-fs-fanout-expand-folder

As a template author, I can declare a fan-out node whose claim is one folder picked from the bundled filesystem store and whose `partition_request` says "expand the folder's contents," so that I can process every file in the folder in parallel without enumerating them upstream.

> **Acceptance:** I author a template with a `fan_out:` node holding a filesystem-store folder claim, `partition_request: {"expand_folder": {"filter": "*.json"}}`. Template registers; I deploy; the parent Open picks a folder containing N matching files. The fan-out node opens N sub-claims (one per matching child path), dispatches N children in parallel, each child's `{{child.partition_key}}` carries the file's basename and `{{claim.<alias>.address}}` carries the file's absolute path.
>
> **Falsifier:** The filesystem store rejects the partition_request shape; OR the fan-out dispatches the wrong number of children for a folder seeded with a known count of files matching the filter at the configured depth; OR children dispatch but each child's `claim.<alias>.address` still addresses the parent folder rather than its assigned child path.
>
> **Proof:** example — a runnable example under `examples/` with a seeded folder containing 3 JSON files; one folder is picked, expand-folder fans out 3 children, each observably processes its specific file through to terminal.

### STORY-pg-fanout-list-array

As a template author, I can declare a fan-out node whose claim is held against the bundled postgres store and whose `partition_request` is a list of items I produced upstream, so that I can run one parallel work unit per item against a postgres-backed queue with no custom claim-producer to write.

> **Acceptance:** Same shape as STORY-fs-fanout-list-array, postgres store side.
>
> **Falsifier:** Same shapes as STORY-fs-fanout-list-array, postgres side.
>
> **Proof:** example — a runnable example under `examples/`, postgres-backed, 3-item upstream list, observable 3-child dispatch.

### STORY-fanout-any-substitution-source

As a template author, I can write a fan-out `partition_request` that substitutes from any standard source — upstream node attribute, claim payload, instance param, or typed message — and the substitution engine resolves it uniformly, so the source I use is my choice and not the architecture's.

> **Acceptance:** I author one template with `partition_request: "{{nodes.prefilter.attribute.items}}"` and a second with `partition_request: "{{messages.backfill_trigger.items}}"`, both targeting the same fan-out node + claim. Both templates register; both deploy; both produce N parallel child runs when their respective sources resolve.
>
> **Falsifier:** One source returns "not found" at substitution while the other resolves; OR one is rejected at registration while the other passes.
>
> **Proof:** example — two runnable templates side-by-side, exercising the two sources.

### STORY-messages-as-nodes-substitution

As a template author, I can use `{{messages.<type>.<field>}}` anywhere `{{nodes.<type>.<field>}}` would work, and both directives resolve through the same lookup. The only difference is registration-time validation: `messages.<type>` requires `<type>` to be declared in the template's `messages:` registry.

> **Acceptance:** I author a template using `{{messages.foo.body}}` where `foo` is declared in `messages:` — semantics identical to `{{nodes.foo.body}}` for a node named `foo`. A template declaring `{{messages.bar.x}}` for a `bar` not declared in `messages:` rejects at registration with a clear error naming `bar` and the missing declaration.
>
> **Falsifier:** The two directives produce different errors or different values for the same underlying data; OR registration accepts undeclared message types.
>
> **Proof:** example — runnable template using both forms; plus a registration-failure proof for the undeclared case.

### STORY-sub-claim-payload-substitution

As a template author, I can read producer-supplied per-sub-claim data via `{{claim.<alias>.payload[.<field>]}}` in a fan-out child's substitution context. The path resolves identically to how it resolves on a regular Open'd claim — there is no second mechanism to learn.

> **Acceptance:** I author a fan-out template; the producer returns per-sub-claim `payload` bytes; the child's attribute substitution reads the per-sub-claim payload via the standard `{{claim.<alias>.payload.<field>}}` directive.
>
> **Falsifier:** `{{claim.<alias>.payload}}` returns "empty" or "not found" for sub-claims; OR resolves to the parent's payload rather than the sub-claim's.
>
> **Proof:** example — per-sub-claim payload visible in each child's executor run.

### STORY-fanout-intent-inheritance

As a template author, I can declare a fan-out claim with `intent: r` and trust that read-only applies to the sub-claims too, so my read-only declarations are honored end-to-end.

> **Acceptance:** I author a fan-out template with `claims: [{name: data, intent: r}]`; deploy and trigger; the producer's Commit handler treats sub-claim Commits as read-only (no items-table write-back on the postgres store; no `_committed/` move on the filesystem store). I author a second template with `intent: rw`; the producer's Commit handler exhibits write-back.
>
> **Falsifier:** A read-only fan-out template later causes the producer to perform write-back operations on sub-claim Commit (the inheritance regressed); OR the producer's behavior on a sub-claim Commit diverges from its behavior on a sibling regular claim of the same intent.
>
> **Proof:** example — side-by-side runnable templates (one `intent: r`, one `intent: rw`) with observable producer-side write behavior differing per declared intent.

## Mechanism

The work splits into four layers, each addressed by a small set of atomic technical decisions:

1. **Substitution layer** — make `partition_request` substitution match every other substitution call site; retire the legacy `trigger.*` directive; make `messages.<type>` sugar for `nodes.<type>`.
2. **Sub-claim wire/persistence parity** — bring `SubScopeDescriptor` to parity with `Acquired` so sub-claims carry payload and address; fix the hardcoded sub-claim intent.
3. **Bundled-store producer side** — implement `SplitScope` in the filesystem and postgres stores with substrate-specific partition_request shapes; advertise `supports_split_scope`.
4. **Verification + plumbing** — extend the claim-producer conformance suite; unify auto-subscribe across messages and nodes; place the universal `{"list": [...]}` unmarshal helper in `lib/services/stores/shared/`.

### Technical decisions

#### TD-partition-request-substitution-scope

**Choice:** `partition_request` substitution uses the same ResolveContext as executor-attribute dispatch. The bespoke narrow builder at `lib/runtime/runner_acquire_helpers.go::substituteFanOutPartitionRequest` is replaced with a call to the shared dispatch context builder (or factored so both call sites share it).

**Rationale:** there is no principled reason for `partition_request` to see a narrower source catalog than other substitution call sites. The current narrowness (no `Deps`, no upstream-node-attribute access) was a deliberate but local choice driven by a backfill use case that the typed-message consolidation has since reframed.

#### TD-messages-as-nodes-sugar

**Choice:** `{{messages.<type>.<field>}}` is sugar for `{{nodes.<type>.<field>}}`. Both directives resolve through the same `Deps` lookup against the substitution engine's standard source catalog. The only difference is registration-time validation: `messages.<type>` requires `<type>` to be declared in the template's `messages:` registry, where `nodes.<type>` requires `<type>` to be declared as a node-type. The dispatch context builder populates `Deps["<type>"]` with the message body when the receiver has a drained wait-set row for the message-type's virtual-node delivery, per `decision:substitution-context-builder-reads-drained-rows` — the same path that already feeds `Deps["<node-type>"]` for real-node attribute reads.

**Rationale:** matches `concept:message`'s "messages are virtual node-types" framing. One substitution channel, not two; one ResolveContext field, not two. The dedicated `TriggerMessagePayload` / `TriggerMessageType` fields and their lookup machinery go away because the same data now flows through `Deps` via the existing drained-wait-set-rows mechanism.

#### TD-legacy-trigger-message-retirement

**Choice:** drop the `{{trigger.message.payload.<field>}}` substitution path from the engine. Drop the `TriggerMessagePayload` and `TriggerMessageType` fields from `ResolveContext`. Drop `triggerMessageForFrame` and its callers. Templates using the legacy form fail at registration with the standard "unknown directive prefix" error.

**Rationale:** pre-v1, no migration commitment. The typed form `{{messages.<type>.<field>}}` (now sugar for `{{nodes.<type>.<field>}}` per TD-messages-as-nodes-sugar) is the only way messages enter substitution.

#### TD-sub-claim-wire-payload-address

**Choice:** `proto:claim_producer.proto::SubScopeDescriptor` gains `bytes address = 4` and `bytes payload = 5`, bringing it to wire parity with `Acquired` for fields meaningful per-partition. Persistence: the `payload` and `address` columns already exist on `rimsky_claim_handles` (migration 008 added `payload`; `address` has been there). The current sub-claim insert path nils both. Runtime: the `SubClaim` struct gains `Address` and `Payload` fields; `AcquireSubClaims` propagates them from the SubScopeDescriptor into the existing columns and into the returned value.

**Rationale:** a sub-claim is a claim — `{{claim.<alias>.payload}}` and `{{claim.<alias>.address}}` should resolve uniformly regardless of how the claim was acquired. The Acquired/SubScopeDescriptor wire-shape asymmetry was incidental, not principled; the persistence row is already shaped for the symmetric case.

#### TD-sub-claim-intent-inheritance

**Choice:** `AcquireSubClaims` reads the parent claim's `intent` from the acquisition context and uses it for the sub-claim insert, replacing the hardcoded `intent := "rw"` in `lib/runtime/runner_subclaim.go`.

**Rationale:** a read-only fan-out claim should produce read-only sub-claims. The hardcoded value is a bug, not a design choice.

#### TD-fs-partition-shapes

**Choice:** the bundled filesystem store accepts three `partition_request` shapes, dispatched on the top-level discriminator:

- `{"list": [{"key": "...", "payload": <opaque>}, ...]}` — pass-through. One sub-scope per element. Sub-scope's `partition_key` = element's `key`; `payload` = element's `payload`; `address` and `claim_scope_data` synthesized from the parent claim's substrate identity plus the element key.
- `{"batch_pick": {"max_items": K}}` — atomic K-item pop from the configured queue. One sub-scope per popped item.
- `{"expand_folder": {"filter": "*", "depth": 1, "kind": "files"}}` — enumerate the parent folder's contents per the filter / depth / kind options. One sub-scope per matching child path. Sub-scope's `partition_key` = relative path from the parent folder; `address` and `claim_scope_data` = absolute path to the matched child.

Unknown discriminators are rejected with `InvalidArgument`.

**Rationale:** `"list"` covers STORY-fs-fanout-list-array (author-supplied list); `"batch_pick"` covers atomic K-item parallelism over the queue; `"expand_folder"` is the substrate-native nested-container walk.

#### TD-pg-partition-shape

**Choice:** the bundled postgres store accepts two `partition_request` shapes:

- `{"list": [{"key": "...", "payload": <opaque>}, ...]}` — pass-through, identical semantics to the filesystem store's pass-through.
- `{"partition_policy": "@<name>", "params": {...}}` — operator-declared partition policy. The operator pre-declares **partition policies** in the postgres store's YAML config alongside `pick_policies:` (e.g. an entry naming the items table, select columns, and parameterized where-clause). The author references the policy by name and supplies `params` via substitution. The producer dispatches the named policy with the params, returns one sub-scope per resulting row.

Unknown discriminators are rejected with `InvalidArgument`.

**Rationale:** mirrors the operator-declares / author-references model of regular postgres pick policies. No SQL leaks into the template authoring surface; the producer owns the substrate query.

#### TD-supports-split-scope-advertisement

**Choice:** both bundled stores set `supports_split_scope: true` in their `Capabilities` response.

**Rationale:** required by `concept:fan-out`'s invariant for template registration to accept `fan_out:` over either producer.

#### TD-conformance-additions

**Choice:** the claim-producer conformance suite at `lib/protocols/conformance/claimproducer/` gains SplitScope test cases: (a) verify the existing rejection of SplitScope on producers not advertising `supports_split_scope`; (b) round-trip the `{"list": [...]}` pass-through shape (any producer advertising the cap should accept this); (c) verify the `SubScopeDescriptor` wire shape including the new `address` and `payload` fields. Per-store substrate-native shapes (`batch_pick`, `expand_folder`, `partition_policy`) are tested in each store's own test suite, not in the conformance runner.

**Rationale:** the conformance runner covers the cross-producer contract any claim-producer implementation must satisfy; substrate-specific behavior is properly per-store.

#### TD-messages-coverage-check-unification

**Choice:** the substitution-ref coverage check treats `{{messages.<type>.<field>}}` identically to `{{nodes.<type>.<field>}}`. A ref to `messages.<type>` is satisfied by a subscription entry naming the type, the same way a ref to `nodes.<type>` is satisfied. Registration rejects templates with uncovered refs in both cases, with the same error shape.

**Rationale:** maintains the coverage-required invariant of `decision:substitution-ref-coverage-required` while extending its symmetry to the messages surface; if substitution is sugar (per TD-messages-as-nodes-sugar), the static coverage check must be sugar too. No auto-subscribe is introduced; the explicit-subscription model of `story:explicit-attribute-context-read` is preserved.

#### TD-shared-list-array-unmarshal

**Choice:** the `{"list": [...]}` pass-through unmarshal logic lives in `lib/services/stores/shared/`, importable by both bundled stores and by any third-party producer. Substrate-specific shapes (`batch_pick`, `expand_folder` for filesystem; `partition_policy` for postgres) stay in each store's own package.

**Rationale:** makes `{"list": [...]}` a de facto cross-producer shape any third-party producer can adopt by importing the helper, rather than a coincidence between the two bundled stores.

## Design changes

- **Concept: mutate `concepts/fan-out.md` in place.** Replace the third invariant bullet (currently begins "The `partition_request` field is opaque to rimsky's split logic … but it is resolved through substitution at acquisition, not passed verbatim. Before the partition-split operation runs, fan-out acquisition runs the node's `partition_request` through the substitution engine with the triggering message's payload in scope") with: "The `partition_request` field is opaque to rimsky's split logic — rimsky does not parse its meaning — but it is resolved through substitution at acquisition, not passed verbatim. Substitution uses the standard resolve context — the same source catalog available to executor-attribute dispatch and to locks-stage substitution per `decision:substitution-grammar-closed`. `partition_request` is not architecturally distinguished from any other substituted field."

- **Concept: mutate `concepts/message.md` in place.** Add a fifth invariant: "The substitution directive `{{messages.<type>.<field>}}` is sugar for `{{nodes.<type>.attribute.<field>}}` — both resolve through the same lookup. The only difference is a registration-time check that `<type>` is declared in the template's `messages:` registry, where `nodes.<type>` requires `<type>` to be declared as a node-type. The substitution-ref coverage check of `decision:substitution-ref-coverage-required` treats the two directives identically."

- **Concept: mutate `concepts/claim-producer.md` in place.** In the "What it is" section, after the bullet describing Split-scope, replace the existing sentence describing SubScopeDescriptor with: "SplitScope's SubScopeDescriptor carries the same substrate-meaningful claim fields a regular Open's Acquired carries — `claim_scope_data`, `address`, `payload` — plus the per-partition discriminators `partition_key` and `producer_metadata`. A sub-claim is a claim; substitution paths over a sub-claim resolve identically to those over a regular claim."

- **Concept: mutate `concepts/fan-out.md` in place.** Replace the sixth invariant bullet (currently describes the sub-claim acquisition transaction) with: "The producer's SplitScope verb is dispatched on a producer-defined `partition_request` shape and returns a list of SubScopeDescriptors, each carrying the substrate-meaningful claim fields (`claim_scope_data`, `address`, `payload`) plus the per-partition discriminators (`partition_key`, `producer_metadata`). The fan-out node iterates the returned list — uniform across producers; what shapes a given producer accepts is producer-specific."

## Manifest

### Stories

- **STORY-fs-fanout-list-array** — author-supplied list, filesystem-store fan-out (Proof: example)
- **STORY-fs-fanout-expand-folder** — picked-folder contents, filesystem-store fan-out (Proof: example)
- **STORY-pg-fanout-list-array** — author-supplied list, postgres-store fan-out (Proof: example)
- **STORY-fanout-any-substitution-source** — `partition_request` resolves from any standard source uniformly (Proof: example)
- **STORY-messages-as-nodes-substitution** — `{{messages.<type>}}` is sugar for `{{nodes.<type>}}` with registry validation (Proof: example)
- **STORY-sub-claim-payload-substitution** — `{{claim.<alias>.payload}}` resolves for sub-claims (Proof: example)
- **STORY-fanout-intent-inheritance** — sub-claim intent matches parent (Proof: example)

### Technical decisions

- **TD-partition-request-substitution-scope** — `partition_request` uses the standard ResolveContext
- **TD-messages-as-nodes-sugar** — `messages.<type>` is sugar for `nodes.<type>` with registry validation
- **TD-legacy-trigger-message-retirement** — `{{trigger.message.payload}}` and the `TriggerMessagePayload`/`Type` ResolveContext fields retired
- **TD-sub-claim-wire-payload-address** — `SubScopeDescriptor` gains payload + address; uniform `payload` column on the claim-handle table
- **TD-sub-claim-intent-inheritance** — sub-claims inherit parent's intent
- **TD-fs-partition-shapes** — filesystem store accepts `list` / `batch_pick` / `expand_folder`
- **TD-pg-partition-shape** — postgres store accepts `list` / `partition_policy` (operator-declared)
- **TD-supports-split-scope-advertisement** — both bundled stores advertise the capability
- **TD-conformance-additions** — SplitScope cases added to the claim-producer conformance suite
- **TD-messages-coverage-check-unification** — substitution-ref coverage check treats `messages.<type>` identically to `nodes.<type>`
- **TD-shared-list-array-unmarshal** — universal `{"list": [...]}` unmarshal in `lib/services/stores/shared/`

### Design changes

- Mutate `concepts/fan-out.md` (substitution invariant + SplitScope-output invariant)
- Mutate `concepts/message.md` (sugar invariant)
- Mutate `concepts/claim-producer.md` (SubScopeDescriptor parity)
