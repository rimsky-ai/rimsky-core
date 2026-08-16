---
audit: graceful-shutdown
artifact: decision:graceful-shutdown
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T05:44:49Z
---

# Shutdown is polite-then-forceful with two hardcoded graces and a universal second-signal escalation

Unsupported. The core paths behave as described: the container entrypoint and the standalone role boot each wait for a signal, then install the escalator and drain behind a thirty-second deadline, hard-killing a spawned role child that outlives it; the CLI's compose path uses five seconds for both its child grace and its role-stack drain, sending a polite terminate first and a kill on expiry. Three claims fail. The grace is not hardcoded at two values: the bundled services shut their gRPC and HTTP surfaces on a third, ten-second window, and the host agent terminates the local binaries it spawned on a grace of its own. That grace is configurable — read from an environment variable with a thirty-second default — against the decision's statement that neither window is configurable. And the second-signal escalation is not universal: exactly three call sites install the escalator, out of twenty-two production signal-handling entry points, and the nineteen that do not — the ten bundled-service mains, the host agent and its proxy, the migrate binary, and the CLI's long-running verbs — install a signal handler that consumes the first signal and never rearms, so a second interrupt on those processes is swallowed rather than escalated, which is worse than the decision's claim rather than merely short of it. Counted from every production file registering a signal handler.
