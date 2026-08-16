---
experiment: assumption-conformance-covers-every-protocol
commit: PENDING
---

# Matching the conformance subcommands against the shipped protocols

## What it ran against

The shipped `rimsky` CLI at this tree, plus one `rimsky-executor-http-node`
container so the observability probe has something to answer it. The run
enumerates `rimsky conformance`'s usage line, asks for the subcommands the
protocol set suggests, and drives `--check-observability` end to end.

## What was observed

`rimsky conformance` offers eight subcommands: `executor`, `claim-producer`,
`publisher`, `validation`, `data-processing`, `blob-backend`,
`lifecycle-subscriber` and `probe`. The shipped protocol set is ten `.proto`
files declaring nine gRPC services — `events.proto` declares none — so
`blob-backend` and `probe` cover something other than a protocol.

`rimsky conformance host-agent` printed `unknown subcommand "host-agent"` and
exited 2; `hostagent`, `host_agent` and `agent` are not subcommands either.
Nothing in the kit proves a HostAgent implementation.

`executor-observability` and `claim-producer-observability` are also not
subcommands, but both protocols are covered by a flag on their sibling's
subcommand. `rimsky conformance executor --check-observability` against the
bundled http-node read the capabilities, the declared attribute schema and
tags, the evicted-trace shape, and a canned dispatch over both `GetTrace` and
`StreamTrace`, printing `observability: ok` and exiting 0.
