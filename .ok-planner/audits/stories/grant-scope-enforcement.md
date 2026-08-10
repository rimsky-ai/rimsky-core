---
audit: grant-scope-enforcement
artifact: story:grant-scope-enforcement
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T05:45:00Z
---

# A tag-scoped delegation holds across the whole template lifecycle

Supported. An admin delegated to a per-tenant key whose grant scopes seven
actions to one template tag, and every scopeable action in the template
lifecycle was then driven twice from that key — once at the tag it owns, once at
a tag the admin owns. All eight pairs behaved: register, deploy, tag move, tag
delete, instance create by tag, instance create by the template's own content
hash, undeploy and deregister each succeeded in scope and were refused with 403
out of scope. Naming the out-of-scope resource by hash rather than by tag did not
evade the scope. The out-of-scope template survived every attempt, and the tenant
key remained a working key for what it was granted and was refused what it was
not.

## Compliance

The body names an internal component as the actor that refuses ("with the
permission matcher refusing requests against any other resource"); a story states
what the user observes, not which part of the system produces it. Compliant text:
"…I can scope an api-key's grant to a specific resource (e.g., a template-tag),
and requests against any other resource of the same action are refused across the
resource's full lifecycle, so that…".
