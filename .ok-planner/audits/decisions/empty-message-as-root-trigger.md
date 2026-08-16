---
audit: empty-message-as-root-trigger
artifact: decision:empty-message-as-root-trigger
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:42:19Z
---

# The implicit empty declared-type entry, its absent body schema, and the refusal of author-declared empty entries

Supported. Both places that derive a template's declared-types set add the empty entry unconditionally — the runtime's cached per-template set used by the substitution registry check, and the control API's send handler, which builds the accepted list starting from the empty string before appending the author-declared types. The entry has no body schema because it is not an author-declared message: the shared body-schema validator looks the type up in the template's message registry, finds nothing, and returns clean, which is also why there is no substitution surface — a substitution reference names a type, and the empty type has no name to write. Author-declared empty entries are refused at registration with an error naming the type as reserved for the runtime trigger, and two unit tests cover the refusal and the clean case. The uniformity the rationale claims holds under inspection: sweeping the runtime, control, and graph trees for a branch on an empty message type finds none in the receipt handler, the enqueue chokepoint, the delivery path, or the dead-letter audit, so the empty type flows through the same idempotency insert, the same envelope enqueue, and the same audit emission as any declared type. Neither rejected alternative exists — there is no wake endpoint, and no runtime-synthetic envelope type is constructed anywhere. The end-to-end behaviour is covered by a scenario suite that sends an empty message and asserts every root node wakes.
