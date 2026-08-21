---
concept: message-schema
---

# Message-schema

## What it is

A message-schema is a template's registry of the message types instances of that template accept. The template declares it at its top level, alongside its per-node attribute declarations and its publisher declarations. Each entry pairs a message type-path with the body shape a message of that type carries, declared as a schema.

## Purpose

The registry gives messages a typed contract in place of an opaque envelope. An instance that receives a message of an undeclared type refuses it and names the type as unknown. The declared body shape is what a receiver substitutes from and what a message-sender node matches its attribute schema against, and one engine resolves both surfaces.

## Boundaries

A message-schema owns the registry's persisted shape, each entry's type-path and body-shape declaration, the registration-time validation pass, and the receipt-time lookup that gates an arriving message on a declared type. It does not own the message envelope (see `concept:message`), the message-sender node kind (see `concept:message-sender-node`), receiver-side subscription (see `concept:node-subscription`), or substitution into a body (see `concept:attribute`).

see also: `concept:message`, `concept:message-sender-node`, `concept:node-subscription`, `concept:attribute`, `concept:template`.
