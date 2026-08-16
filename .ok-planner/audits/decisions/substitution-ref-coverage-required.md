---
audit: substitution-ref-coverage-required
artifact: decision:substitution-ref-coverage-required
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:38:09Z
---

# Registration rejects any substitution ref no subscription entry covers

Supported. Template validation extracts every directive that names a sender — the node and message prefixes, the only two of the grammar's six source kinds that have one — indexes each receiver's declared subscription entries by sender and type, and emits a structured rejection for any ref the index does not match; a template with such a rejection is not valid, and both registration and the validate endpoint return the entries rather than accepting the spec. Both read shapes the decision names are handled: a per-field read is covered by a changed-attribute entry for that field or by the attribute wildcard, and a whole-pull read is covered only by the wildcard. No cascade edge is ever synthesised to paper over an uncovered ref — the edge builder inserts only entries the author wrote plus the structural-root wake. Extraction reaches beyond the attribute schema to claim-producer selectors, lock names, and the fan-out partition request, so the checked population is a superset of the claim. Unit tests cover the per-field and whole-pull rejections, the message-ref symmetry, wildcard acceptance, and the retired event form.
