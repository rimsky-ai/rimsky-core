---
audit: sensor-webhook
artifact: story:sensor-webhook
determination: supported
commit: b767a27d
audited: 2026-08-02T09:29:02Z
---

# Bundled webhook sensor exposes authenticated inbound routes that forward to the target instance

Supported. `lib/services/sensors/sensor-webhook` mounts a catch-all POST route per `chi.Mux`, binds each `Subscribe`'d `path_prefix` to a watch (longest-prefix-wins routing, verified by `TestDispatchWebhook_RoutesSubPathsUnderDeclaredPrefix` / `TestDispatchWebhook_LongestPrefixWinsAmongOverlappingSubscriptions`), authenticates every inbound POST per the subscription's required `auth` block before accepting it (`serveWebhook` calls `authenticate` first, rejecting unauthenticated or misauthenticated requests with 401/413), and forwards the decoded body to the subscription's target instance via `postMessage`, addressed by `w.InstanceID` — no polling involved, confirmed end-to-end by `TestSubscribe_MountsRouteAndForwards`.
