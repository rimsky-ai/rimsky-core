---
story: validation-author
status: as-is
---

# Service author writes validation mix-in

## Story

As a service author writing a validation mix-in, I can implement the validation protocol's single validation RPC and advertise it in my primary protocol's capabilities handshake, with rimsky calling my validator at registration time (for the relevant role context — executor / claim-producer / publisher / lifecycle-subscriber) and surfacing my findings to the operator as errors (blocking) or warnings (informational), so that I customize validation beyond rimsky's built-in shape checks.

Public validation protocol surface — the validation protocol's single validation RPC (see `concept:validation`); advertised alongside a primary protocol's capabilities handshake; rimsky calls the validator at registration with the relevant role context and surfaces findings to the operator.

Service authors customize validation beyond rimsky's built-in shape checks, with the same blocking vs informational severity semantics applied consistently.
