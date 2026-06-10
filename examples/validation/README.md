# examples/validation — Validation mix-in service

A minimal, copy-and-modify example of a **Validation** mix-in service:
a gRPC server that implements the single `Validate` RPC and rides
alongside a primary service protocol (claim-producer, executor,
publisher, lifecycle-subscriber). Rimsky calls `Validate` at template
registration time and surfaces the findings to the operator as errors
(blocking) or warnings (informational).

This is intended for service authors who want to layer custom
validation on top of rimsky's built-in shape checks — for example, a
claim-producer that wants to refuse a misspelled retention class
before the template ever runs, or an executor that wants to flag a
suspicious-looking attribute value as a warning.

## What this example ships

- `validation.go` — the `Validation` server. Routes on the
  `ValidateRequest.context` oneof; demonstrates both the **executor**
  arm (validates `attributes_schema` is parseable JSON) and the
  **claim-producer** arm (routes on a per-binding `selector` sentinel
  to surface either an error finding, a warning finding, or
  acceptance).
- `producer.go` — a minimal `ClaimProducer` companion. Its only job
  is to host the `Capabilities` handshake that advertises the
  `validation` mix-in alongside the primary protocol — rimsky reads
  `validation_supported_roles` from the claim-producer Capabilities
  response to decide which roles the Validation service is willing to
  validate. A real service that already has a substantive primary
  protocol (a true claim-producer, an executor, a publisher) would
  advertise the validation mix-in from THAT primary protocol's
  Capabilities and would not need this trivial companion.
- `main.go` — boots a single gRPC server with both services
  registered.
- `validation_test.go` — in-process unit-style test that exercises
  the executor arm end-to-end over an in-process gRPC channel.
- `main_e2e_test.go` — cross-stack proof for
  STORY-validation-author: brings up a real rimsky stack via
  testcontainers, registers the example service as a peer, and posts
  templates that exercise each of the three observable outcomes
  (error blocks, warning passes-with-surface, accept passes
  silently).

## How rimsky wires this service

The operator declares the service in `rimsky.yml` under the
`claim_producers:` block, listing both protocols on the `protocols:`
field:

```yaml
claim_producers:
  validator:
    endpoint: "validator.example.svc:9400"
    protocols: [claim_producer, validation]
    write_semantics_allowed: [read_only]
```

At startup rimsky:

1. Dials the peer.
2. Calls `ClaimProducer.Capabilities` and caches the response. The
   Capabilities response advertises `protocols: [validation]` and
   `validation_supported_roles: [executor, claim_producer]`.
3. Walks the union of declared peers and, for each protocol on each
   peer's `protocols:` list, dials the matching gRPC client. The
   `validation` protocol's client is the `Validation` service stub on
   the same endpoint.

At template registration time (`POST /v1/templates` or
`POST /v1/templates/validate`), rimsky walks the template's nodes.
For each node, it looks up the per-protocol clients keyed by the
peer name the node references and, for each Validator that advertises
the role being validated, calls `Validate` with the role-specific
context built from the canonicalized template.

The validator returns a `ValidateResponse` with three observable
shapes:

- `valid=false` + a non-empty `errors` array → rimsky refuses the
  registration with HTTP 400; the body carries the findings under
  `validation_errors`.
- `valid=true` + a non-empty `warnings` array → rimsky accepts the
  registration (HTTP 201 on the full register surface; HTTP 200 +
  `ok=true` on the validate-only surface) and surfaces the warnings
  under `validation_warnings`.
- `valid=true` + empty errors/warnings → rimsky accepts the
  registration cleanly.

## Running the cross-stack proof

The cross-stack proof boots a real rimsky stack, so it requires
Docker and a locally-built `rimsky-all-in-one:latest` image:

```sh
make core-images                   # produces rimsky-all-in-one:latest
go test ./examples/validation -run TestE2E -count=1
```

The proof never relies on the in-process unit test for any of its
acceptance legs — every observation goes through the real
`POST /v1/templates` and `POST /v1/templates/validate` routes against
the assembled product.

## Copying this example

Two paths, depending on what your service already is:

- **Your service is already a claim-producer / executor / publisher
  / lifecycle-subscriber.** Copy `validation.go` into your service's
  source tree, replace the body of `Validate` with your own
  role-specific checks, register the `Validation` server on your
  existing gRPC server alongside the primary one, and add
  `"validation"` to the `protocols` field of your Capabilities
  response (plus the role names you implement to
  `validation_supported_roles`). Your operator-side config gains
  `validation` on the `protocols:` list for your peer.

- **You're writing a pure validator with no real primary protocol.**
  Copy the whole directory, rename the module, replace the bodies of
  `Validate` and `Capabilities` with your own. The `Producer` here
  is a trivial read-only claim-producer that exists solely to host
  the Capabilities handshake — adjust the
  `validation_supported_roles` list to match the roles your
  validator covers.
