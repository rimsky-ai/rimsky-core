---
audit: claim-producer-protocol
artifact: story:claim-producer-protocol
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# A custom producer plugs into a stack and rimsky orchestrates claims against it

Supported. A producer written against the published protocol was started five
times on loopback — one per advertised write-semantics value and one that always
answers Open with Unavailable — and a stack was pointed at all five through the
rimsky.yml claim-producers block. The control API lists each producer carrying
the error class it declares in its capabilities response, so the startup
handshake arrives. Four nodes, one per producer, each settled fresh: Open
reached the producer with the selector resolved from the instance parameter, the
declared intent, the declared alias and the node's opaque data blob unchanged;
the successful write claim was closed with Commit and the read claim with Commit
as well. Each producer's returned address, scope bytes and payload field reached
its node's dispatch, and the four claim-handle rows record the four realized
write-semantics values, one per producer. The producer that answers Unavailable
settled its node on the error class it declares rather than on a generic
acquisition failure.
