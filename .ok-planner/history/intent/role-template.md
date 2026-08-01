# Intent Dossier: role-template

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

All record on this concept is artifact-tier.

- A role template is a **CLI-bundled JSON resource** that expands into a permission grant at key-creation time. Roles are CLI-side expansion only: the server stores the raw grant and records no role identifier — "Server doesn't know about roles" (2026-05-15).
- The bundled set is six templates: the original five — admin (`*`), operator (full operational access plus auth:read, no key mutation), read-only (`*:read`), agent-supervisor (`*:read` plus node:invalidate, node:reset, message:send), publisher-service (message:send only) (2026-05-15) — plus debug-operator (2026-05-24).
- Operators add custom roles via **local role files**, not any server surface (2026-05-15).
- `audit:read` joins the read-only, operator, and admin role templates (2026-05-29).

## Required behaviors (open promises)

- CLI-side JSON expansion at key-creation time; server stores the raw grant, no role identifier — "Role template — CLI-bundled JSON resource... that expands into a permission grant at key-creation time. Server doesn't know about roles." (2026-05-15, control-plane-mcp-and-auth, artifact)
- The five v1 templates with the grants listed above (2026-05-15, control-plane-mcp-and-auth, artifact) (artifact-only for the exact grant contents).
- debug-operator bundles `*:read` plus the five debug write verbs (instance:pause, instance:resume, breakpoint:create, breakpoint:resume, breakpoint:delete) and is flagged high-risk in production — "grant only to operators or agent keys that need to halt or mutate live dispatches." agent-supervisor's permission set is deliberately unchanged: agent keys read breakpoint hits via `*:read` but cannot create/resume/delete breakpoints or pause/resume instances without an explicit debug-operator grant (2026-05-24, instance-debugger, artifact) (artifact-only).
- `audit:read` — a distinct action from event:read because audit data (actor identity, IP, user-agent, actions) is sensitive enough to grant separately — is part of the read-only, operator, and admin templates (2026-05-29, console-upstream-auth-audit-and-fixes, artifact) (artifact-only).
- End-to-end enforcement: "a CLI-bundled role JSON expands into a permission grant at key-creation time and the minted key enforces exactly that grant" — each role's representative action returns 200 and a non-role action 403 through the real gate (2026-06-02, acceptance-coverage-recovery, artifact).

## Intentional absences

- **Server-side role storage or role identifier** — by design the server sees only the expanded grant (2026-05-15, control-plane-mcp-and-auth).
- **Server surface for custom roles** — custom roles are local role files only (2026-05-15, control-plane-mcp-and-auth).
- **Breakpoint/debug verbs in agent-supervisor** — deliberately withheld; debug capability requires an explicit debug-operator grant (2026-05-24, instance-debugger). Note the debug-operator bullet above names five write verbs while the same artifact's verb registry lists breakpoint:read as a new action — breakpoint:read is covered by `*:read`, not a debug-operator-exclusive verb.
