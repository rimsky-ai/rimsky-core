---
concept: control-api
---

# Control API

## What it is

The control API is the operator-facing interface of a rimsky deployment, served by the control-api role. It exposes one operation set through more than one protocol skin on a single listener. The operations cover template registration, instance lifecycle, per-instance breakpoint control, the auth surface, observability reads, and admin diagnostics. One skin is a direct request-and-response surface for scripts and operator tooling. Another is an agentic-tool surface, whose catalog the control API computes from the canonical action registry and filters by the permission grant of the key that asks. Every skin passes through the same auth and permission gate. The control API stages a lifecycle delivery for every template and instance transition it commits, in the transition's own transaction; the delivery runs after the transition commits and never decides the request's outcome (see `decision:lifecycle-fanout-after-commit`).

## Purpose

The control API is the one surface every client drives a deployment through: an operator at a terminal, the rimsky CLI, and an agentic client alike. Carrying several skins over one operation set lets a new kind of client reach every operation without the project writing the operations twice. An operator scripts and exposes the plain request-and-response skin, while a daemon discovers the same catalog and dispatches tool calls against it.

## Boundaries

The control API owns the operation surface and its handlers, the observability read handlers, the auth gate, and the auth operations. It owns the lifecycle staging for template and instance transitions, and for the run-scope terminals of an administrative termination — that staging covers every remaining scope in each frame's tree. Like every role, it runs a drain that delivers staged lifecycle rows to services. It owns the agentic-tool envelope handler and its catalog, and it hosts that skin in process, so a tool invocation re-enters the same operation surface without a network round trip back to itself. Where the deployment enables mutual TLS between services, the control API also owns the enrollment operation, where a key holding the enrollment permission exchanges its identity for a short-lived certificate and the certificate-authority root. The control plane hosts the per-deployment certificate authority, and the other trust boundaries defer to it as the identity authority (see `concept:service-auth`).

The control API does not own dispatch, which belongs to the supervisor, or scheduling, which belongs to the scheduler. It does not own the run-scope terminals of a settling frame, which the scheduler stages when the frame settles, nor those of the child scopes a sub-graph or fan-out partition closes at rendezvous, which the supervisor stages. It does not own the protocols the out-of-process services speak, nor the certificate lifecycle on the service side (see `concept:service-auth`).

see also: `rimsky`, `supervisor`, `lifecycle-subscriber`, `observability`, `cascade-graph`, `instance`, `template`, `api-key`, `permission`, `service-auth`
