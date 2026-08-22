---
decision: host-agent-path-resolution-anchored-to-agent-cwd
---

# The host agent resolves binding paths against its own working directory

## Choice

The host agent resolves each binding's path at exec time against its own working directory, without a shell, and canonicalizes it through any symlinks. That one resolved path serves both the operator path-glob allowlist match and the exec. The spawn's declared working directory reaches the child process only (see `concept:host-agent`).

## Rationale

The allowlist is the operator's boundary on what the agent may run, so the path it matches must be the path the agent executes. Anchoring resolution to the agent's own working directory keeps a caller from steering a relative allow-glob onto a different binary of the same name, and canonicalizing symlinks closes the same gap through a link. Resolving without a shell keeps metacharacters in a path from becoming commands.

## Alternatives

- Resolve against the caller-supplied working directory — rejected: the caller then chooses which file a relative allow-glob names, so the operator's boundary bounds nothing.
- Match the allowlist on the declared path and exec the resolved one — rejected: the two can name different files, so the check and the exec disagree.
