---
concept: host-daemon-proxy
---

# Host daemon proxy

## What it is

The host daemon proxy is a rimsky-stack service (see `concept:service`) that stands between a deployment and a developer's machine. It presents rimsky's service protocols on the supervisor-facing side, one handler per protocol registered on one server. It maintains long-lived daemon connections on the dev-facing side, over a protocol of its own. That dev-facing hop runs TLS in every posture (see `decision:host-daemon-proxy-tls`). The proxy routes each dispatch to whichever daemon is connected under the routing identity stamped on the instance the dispatch belongs to. A deployment declares the proxy in its rimsky configuration once per protocol the proxy serves (see `concept:rimsky-yml`), every entry naming the same binary.

## Purpose

The proxy lets rimsky dispatch work to binaries on a developer's machine, declared per instance, while the supervisor and the graph-processing layers see only the standard service protocols. It is the one place that knows daemons exist: it dispatches to them and rewrites the callback URL a spawned process needs. Everything above it speaks the platform's own vocabulary.

## Boundaries

The proxy owns the daemon-facing protocol and the spawn lifecycle behind it (see `decision:proxy-single-spawn-multiplexing`). It owns the per-instance cache of service bindings, filled from lifecycle notifications (see `concept:lifecycle-subscriber`), refilled from the control API on a miss, and evicted when the instance terminates — each notification arrives through the lifecycle outbox, so the reap of a closed run scope's spawns follows the scope's close by the drain's delivery and is retried if the proxy was unreachable — so entries neither accumulate over a long-lived proxy nor let a terminated instance shadow a later one that reuses a binding name. It owns the per-protocol handlers that forward to spawned processes, and the callback URL it rewrites so a spawned process posts to the daemon's local listener instead of dialing the supervisor; that callback URL is the only URL the proxy rewrites. It resolves every dispatch through one routing identity (see `decision:host-daemon-proxy-uniform-routing-identity`). Its sanctioned late-bind surface is `concept:executor` and `concept:claim-producer` (see `story:host-daemon-late-bind-all-protocols`), and it checks a late-bound executor's resolved attributes per dispatch, against the schema that binary advertised when it spawned (see `decision:host-daemon-late-bind-schema-check-deferred`).

The proxy does not own the rimsky-side service protocols themselves, which are `concept:executor`, `concept:claim-producer`, and the other service protocols. It does not own the supervisor's dispatch logic, the failure vocabulary a dispatch surfaces in (see `decision:host-daemon-proxy-error-vocabulary-reuse`), per-instance state (see `concept:instance`), or the lifecycle-subscriber wire protocol (see `concept:lifecycle-subscriber`). See also `concept:host-daemon`, `concept:service`, `concept:service-auth`, and `concept:anonymous-mode`.
