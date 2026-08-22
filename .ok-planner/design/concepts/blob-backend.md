---
concept: blob-backend
---

# Blob backend

## What it is

A blob backend is the store that holds a byte stream rimsky has spilled out of the row that would otherwise carry it. Two surfaces spill into a backend: attribute values and scratch. A backend reads and writes byte streams and names itself. A deployment picks one backend from several, and the backends differ in where they put the bytes. Every spilled byte stream is addressed by a handle that names the backend that wrote it, so a reader can tell which backend a handle belongs to. The name also tells a reader how to read a handle that resolves to nothing. A handle belonging to a backend that keeps bytes only for the life of the writing process names an absence once that process exits. A handle belonging to a durable backend names lost data.

## Purpose

A blob backend lets a large attribute value behave to its consumers exactly as a small one does. Spilling the large values out of their rows keeps rows small. The choice of backend puts the bytes where the deployment wants them.

## Boundaries

A blob backend owns the byte streams it stores, the threshold above which a value spills into it, the ledger of orphaned blobs, and the sweep that reclaims them. rimsky never inspects a blob's content: it reads a blob to hand the bytes to a consumer, and it branches on nothing inside them (see `concept:inertness`). A read whose handle names a backend other than the active one refuses (see `decision:blob-backend-mismatch-read-refused`).

Substituting an attribute value into a dispatch belongs to `concept:attribute`. Claim-payload bytes belong to `concept:claim-handle`.

see also: `attribute`, `inertness`, `persistence-database`, `claim-handle`
