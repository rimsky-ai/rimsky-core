---
audit: compose-namespace-guard
artifact: story:compose-namespace-guard
determination: unsupported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:25:00Z
---

# The compose namespace is not reserved to compose on a keyless deployment

Unsupported. On an authenticated deployment the promise holds: all 8 attempts by
a key without the compose-origin capability — tag create, template register with
a prefixed tag, and instance create with a prefixed key over the HTTP API, the
same two creations over the MCP surface, the tag create through the CLI, and both
creations by an admin key that omits the origin header — were refused with 400
and nothing landed. On a deployment with no keys, which is the shipped default
posture and the only one where the compose verbs work at all, an ordinary HTTP
client carrying no credential created a compose-prefixed tag, a compose-prefixed
instance key, and a template bearing a compose-prefixed tag, all accepted and all
readable back out of the store, simply by setting the compose-origin header on
its own requests. The header is a self-declaration any client can make, so in
that posture nothing at the server distinguishes the compose machinery from
anything else.
