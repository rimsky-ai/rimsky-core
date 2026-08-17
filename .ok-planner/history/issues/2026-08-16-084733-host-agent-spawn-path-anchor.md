---
issue: host-agent-spawn-path-anchor
kind: audit
category: conflicting
artifacts:
  - concept:host-agent
status: promoted
opened: 2026-08-16T08:47:33Z
sprint: 2026-08-17-accepted-intake-drain.md
---

# The host-agent concept describes binding-path resolution the agent deliberately does not do

The host agent (a daemon that spawns local binaries on request from a rimsky deployment) resolves each binding's path before exec. Its concept says absolute paths are used as-is and relative paths resolve against the spawn's working directory. The code resolves — and symlink-canonicalizes — the path against the agent's own working directory, uses that one resolved value both for the operator's path allowlist match and for the exec, and applies the caller-supplied working directory only to the child process. A test names the reason: a relative allow-glob must anchor to the agent's own directory so a remote-supplied working directory cannot steer it onto a foreign binary of the same name. The concept states the insecure shape; the code has the secure one. The ruling decides the text.

Nothing in the code should move: the anchoring is the operator's protection against a compromised caller redirecting the allowlist.

## Options

- Rewrite the invariant to describe the anchoring (agent-relative resolution, one resolution for allowlist and exec, spawn cwd applied to the child only) and its reason; cost: none beyond the edit.

The ruling decides the wording; the tested behaviour is the deliberate one.

## Ruling

> Generated ruling (/verify-issues): Rewrite the concept's path-resolution invariant to say what the agent does — the binding path is resolved and symlink-canonicalized against the agent's own working directory before exec, that single resolution serves both the allowlist match and the exec, and the spawn's declared working directory is applied to the child process only — and say why: a caller-supplied working directory must not steer a relative allow-glob onto a foreign same-named binary. Forced by the current-state-only rule; the code's behaviour is tested and security-motivated, so the text moves. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
