---
audit: sqlite-modernc-pure-go
artifact: decision:sqlite-modernc-pure-go
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# Whether SQLite persistence runs on the pure-Go driver with no CGO dependency

Supported. Two module manifests — root and foundation — require the pure-Go driver at one identical version, a manifest fitness test fails if that pin disappears, and the CGO-based incumbent appears in no manifest and no source file. The SQLite persistence package registers that driver and uses its error type directly, and it is the only SQLite driver in the tree. The CGO-free consequence is real rather than asserted: no package in any of the four modules imports the C pseudo-package, and every image definition that compiles Go disables CGO explicitly, so the SQLite-backed binaries are the static ones the rationale describes.
