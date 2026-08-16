---
audit: toplevel-dirs
artifact: decision:toplevel-dirs
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 4
unaccounted: 0
---

# Whether exactly four top-level code directories split the repo by role

Supported. The tracked repo root holds exactly the four code directories the choice names — binaries, library code, out-of-tree tests and their machinery, and dev tooling — and no fifth: the only other tracked directories are the image build inputs and the per-tag release notes, which carry no code, plus dotted tooling directories. Untracked build output is ignored by the version-control configuration and is not part of the layout. The wrapper convention the decision rejects is absent, and no root-level compat shim exists for any module. A fitness test fails if a shim directory reappears at the root and checks each module's manifest declares the path its directory implies, which is what keeps the library group the single import root.
