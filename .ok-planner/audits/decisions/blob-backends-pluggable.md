---
audit: blob-backends-pluggable
artifact: decision:blob-backends-pluggable
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:30:00Z
---

# One blob-backend interface across four backends

Supported. A single five-method backend interface — write, read, ranged read, delete, and a self-naming method — is implemented by exactly the four backends the decision names and by no others: inline, large-object, filesystem, and memory, each asserting the interface at compile time and each returning its own name. The open path selects among exactly those four from a configured backend string and rejects anything else, and the validator admits the same closed set, so the four in the decision and the four in the code are the same four. The large-object backend additionally implements a transaction-aware extension of the same interface, which is a widening rather than a second abstraction. The rejected alternative is absent: no code path stores a spilled payload in the row unconditionally, because the inline backend is itself one of the four selectable options rather than a hardcoded floor.
