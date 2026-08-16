---
audit: auth-dry-run-request-flag
artifact: decision:auth-dry-run-request-flag
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:24:31Z
---

# A per-request dry-run flag previews writes on the real write path

Supported. The action gate reads a `dry_run` query parameter on every gated request, rejects any value other than `true` or `false` before the handler runs, and puts the resulting mode on the request context; handlers read it back and answer with a uniform dry-run envelope naming what they would have done instead of mutating. Enumerated the control API's action registry from source: 23 of its actions are marked as writes, and every one of the 23 has a dry-run branch in its handler — instance create, terminate, pause, resume, kill and debug-override; breakpoint create, resume and delete; template register, deploy, undeploy and deregister; tag create, set and delete; node reset; message send; lineage prune; asset delete; and auth key create, revoke and rotate. Reads still execute under the flag, which is the intended shape. There are no separate validation verbs paired with the write verbs — the rejected alternative — save the one deliberate read-only template-validate action. Tests cover the flag setting the mode, an invalid value being refused before dispatch, `false` executing, and a dry-run write leaving no row behind over both the HTTP and MCP surfaces.
