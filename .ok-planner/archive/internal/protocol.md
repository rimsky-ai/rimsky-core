# Rimsky Wire Protocols

> **This document is a pointer.** The authoritative source for the three Rimsky service protocols is `docs/specs/2026-05-04-service-protocol-contract.md`. That contract supersedes everything that previously lived here.

The Rimsky service-protocol surface is three protocols carried in the `protocols/` Go module (`github.com/fallguyconsulting/rimsky/protocols`):

- **`ClaimProducer`** — five methods (`Open` / `Commit` / `Abandon` / `Release` + `Capabilities()`). Foundation calls this at acquisition (`Open`), at executor terminal (`Commit` / `Abandon` / `Release` on non-held claims), and at auto-terminal (`Commit` / `Abandon` on held claims). Proto: `protocols/proto/v1/claim_producer.proto`. Go interface: `protocols/claimproducer/`.
- **`LifecycleSubscriber`** — six methods (`OnTemplateRegistered/Deployed/Undeployed/Deregistered`, `OnInstanceCreated/Terminated`). Modeling (control-api) calls this on template/instance state transitions. Proto: `protocols/proto/v1/lifecycle.proto`. Go interface: `protocols/lifecycle/`.
- **`Executor`** — four methods (`Execute`, `StreamTrace`, `GetTrace`, `GetCapabilities`). Foundation calls `Execute` at dispatch. Proto: `protocols/proto/v1/executor.proto`. Go interface: `protocols/executor/`.

Each protocol has a corresponding conformance binary:

- `cmd/rimsky-executor-conformance/` — Executor + LifecycleSubscriber.
- `cmd/rimsky-claim-producer-conformance/` — ClaimProducer.
- `cmd/rimsky-conformance-probe/` — utility helper used by `--require-stub-mode`.

For the wire shapes, Go interfaces, types, capability handshakes, conformance requirements, async-callback path, and out-of-scope notes, read `docs/specs/2026-05-04-service-protocol-contract.md` directly. The proto sources are the authoritative wire definitions; the contract doc maps each proto message to its Go interface counterpart.

## See also

- `docs/specs/2026-05-04-service-protocol-contract.md` — the authoritative service-protocol contract.
- `docs/specs/2026-05-04-foundation-contract.md` — what foundation calls into the protocols at.
- `docs/specs/2026-05-04-modeling-layer-contract.md` — what modeling calls into the LifecycleSubscriber protocol at.
- `docs/executor-author-guide.md` — guide for writing an Executor.
- `docs/claim-producer-author-guide.md` — guide for writing a ClaimProducer.
- `docs/glossary.md` — vocabulary reference.
