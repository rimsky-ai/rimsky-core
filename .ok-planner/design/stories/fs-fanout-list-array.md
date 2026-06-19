---
story: fs-fanout-list-array
status: as-is
---

# Template author fans out over an upstream list against the filesystem store

## Role

As a template author,

## Capability

I can declare a fan-out node whose parent claim is held against the bundled filesystem store and whose `partition_request` is a list of `{key, payload}` items I produced upstream,

## Business value

so that I can run one parallel work unit per item with no custom claim-producer to write.

## Acceptance

I author a template with a fan-out node holding a filesystem-store claim and a `partition_request` substituted from an upstream source (a list of `{key, payload}` objects). The template registers; I deploy and trigger an instance that produces N items upstream. The fan-out node opens N sub-claims, dispatches N children in parallel, and each child observes its `{{child.partition_key}}` matching the upstream item key and its `{{claim.<alias>.payload}}` matching the upstream payload.

## Falsifier

Template registration rejects with the capability-advertisement error; OR the instance dispatches only one child when N were expected; OR children dispatch with empty or wrong key or payload.

## Proof

Executable proof. A runnable example with a 3-item upstream list, the fan-out node, and an observable terminal where 3 children processed their distinct items.
