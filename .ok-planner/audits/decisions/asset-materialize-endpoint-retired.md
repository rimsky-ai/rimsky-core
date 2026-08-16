---
audit: asset-materialize-endpoint-retired
artifact: decision:asset-materialize-endpoint-retired
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:42:19Z
---

# No materialize verb on any asset surface; the surviving asset surfaces intact

Supported. Sweeping the library, command, and test trees for any materialize verb turns up nothing outside the surviving history surface: there is no control-API route, no CLI subcommand among the five asset commands the binary registers, no MCP tool among the five the asset actions declare, and no action row among the 49 in the canonical registry — assets carry exactly two, a read action and a delete action. The surfaces the decision says survive all do: list, detail, versions, and materialization-history are registered as read routes with matching MCP tool names, and delete is registered as its own write route under its own action. The re-materialization path the decision points to instead exists and is exercised — the empty-message whole-instance trigger runs end to end in its own scenario suite, and author-declared subscription edges on producer nodes are the ordinary template mechanism, so nothing had to be invented to replace the retired verb. Neither rejected alternative is present: no runtime-injected materialize message type is declared anywhere, and no synthetic-envelope path survives — the only two message-enqueue call sites in the tree are the control API's send endpoint and the send-node's own dispatch.
