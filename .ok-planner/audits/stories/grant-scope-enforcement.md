---
audit: grant-scope-enforcement
artifact: story:grant-scope-enforcement
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:52:00Z
---

# A key scoped to one resource is refused against every other resource of the same action

Supported. An admin key delegated to a per-tenant key whose grant scoped seven
actions to one template tag and granted unscoped reads; the grant read back off
the key with all seven entries carrying that scope. Each of the seven scopeable
actions was then driven twice from the tenant key — once at the in-scope tag,
once at a second tag the admin owned — across the resource's whole lifecycle:
register, deploy, tag move, tag delete, instance create by tag, instance create
by the template's own content hash, undeploy and deregister. Every in-scope call
succeeded and every out-of-scope call was refused with 403, the hash-addressed
creation included, so naming the resource by id rather than by tag does not
evade the scope. The second template survived every out-of-scope attempt and
still read for the admin, and the tenant key remained a working key for what it
was granted (template list 200) while being refused what it was not (minting a
key, 403).

## Compliance

The body names an internal component — "with the permission matcher refusing requests" — which is a decision's territory, not the user's; compliant text states the outcome without the component, e.g. "with requests against any other resource of the same action refused, across that resource's full lifecycle".
