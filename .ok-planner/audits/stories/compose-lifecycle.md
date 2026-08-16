---
audit: compose-lifecycle
artifact: story:compose-lifecycle
text: noncompliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:59:54Z
checked: 5
unaccounted: 0
---

# The five things an operator does with a compose manifest

Supported across all five capabilities the story names — plan, inspect status,
apply and reconcile, namespace the resources, and tear down with one command. One
manifest declaring two templates, their tags, and two instances was driven
through the whole cycle against a running deployment. Planning listed all eight
steps before anything existed and named the namespaced identities it would
create; status reported every declared resource as missing from the deployment
beforehand and in-manifest afterwards; applying performed the eight steps, and
the tag, template, and instance listings then carried the compose prefix on every
resource with the templates deployed. Reconciliation was settled by a second
apply reporting no changes rather than repeating the work. One teardown command
then removed instances, deployments, tags, and templates in eight steps, and both
listings came back clean. The demonstration was taken on a deployment in the
shipped default posture, which is the only posture where these verbs work: the
compose verbs send no credential, so on a deployment with authentication enabled
they fail unauthorized under every key-passing mechanism the CLI offers, while an
ordinary verb with the same key succeeds. Eighteen checks, none failing.

## Compliance

- The body prescribes mechanism ("namespace the resources under a
  compose-prefixed tag") — the naming scheme is the decision's territory; the
  compliant text names the need, e.g. "keep the resources it manages
  distinguishable from ones I authored by hand".
