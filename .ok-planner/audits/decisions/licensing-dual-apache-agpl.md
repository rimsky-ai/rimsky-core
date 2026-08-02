---
audit: licensing-dual-apache-agpl
artifact: decision:licensing-dual-apache-agpl
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:39:17Z
---

# The repo is split Apache/AGPL exactly along the protocols+examples vs. everything-else line

Supported. `licensing.yml` classifies `lib/protocols/` and `examples/` as `apache` and `lib/foundation/`, `lib/graph/`, `lib/control/`, `lib/runtime/`, `lib/services/`, `cmd/`, `test/`, and `tools/` as `agpl` — an exact match, checked against all 8 top-level code directories under `lib/` plus the four `decision:toplevel-dirs` groups, to the split the decision names. `COPYING.md`, `NOTICE`, `LICENSE.apache`, and `LICENSE.agpl` are all present and consistent, and `COPYING.md` documents the Fall Guy Consulting commercial license as the AGPL alternative, matching the rationale.
