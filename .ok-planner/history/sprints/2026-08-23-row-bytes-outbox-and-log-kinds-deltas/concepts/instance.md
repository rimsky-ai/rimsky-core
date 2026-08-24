---
concept: instance
---

# Instance

## What it is

An instance is one live deployment of a template. Rimsky mints its identifier at creation. An operator creates an instance through the control API, naming the template to bind, the initial params, and any attribute overrides. The binding names one template and holds for the instance's life. The instance carries a free-form params blob that node configurations substitute from, and optional per-instance, per-node attribute fragments. An instance is quiescent when no frame of it is running and no message waits on its queue.

## Purpose

A template declares the graph shape; an instance is the live runtime that shape runs in. Frames belong to an instance, and cascade resolves against one. Creating an instance materializes its per-node state and notifies the services that subscribe to instance creation; nothing runs until a sender posts a message to it.

## Boundaries

An instance owns its per-deployment runtime state: the params, the attribute overrides including match-based overlays, the late-bound service-binding catalog set at creation, the linkage to the api-key whose request created it (see `concept:api-key`), the paused state, the binding to a template, and the message queue that holds pending wake messages while a frame is in flight, together with the per-instance mode that decides whether pending messages accumulate or coalesce. It does not own the template spec (see `concept:template`), the instance's nodes, which carry their own instance reference (see `concept:node`), claim conflict, which scopes to `concept:supervisor`, or the frame currently running against the queue (see `concept:frame`).

Creation is the mandatory validation gate for statically knowable configuration. That validation reads routing keys, never attribute values (see `concept:inertness`). The creator's api-key linkage records ownership for audit and nothing else; routing to the serving host daemon reads a separate routing identity stamped at creation, which resolves the same way for an owner-created instance and an anonymous one (see `concept:host-daemon-proxy`, `concept:anonymous-mode`).

An instance is terminal exactly when its terminal timestamp is set, and it never sets that timestamp itself. The run-to-terminal verbs poll for quiescence and terminate the instances they created, and an operator terminates one directly (see `decision:termination`). Terminal is not removal: deleting an instance is a separate act, permitted only once the instance is terminal.

see also: `concept:template`, `concept:tag`, `concept:frame`, `concept:node`, `concept:message`, `concept:api-key`, `concept:host-daemon-proxy`, `concept:breakpoint`, `concept:control-api`.
