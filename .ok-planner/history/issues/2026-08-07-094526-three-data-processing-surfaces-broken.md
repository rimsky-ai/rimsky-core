---
issue: three-data-processing-surfaces-broken
kind: human
category: bug
artifacts:
  - concept:data-processing
  - concept:asset
  - concept:fan-out
  - story:data-processing-author
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T09:45:26Z
github: https://github.com/rimsky-ai/rimsky-core/issues/86
---

# A data producer's per-candidate metadata is collected and then dropped

When a node's work fans out across many children, each child writes into its own
staging area and commits it separately. The service that owns the data — a
**data-processing producer** — returns a metadata blob with each of those
per-child commits: row counts, output paths, whatever the producer wants the
platform to know about what it just wrote.

rimsky decodes that blob off the wire into a field
(`lib/runtime/clientiface/data_processing.go::CommitCandidateOutput.CandidateMetadata`,
populated by `lib/runtime/peer/data_processing_client.go`) and then nothing
reads it. It is not written back to the parent, not recorded in lineage, not
surfaced anywhere an operator or a downstream node could see it.

That it is a gap rather than a deliberate omission is unusually well evidenced:
both bundled producers go to real trouble to populate it, and the shipped
example's own end-to-end test asserts it comes back non-empty and parses as
JSON. Somebody built the producing half against a consumer that does not exist.

There is a working counterpart one level up. The equivalent metadata on the
*parent* claim's commit does get consumed — it is carried through the outbox and
merged into the parent's attribute bag under a `producer_metadata` key, keyed
per child (`lib/runtime/child_execution.go`). So the mechanism for surfacing
producer metadata already exists and already handles the per-child case; the
per-candidate blob simply never enters it.

The corpus does not settle where it should land. The data-processing concept is
silent on this field, and the fan-out invariant covering parent writeback speaks
only to the parent commit's metadata — the path that already works.

Two other findings filed under this issue are fixed: the unreachable version-id
field on the candidate-commit path is removed (the concept places version-id
production on the parent claim's commit, where it is correctly wired), and the
shipped example now errors on an unknown handle instead of silently succeeding,
so it passes its own conformance suite.

## Options

- **Route it into the existing parent writeback**, alongside the per-child
  metadata already merged there. Reuses a working mechanism and lands the data
  where a downstream node can read it; costs one more thing in the parent's
  attribute bag, which grows with fan-out width.
- **Record it in lineage** as a per-candidate fact. Better fit for audit-shaped
  data and no effect on node inputs; costs a new lineage surface, and the data
  is then invisible to the graph itself.
- **Drop the field.** Honest and cheap; discards a channel both bundled
  producers already use and an example test already asserts.

The ruling decides where a per-candidate commit's metadata goes, or that it goes
nowhere.

## Ruling

> Recommended ruling (/verify-issues): route it into the parent writeback that
> already carries per-child producer metadata. The mechanism exists, it is
> already keyed per child, and a producer that reports what it wrote is
> reporting something the graph should be able to act on — that is what makes it
> different from audit data.
>
> Rationale: the alternative destinations each give up the thing that makes this
> worth wiring at all — lineage makes it visible but not usable by a downstream
> node, and dropping it discards a channel two bundled producers populate and a
> shipped test asserts. Reusing the existing path also keeps one way of doing
> this rather than two, which matters more here than the marginal growth of the
> parent's attribute bag. What would change this call: if per-candidate metadata
> is expected to be large or high-cardinality in real use, the attribute bag is
> the wrong home and lineage becomes the right one.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to
accept — the next /plan-sprint carries it, naming the generated/recommended
batches at sign-off. Edit the text to redirect, empty the section to discuss
live, or delete this note to adopt the ruling as your own. -->
