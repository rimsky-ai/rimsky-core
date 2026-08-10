---
audit: fs-fanout-expand-folder
artifact: story:fs-fanout-expand-folder
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Fanning out over the contents of a picked folder without enumerating them

Supported. Against an all-in-one deployment with the bundled filesystem claim
producer configured with a pick policy over two candidate folders, each holding
three matching files and one non-matching file, one fan-out node declared that
pick policy as its claim and a folder-expanding partition request. The producer
opened exactly one parent claim naming one of the two folders; the split
returned three sub-scopes; the sub-claims were keyed by that folder's three
matching file names; the non-matching file in the same folder and every file of
the folder that was not picked appeared nowhere in the run. The parent and its
three clones all settled fresh, one work unit ran per file addressed to its own
file, and the endpoint the work units called reported three requests in flight
at once. The template named no file.

## Compliance

The capability clause names a template key (`partition_request`) and a specific
shipped component ("the bundled filesystem store"), both delivery-surface
choices the story rules place in `decisions/`; the compliant text is "I can
declare a fan-out node whose claim is one folder picked from a filesystem-backed
claim producer and whose partition declaration expands the folder's contents".
