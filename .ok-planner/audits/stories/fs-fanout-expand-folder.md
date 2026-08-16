---
audit: fs-fanout-expand-folder
artifact: story:fs-fanout-expand-folder
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:26:27Z
---

# Fanning out over the contents of a folder the filesystem store picked

Supported. A stack from this tree ran with the bundled filesystem claim producer
over a bind-mounted workspace holding two candidate folders, each seeded with
three matching files and one non-matching file, and a template declaring a single
fan-out node whose claim is the pick-policy selector and whose partition request
expands the folder's contents. The producer opened exactly one parent claim on
one of the two folders; the run derives its expectations from which folder was
picked, so it does not depend on the choice. The split returned three sub-scopes,
the sub-claims are keyed by that folder's three matching file names, the
non-matching file in the same folder produced no sub-claim, and no file of the
folder that was not picked appeared anywhere. The parent and its three work units
all settled fresh, each work unit reached an endpoint at a path carrying its own
partition key, and that endpoint — which holds every request open — reported a
peak of three in flight, so the units ran in parallel. The template names no
file.

## Compliance

- The body names the delivery surface, which the story rules place in decisions:
  it quotes the template's partition-request key. The compliant text states the
  need without it — a fan-out over one folder the store picks, fanning out across
  the folder's own contents.
