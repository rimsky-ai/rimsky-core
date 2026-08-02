---
audit: cli-spawn-mechanism
artifact: decision:cli-spawn-mechanism
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:22Z
---

# claude-agent spawns the `claude` binary via `os/exec`

Supported. The claude-agent handler's runner constructs the CLI invocation and spawns it with the Go standard library's `os/exec.Command` directly — no SDK, RPC, or embedding runtime sits between the handler and the child process; the handler owns argument/environment composition, temp-file setup for the system prompt and MCP config, and post-exit cleanup, while the spawned binary owns the session protocol and tool loop. A test spawns the real binary end-to-end and asserts on delivered stdout/exit-code, and further tests confirm the subprocess mechanism's process-group signal handling (SIGTERM, SIGKILL of the whole group) and real-filesystem side effects (system-prompt and MCP-config files a real child process reads back).
