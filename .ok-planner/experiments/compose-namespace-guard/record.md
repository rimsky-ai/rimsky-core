---
experiment: compose-namespace-guard
commit: PENDING
---

# Who may create a compose-prefixed tag or instance key

## What it ran against

Two `rimsky-all-in-one` containers from this tree's image set. On the first,
`auth init` enabled authentication and an operator key was minted holding every
ordinary operational grant but not the compose-origin capability. The second was
left in the shipped default posture, with no keys — which is also the only
posture in which the compose CLI works, since it sends no credential.

## What was observed

On the authenticated deployment every attempt was refused with 400 and nothing
landed: tag create, template register carrying a compose-prefixed tag, and
instance create with a compose-prefixed key, over the HTTP API with and without
a spoofed `X-Rimsky-Compose-Origin` header; the same two creations over the MCP
JSON-RPC surface; the same tag create through the CLI; and both creations with
the admin key when the header was absent.

On the unauthenticated deployment the reservation does not hold. An ordinary
`urllib` client — not the compose CLI, carrying no credential — was refused only
while it omitted the header. Setting `X-Rimsky-Compose-Origin: 1` was enough:

    POST /v1/tags      {"tag": "compose:anon-intruder:sneaky@1"}   -> 201
    POST /v1/instances {"instance_key": "compose:anon-intruder:sneaky"} -> 201
    POST /v1/templates {"tag": "compose:anon-intruder:other@1"}    -> 200

All three landed and were readable back out of the store afterwards. The header
is a self-declaration, and on a keyless deployment nothing behind it
distinguishes the compose CLI from any other client. The same deployment
accepted `compose up`, which created `compose:guard-control:only@1`.

RESULT: FAIL (unauthenticated deployment accepts a non-compose client's
compose-prefixed creations)
