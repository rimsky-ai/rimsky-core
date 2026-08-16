---
audit: event-log-kind-enum
artifact: decision:event-log-kind-enum
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:30:00Z
---

# Operational event kinds as a closed protocol enum, typed at the app boundary

Supported. The operational kinds are a closed enum of 48 values declared at the protocol layer, and the app-facing kind type is a sealed struct discriminating an operational family from a signal family, whose only constructors take a generated enum value — panicking on an unmapped one — or a signal type-path. The append input takes that struct and nothing else, so an application caller cannot write a raw string kind; that is the gate, and it holds by construction rather than by discipline. The persistence layer does the marshaling in both directions: writes render the typed value to its wire form, and both drivers' read paths parse the stored string back through the shared parser and fail the read with a logged unknown-kind error rather than passing the string through as control flow. Both read-API kind filters — the event listing and the audit listing — validate the caller's kind parameter through that same parser and reject an unknown one with a bad-request before the already-validated string reaches the query, which is the marshaling detail the decision permits. The persistence side carries no integrity-check constraint on the kind column in either dialect, exactly as chosen, and no registry table of kinds exists.
