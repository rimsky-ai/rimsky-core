---
experiment: validation-mixin-uniform
commit: d977250c
---

# The validation mix-in advertised from an executor peer and a publisher peer

## What it ran against

`peerstub.go` is a service written for this experiment. It speaks the published
protocols and nothing else: with `-kind executor` it serves the executor and
executor-observability protocols, with `-kind publisher` it serves the publisher
protocol, and in both cases it also serves the validation protocol's `Validate`
RPC and advertises the roles given by `-roles` as
`validation_supported_roles` in its primary protocol's capabilities handshake.
Its `Validate` returns one warning naming the peer, the role it was called for,
and the context it was given.

`run.py` builds `peerstub.go`, starts three of them on free host ports — an
executor peer declaring role `executor`, a second executor peer declaring role
`claim_producer`, and a publisher peer declaring role `publisher` — then boots a
`rimsky-all-in-one` container from this tree's image at `RIMSKY_IMAGE_TAG` with
a mounted `rimsky.yml` declaring all three at `host.docker.internal`, each with
`validation` in its `protocols` list. One template naming both executor peers as
node executors and the publisher peer under `publishers:` goes through
`POST /v1/templates/validate` and `POST /v1/templates`. `run.py` stops the peers
and removes the container.

## What was observed

Seven checks, none failing.

The validate response carried two `mixin_consulted` warnings. One came from the
executor peer, called for role `executor` with `node_alias=worker`. One came
from the publisher peer, called for role `publisher` with
`publisher_name=publisher-peer`. Neither peer is a claim producer, and rimsky
found both mix-ins through each peer's own capabilities handshake.

The second executor peer, which advertised the mix-in but declared only the role
`claim_producer`, was never called. Its node was in the same template as the
first executor peer's node.

`POST /v1/templates` returned the same two findings, so the mix-ins are
consulted on registration and not only on validation.
