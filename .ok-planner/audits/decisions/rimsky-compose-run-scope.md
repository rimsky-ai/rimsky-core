---
audit: rimsky-compose-run-scope
artifact: decision:rimsky-compose-run-scope
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:22Z
---

# `compose run` stays manifest-only; the single-template case lives in `rimsky run`

Supported. `compose run`'s flag parser accepts exactly one positional argument, always loaded as a compose manifest (`LoadManifest`) — there is no flag or autodetection path that accepts a bare template file, so a template can only reach a self-hosted stack through the separate `rimsky run` verb (see `decision:rimsky-run-self-hosts-templates`, audited alongside this one). `compose up`, `down`, `plan`, and `status` are unchanged by the one-shot verb's introduction: all 4 still require an already-reachable endpoint and operate on the same manifest-shaped input as before.
