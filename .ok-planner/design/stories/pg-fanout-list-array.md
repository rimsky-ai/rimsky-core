---
story: pg-fanout-list-array
status: as-is
---

# Template author fans out over an upstream list against the postgres store

## Story

As a template author,

I can declare a fan-out node whose parent claim is held against the bundled postgres store and whose `partition_request` is a list of items I produced upstream,

so that I can run one parallel work unit per item against a postgres-backed queue with no custom claim-producer to write.
