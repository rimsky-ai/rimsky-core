---
concept: named-lock
---

# Named lock

## What it is

A named lock is a capacity counter that no producer owns. An operator declares each named lock in the deployment's configuration under a name and a single capacity limit. A limit of one makes the lock a mutex. A limit above one makes it a counting semaphore. The mode follows from the limit, and nobody declares it separately. A template refers to a named lock by name alone.

## Purpose

A named lock bounds concurrency on something the data does not describe. Some limits belong to the deployment rather than to any piece of data: how many runs of one template may proceed at once, or that one job proceeds alone. A named lock gives a template that coarse limit without a claim producer.

## Boundaries

A named lock owns its per-name capacity declaration in the deployment's configuration, its rows in the claim-handle ledger, and how rimsky disposes of its counter at terminal. It does not own scope conflicts, which belong to a claim (see `claim`). It carries no write semantics, because it guards no data (see `write-semantics`). See also: `claim`, `claim-handle`, `claim-scope`, `advisory-lock`.
