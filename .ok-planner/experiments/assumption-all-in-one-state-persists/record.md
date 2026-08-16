---
experiment: assumption-all-in-one-state-persists
commit: d977250c
---

# What survives when the container does not

## What it ran against

Three `rimsky-all-in-one` containers from the tree's own image tag over one host
directory mounted at `/var/lib/rimsky`. The first registers a template, deploys
it, creates an instance under a fixed key, wakes the graph with an empty message
and a typed one, and runs to quiet. That container is then destroyed and a
second one started over the same directory, and everything is read again. A
third container is started with nothing mounted.

## What was observed

Seven checks, none failing.

The first deployment ended holding one template, one instance and eleven events
over five kinds, with `state.db` — plus its write-ahead log and lock files — on
the mounted directory.

After the container was destroyed and replaced, everything came back: the same
template id, the same instance id and instance key, the same eleven events in
the same kinds, the same messages, and the same per-node run counts.

A container started with nothing mounted came up with no templates and no
instances at all, so the mount is what carried the history rather than anything
baked into the image.
