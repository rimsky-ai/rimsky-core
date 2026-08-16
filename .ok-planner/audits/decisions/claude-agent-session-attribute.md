---
audit: claude-agent-session-attribute
artifact: decision:claude-agent-session-attribute
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:43:41Z
---

# The session token rides the attribute delta on Success and the scratch slot on Park

Supported. The executor's expected-attributes schema declares a session-token property as a read-only string defaulting to empty, and the Success path writes the current dispatch's run id into the attribute delta it returns, on both the real completion path and the stub one. The Park path carries no attribute delta at all in the callback body the executor builds, and instead base64-encodes the token into the park body's scratch field; a unit test asserts exactly that body shape. On dispatch, both entry points — the gRPC surface and the HTTP bridge — resolve the token through one two-line function that prefers the request's scratch and falls back to the carried-forward attribute, so the two legs converge without a caller-visible branch. An end-to-end scenario against a live stack drives a three-turn self-edge loop and checks that all three dispatches share one RunScope, that each writes its own run id into the attribute, that turns two and three resume the CLI with the previous dispatch's run id, and that a sub-graph invocation lands in a different RunScope with no resumed session and a fresh conversation — which is the sub-graph clause and, together with the shared-RunScope assertion, the intra-frame scoping the decision claims. The park leg's rate-limit trigger is exercised end-to-end by a separate cross-stack scenario that drives the node to parked, and rimsky's generic park-scratch round-trip has its own scenario suite.
