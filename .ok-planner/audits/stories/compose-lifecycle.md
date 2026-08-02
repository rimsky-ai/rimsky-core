---
audit: compose-lifecycle
artifact: story:compose-lifecycle
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:22Z
---

# Manifest-driven reconcile, namespace, plan/status, and teardown

Supported. `rimsky compose up` reads a manifest, queries live state, computes a plan against it, and applies register/tag/deploy/instance-create steps; `rimsky compose plan` computes and prints the same plan without applying (nonzero exit when there are pending changes); `rimsky compose status` annotates every manifest and live tag/instance as in-manifest, api-missing-from-manifest, or manifest-missing-from-api; `rimsky compose down` computes deletes for every manifest-scoped instance, tag, and template and refuses when any manifest instance is non-terminal. Every manifest-declared tag and instance name is namespaced under a `compose:<project>:` prefix before being sent to the server. An end-to-end test drives the full cycle — plan (pending), up, plan (clean), status (annotations correct for 2 templates + 2 instances), teardown after termination, down — and confirms down removes only the manifest's own tags/instances while leaving a manually-created tag and template alone, i.e. reconcile is project-scoped.
