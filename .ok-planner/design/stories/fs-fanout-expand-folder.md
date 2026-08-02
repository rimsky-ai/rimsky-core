---
story: fs-fanout-expand-folder
---

# Template author fans out over picked-folder contents against the filesystem store

## Story

As a template author,

I can declare a fan-out node whose claim is one folder picked from the bundled filesystem store and whose `partition_request` says "expand the folder's contents,"

so that I can process every file in the folder in parallel without enumerating them upstream.
