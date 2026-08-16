---
experiment: producer-error-passthrough
commit: PENDING
---

# Reading a producer's error class and message in the API response

## What it ran against

`run.py` builds `producer.go` — a claim producer written for this experiment
against the published claim-producer and data-processing gRPC protocols, whose
Release verb rejects with a caller-named error class carried as a gRPC error
detail — for Linux and runs two instances of it in `alpine` containers on a
private docker network, one rejecting with `content/release_refused` and the
other with `storage/quota_exceeded`. It then boots a `rimsky-all-in-one`
container from this tree's image on the same network with a mounted
`rimsky.yml` naming both producers, materialises one durable asset against
each, and asks the control API to retire each asset — the API-triggered
operation that calls the producer's Release verb.

## What was observed

Fifteen checks, none failing. Each retire returned HTTP 422 with a body
carrying `error_class`, `message`, and `producer_name`. The class was the one
that producer rejects with, and the two producers produced two different
classes, so the class in the response follows the producer rather than being a
rimsky constant. The message was the producer's own sentence naming the claim
it refused to drop, and the `error` field named the Release verb. The status
was 422 rather than 500, distinguishing the producer's rejection from a rimsky
internal error. `rimsky asset delete` exited non-zero and repeated the
producer's message. Both assets remained in the listing, because the producer
refused the release.
