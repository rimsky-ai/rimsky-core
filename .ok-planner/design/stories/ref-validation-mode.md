---
story: ref-validation-mode
status: as-is
---

# Operator chooses registration-time strictness

## Story

As an operator setting up a staged bring-up where templates register before all referenced services exist, I can choose a registration-time reference-validation strictness mode — the all mode (refuse anything missing), the available mode (validate only what's provisioned), or the none mode (skip altogether) — with whatever a relaxed mode lets through caught at the mandatory instantiation gate, so that infra-as-code bring-up is an explicit operator choice rather than implicit heuristic.

Operator-selected registration-time reference-validation strictness (all, available, or none); the mandatory instantiation gate catches anything a relaxed mode let through.

Operators choose explicitly between strict bring-up safety and staged bring-up flexibility; the always-on instantiation gate ensures relaxed registration never produces a running instance with missing refs.
