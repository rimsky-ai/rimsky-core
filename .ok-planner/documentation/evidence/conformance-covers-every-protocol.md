---
trap: conformance-covers-every-protocol
release: d977250c
---
# Evidence set — there is a conformance subcommand for every protocol a third party can implement, including the host-agent protocol and executor-observability.

Source of the prior: sibling-symmetry — eight `rimsky conformance` subcommands against ten proto files

## What the audit ran and observed (assumption record)

The experiment `assumption-conformance-covers-every-protocol` enumerated the
kit and asked for the missing subcommands. `rimsky conformance` offers eight:
`executor`, `claim-producer`, `publisher`, `validation`, `data-processing`,
`blob-backend`, `lifecycle-subscriber` and `probe` — and two of those cover
something other than a protocol. The shipped set is ten proto files declaring
nine gRPC services. `rimsky conformance host-agent` printed
`unknown subcommand "host-agent"` and exited 2, as did `hostagent`,
`host_agent` and `agent`: nothing in the kit proves a HostAgent implementation.
The prior's other named case holds by a different route than it expects.
`executor-observability` and `claim-producer-observability` are not
subcommands either, but both protocols are covered by `--check-observability`
on their sibling's subcommand, and the flag really runs — against the bundled
http-node it read the capabilities, the declared schema and tags, the
evicted-trace shape and a canned dispatch over `GetTrace` and `StreamTrace`,
printing `observability: ok`. A third-party author implementing the host-agent
protocol has nothing to prove it against.

## Experiment record (experiment:assumption-conformance-covers-every-protocol)

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

Runnables: `src:.ok-planner/experiments/assumption-conformance-covers-every-protocol/` at the stamped commit.
