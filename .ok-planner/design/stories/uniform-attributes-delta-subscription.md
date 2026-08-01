---
story: uniform-attributes-delta-subscription
status: as-is
aliases: []
---

# Subscriber predicates on verdict attributes across terminal kinds

## Story

As a template author, I can write one subscription predicated on the attribute values the executor wrote with its verdict and have it fire uniformly whether the run succeeded or errored (`concept:signal`), so that I express "fire when the verdict carries this value" once instead of enumerating per-kind subscriptions or going blind to the same attribute when the producer errors.
