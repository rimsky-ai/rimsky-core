---
concept: claim-lifetime
---

# Claim lifetime

## What it is

Claim lifetime is a per-claim property with two values. It decides how long a claim's handle lives after the claim settles. A subgraph claim, the default, settles when the subgraph holding it finishes; rimsky keeps its handle for a trailing window and then reaps it. A durable claim settles the same way. When a durable claim settles as successful, rimsky exempts its handle from that reaping: the handle stays until an operator releases it, or until someone deletes the instance that created it. Terminating an instance abandons the claims that instance still holds, and leaves a durable claim that already settled as successful in place (see `concept:claim-handle`, `concept:auto-terminal`).

## Purpose

A durable lifetime lets a claim's result outlive the run that produced it. A later dispatch can co-hold the same claim (see `concept:claim-co-holdership`). Where the producer handles typed data, the settled claim is what the asset surface presents and what a version query reads (see `concept:asset`, `concept:data-processing`). A subgraph lifetime serves the ordinary case, where a claim exists only to guard a resource while a subgraph runs.

## Boundaries

Claim lifetime owns the per-claim choice between the two lifetimes, and what that choice implies for how long the handle survives its settlement. It does not own the handle row itself, which belongs to `claim-handle`; the firing of a claim's terminal verb, which belongs to `auto-terminal`; the surface that presents a settled durable claim, which belongs to `asset`; or the typed-data protocol behind that surface, which belongs to `data-processing`. See also: `claim`, `claim-handle`, `asset`, `auto-terminal`, `data-processing`.
