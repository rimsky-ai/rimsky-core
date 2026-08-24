---
concept: sensor
---

# Sensor

## What it is

A sensor is a class of `concept:publisher` implementation that observes state outside rimsky. A sensor polls, listens, or otherwise watches a substrate rimsky does not run, and it publishes a message into rimsky when that substrate changes. A sensor implements the publisher protocol and sends each message to rimsky's one operator message-send surface, identifying itself as a publisher and presenting its per-subscription capability token. A sensor shares no protocol surface with rimsky beyond the publisher protocol.

## Purpose

A sensor carries a change in an external substrate into an instance's frames without rimsky-core knowing that substrate. The sensor observes, builds an opaque payload, and hands it to rimsky as a plain `concept:message`; rimsky then routes that message through the cascade machinery it already runs. A sensor observes and does not interpret: the payload bytes travel through rimsky unread until a consumer's substitution leaf walks into them (see `concept:inertness`).

## Boundaries

A sensor owns its watching loop, its dialect for the substrate it watches, the per-subscription progress it tracks inside its own process, and the message envelope it builds when it fires. A deployment runs a sensor as a standing service and declares it among its publishers (see `concept:rimsky-yml`), the same deployment model `concept:claim-producer` and `concept:executor` follow.

A sensor does not own the wire protocol, which is `concept:publisher`. It does not own the message envelope's shape, which is `concept:message`. It does not own the binding between an instance and a subscription, which is `concept:publisher-subscription`. What a sensor does with its own progress when rimsky rejects a send is settled by `decision:sensor-emission-permanent-drop-vs-transient-retry`.

See also: `concept:publisher`, `concept:publisher-subscription`, `concept:message`, `concept:inertness`, `concept:service-auth`, `concept:rimsky-yml`, `concept:claim-producer`, `concept:executor`.
