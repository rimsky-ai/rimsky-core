---
audit: jcs-cyberphone
artifact: decision:jcs-cyberphone
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:14Z
---

# Frozen version pin on the JCS canonicalization library

Supported. `go.mod` pins `github.com/cyberphone/json-canonicalization` at a specific pseudo-version, and `test/plumbline/jcs_pin_freeze_test.go` mechanically enforces the "moves only as a deliberate act" claim: it reads `go.work` to enumerate every workspace module manifest (all modules that `require` this library, checked across the full `go.work` module list), fails if the pinned version in any manifest differs from a frozen constant baked into the test, and separately fails if any manifest carries a `replace` directive for the module (which would silently substitute different canonicalization output bytes without changing the declared version). This directly encodes the decision's rationale that a routine bump changing output bytes would split template identity between persisted and freshly computed ids.
