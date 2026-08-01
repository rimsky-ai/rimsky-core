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

