---
story: validation-author
status: as-is
---

# Service author writes validation mix-in

## Role

As a service author writing a validation mix-in, I can implement the gRPC `Validation` server (single `Validate` RPC) and advertise it in my primary protocol's capabilities handshake, with rimsky calling my validator at registration time (for the relevant role context — executor / claim-producer / publisher / lifecycle-subscriber) and surfacing my findings to the operator as errors (blocking) or warnings (informational), so that I customize validation beyond rimsky's built-in shape checks.

## Capability

Public `Validation` protocol surface (single `Validate` RPC); advertised alongside a primary protocol's capabilities handshake; rimsky calls the validator at registration with the relevant role context and surfaces findings to the operator.

## Business value

Service authors customize validation beyond rimsky's built-in shape checks, with the same blocking vs informational severity semantics applied consistently.

## Acceptance

A service implementing `Validation` (alongside its primary protocol), registered with rimsky's catalog, has its validator called on template registration with the role context appropriate to where it's referenced; findings the validator returns as errors cause the registration to be refused with the finding surfaced to the operator; findings returned as warnings are surfaced without blocking.

## Falsifier

Error-severity finding doesn't block registration, OR warning-severity finding blocks registration, OR validator is registered but `Validate` is never called.

## Proof

Example — a shipped validation reference paired with a worked walkthrough.
