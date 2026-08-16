---
audit: env-var-convention-across-modes
artifact: decision:env-var-convention-across-modes
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:44:00Z
---

# One env-var convention across containerized and all-in-one modes, and the ephemeral wrapper's pass-through

Supported. Each of the six bundled handlers that ship both ways — four executors and two claim producers — exposes exactly one options loader that reads the process environment, and both call sites use it: the standalone binary's entry point in containerized mode, and the in-process bundled registration the all-in-one stack runs, so the env-var names are the same set by construction rather than by convention. The unified config carries no per-service config content: its executor, claim-producer, and publisher entries hold only transport, endpoint, TLS mode, protocol membership, and an observability endpoint, and the rejected opaque config-path field appears in no schema. The ephemeral-run env plumbing snapshots and restores only the variables it sets for the embedded stack, leaving operator variables destined for bundled handlers untouched, with a test that sets two such variables across the snapshot and asserts both survive.
