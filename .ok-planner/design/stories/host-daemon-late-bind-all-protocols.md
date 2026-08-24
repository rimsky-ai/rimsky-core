---
story: host-daemon-late-bind-all-protocols
---

# Executor and claim-producer work through late-bind

## Story

As a template author wiring a workflow against locally-running executor and claim-producer binaries, I can run the host-daemon on my dev machine connected to a remote rimsky stack, declare bindings for those two protocols, and have rimsky dispatch through the proxy to spawned local children identically across both, so that I exercise the assembled product against local code without rebuilding images.
