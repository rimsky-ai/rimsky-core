---
decision: auth-anonymous-via-empty-key-ledger
---

# auth-anonymous-via-empty-key-ledger

## Choice

Rely on the existing `concept:anonymous-mode` behavior for loopback admission: a fresh per-run sqlite database has zero rows in the API-key ledger, so every request is admitted as a synthetic admin identity. The verb provisions no keys.

## Rationale

No new mechanism — the existing data-derived anonymous-mode rule does the right thing for a self-contained ephemeral run. Loopback-only binding (`decision:network-binding`) provides the network-layer isolation. Together, the run is reachable only from the host and serves every request as admin, which is the simplest one-shot model.

## Alternatives

- Provision an ephemeral admin key at startup and thread it to the client — rejected: extra generation-and-plumbing machinery for a loopback-only ephemeral run that anonymous mode already admits.
