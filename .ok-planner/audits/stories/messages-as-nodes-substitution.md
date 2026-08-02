---
audit: messages-as-nodes-substitution
artifact: story:messages-as-nodes-substitution
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# `{{messages.<type>.<field>}}` works anywhere `{{nodes.<type>.<field>}}` would, through the same lookup and distinct registration checks

Supported. The template-validator's substitution-ref scanner (`parseSubstitutionRefsFromAttributes`) walks the same four surfaces — attribute schema, `claim_producers[].selector`, `locks[].name`, `fan_out.partition_request` — for both `nodes.` and `messages.` directives uniformly, and a dedicated symmetry test (`TestCoverageCheck_SymmetryWithNodes`) registers one template using `{{nodes.foo.attribute.items}}` in a `fan_out.partition_request` and a second using `{{messages.ev/foo.items}}` in the identical position, both validating cleanly. Registration-time validation is checked to differ exactly as the story specifies: `nodes.<type>` refs are checked against the map of declared node types (`ValidateTemplate`'s `declared` map) while `messages.<type>` refs are checked against the declared `messages:` registry, each enforced by its own passing/failing unit tests (`TestCoverageCheck_MessagesUndeclaredRejected`, `TestCoverageCheck_MessagesDeclaredAccepted`).
