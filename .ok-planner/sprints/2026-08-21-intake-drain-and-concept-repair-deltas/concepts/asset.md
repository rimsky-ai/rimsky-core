---
concept: asset
---

# Asset

## What it is

An asset is a documented compound, not a new primitive: a committed claim, held against a producer that advertises the data-processing capability, whose lifetime is durable. A claim satisfying all three is an asset; any other claim is not, and rimsky applies asset semantics to no other claim. Rimsky surfaces an asset by finding the claim handles that meet the three conditions, so no separate asset record exists.

## Purpose

An asset gives an operator a durable, named handle on the data an instance produced, and does it without adding a record type to the platform. Because an asset is a claim, its versions, its materialization history, and its release all come from the claim machinery already in place.

## Boundaries

An asset owns the compound definition and the surface through which an operator observes and removes assets (see `story:asset-management`). It owns no new primitive: an asset is a claim, and its durability is a claim lifetime. It does not own re-materialization, which an operator asks for by sending a message — an empty one to wake the whole instance, a typed one to take the partial path the template author designed. It does not own whatever a template declares for the producer's own use inside an asset; that block is producer-targeted and stays opaque to rimsky.

see also: `claim`, `claim-lifetime`, `claim-handle`, `data-processing`, `lineage`
