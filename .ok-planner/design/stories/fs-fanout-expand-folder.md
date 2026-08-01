---
story: fs-fanout-expand-folder
status: as-is
---

# Template author fans out over picked-folder contents against the filesystem store

## Role

As a template author,

## Capability

I can declare a fan-out node whose claim is one folder picked from the bundled filesystem store and whose `partition_request` says "expand the folder's contents,"

## Business value

so that I can process every file in the folder in parallel without enumerating them upstream.

