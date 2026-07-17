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

## Acceptance

I author a template with a fan-out node holding a filesystem-store folder claim and a `partition_request` of the shape `{"expand_folder": {"filter": "*.json"}}`. The template registers; I deploy; the parent Open picks a folder containing N matching files. The fan-out node opens N sub-claims (one per matching child path), dispatches N children in parallel, and each child's `{{child.partition_key}}` carries the matched path relative to the picked folder (equal to the file's basename at the default depth of 1; a slash-joined relative path at depth greater than 1) while `{{claim.<alias>.address}}` carries the file's absolute path.

## Falsifier

The filesystem store rejects the partition_request shape; OR the fan-out dispatches the wrong number of children for a folder seeded with a known count of files matching the filter at the configured depth; OR children dispatch but each child's `claim.<alias>.address` still addresses the parent folder rather than its assigned child path.

## Proof

Executable proof. A runnable example with a seeded folder containing 3 JSON files; one folder is picked, expand-folder fans out 3 children, each observably processes its specific file through to terminal.
