---
story: validation-author
---

# Service author writes validation mix-in

## Story

As a service author writing a validation mix-in, I can implement the validation protocol's single validation RPC and advertise it in my primary protocol's capabilities handshake, with rimsky calling my validator at registration time (for the relevant role context — executor / claim-producer / publisher / lifecycle-subscriber) and surfacing my findings to the operator as errors (blocking) or warnings (informational), so that I customize validation beyond rimsky's built-in shape checks.
