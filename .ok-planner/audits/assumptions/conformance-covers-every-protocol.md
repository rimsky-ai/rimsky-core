---
assumption: conformance-covers-every-protocol
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# there is a conformance subcommand for every protocol a third party can implement, including the host-agent protocol and executor-observability.

As third-party service author, I would take it that there is a conformance subcommand for every protocol a third party can implement, including the host-agent protocol and executor-observability.

## Source

sibling-symmetry — eight `rimsky conformance` subcommands against ten proto files

## What a run would observe

enumerate `rimsky conformance --help` and match the subcommands against the ten protocols.

## Measured

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
