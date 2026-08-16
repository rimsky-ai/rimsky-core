---
experiment: assumption-image-names-follow-one-scheme
commit: d977250c
---

# Whether the image name can be guessed from the kind

## What it ran against

The fifteen image names of the shipped set, each confirmed present at the tree's
own tag, tested mechanically against `rimsky-<kind>-<name>` for the four service
kinds. Then a small stack — a `rimsky-sensor-cron` container and a
`rimsky-all-in-one` orchestrator — wired two ways: once with the sensor declared
under `publishers` in the mounted `rimsky.yml`, once under `sensors`.

## What was observed

Five checks, none failing.

Eleven of the fifteen follow the scheme, and they cover all four service kinds:
`claim-producer`, `executor`, `sensor` and `subscriber`. Every service image is
guessable from its kind and name — `rimsky-claim-producer-filesystem`,
`rimsky-executor-http-node`, `rimsky-sensor-cron`,
`rimsky-subscriber-openlineage`.

The other four are the core images — `rimsky`, `rimsky-all-in-one`,
`rimsky-host-agent-proxy`, `rimsky-conformance` — which carry no service kind
because they are not services of a kind.

The kind word is not the configuration's word for the same thing. The sensor
image is wired into a deployment under `publishers`, where its subscription
mounts live as kind `cron`; the same block written under `sensors` stops the
container at load, because `sensors` is not a key the configuration knows.
