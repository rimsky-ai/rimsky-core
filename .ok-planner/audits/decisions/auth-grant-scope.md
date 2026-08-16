---
audit: auth-grant-scope
artifact: decision:auth-grant-scope
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:41:50Z
---

# Whether grants carry a per-entry scope map of action-specific dimension keys

Supported. A grant is a list of entries, and each entry carries its own optional dimension map alongside the action and the mode; the permission check walks the entries, keeps only those whose action pattern matches the request, and drops any whose dimension map is not satisfied by the request's target map — an empty map on the entry means unconstrained, and every declared key must be present and equal. Nothing about the shape is per-resource: an entry names no resource identifier, so the rejected alternative genuinely was not taken, and the dimension-key form means a grant written today constrains resources created tomorrow, which is the property the rationale claims. The dimension key the decision offers as its example is live rather than hypothetical: the control API builds a template-tag target from the request for the template register, deploy, undeploy, deregister, and tag verbs, and the tag-set verb checks it directly. Fifteen unit tests in the permission package cover the matching and the mode floor, and ten end-to-end scenario tests exercise the scoped grant across those verbs, including that a tag-scoped register grant cannot move a template to another tag without the tag-set grant.
