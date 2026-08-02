---
audit: licensing-enforced-by-license-lint
artifact: decision:licensing-enforced-by-license-lint
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095813-license-lint-ignores-third-party-imports
---

# License-import discipline

Unsupported. The license-check tool's import inspection explicitly skips every import outside the project's own module prefix before any license classification happens, so it never inspects a third-party dependency's license at all — it enforces internal import direction only. Checked every other tool in the repository's build and lint configuration for a third-party license check; none exists. The decision's rationale explicitly claims its chosen mechanism avoids exactly this blind spot relative to a plain import-path deny rule, but the actual mechanism is that plain deny rule, scoped to internal packages only.
