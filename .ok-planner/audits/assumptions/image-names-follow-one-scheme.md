---
assumption: image-names-follow-one-scheme
commit: d977250c
disposition: held
synthesized: 2026-08-16T05:48:16Z
---

# image names follow `rimsky-<kind>-<name>` consistently, so a claim producer is `rimsky-claim-producer-filesystem`, an executor is `rimsky-executor-http-node`, and a sensor would be `rimsky-sensor-cron`.

As operator writing a compose file, I would take it that image names follow `rimsky-<kind>-<name>` consistently, so a claim producer is `rimsky-claim-producer-filesystem`, an executor is `rimsky-executor-http-node`, and a sensor would be `rimsky-sensor-cron`.

## Source

sibling-symmetry — `rimsky-executor-*` and `rimsky-subscriber-openlineage` beside bare `rimsky-sensor-cron` and `rimsky-claim-producer-*`

## What a run would observe

pull each of the fifteen image names and check the scheme holds across all four service kinds.

## Measured

Experiment `assumption-image-names-follow-one-scheme` (five checks, none
failing) tested all fifteen shipped image names against `rimsky-<kind>-<name>`
and then wired a sensor image into a deployment two ways. The prior holds where
it predicts. Eleven of the fifteen follow the scheme and they cover all four
service kinds — `claim-producer`, `executor`, `sensor`, `subscriber` — so each
of the prior's three worked examples is exactly right and every service image is
guessable from its kind and name. The four that carry no kind segment are the
core images (`rimsky`, `rimsky-all-in-one`, `rimsky-host-agent-proxy`,
`rimsky-conformance`), which name no service kind because they are not services
of one.

One seam sits beside the naming rather than inside it: the kind word in the
image name is not the configuration's word for the same thing. The sensor image
is wired under `publishers` in `rimsky.yml`, where its subscription mounts as
kind `cron`; the same block written under `sensors` stops the container at load
because `sensors` is not a key the configuration knows. The compose-file author
guesses the image name correctly and then has to switch vocabulary to wire it.
