---
audit: release-scan-docker-scout
artifact: decision:release-scan-docker-scout
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 15
unaccounted: 0
---

# Whether the release chain scans every built image with Scout and fails on unaddressed critical or high findings

Supported. The scan stage iterates one list naming all 15 locally-built images — the four core and the eleven bundled services, matching what the image-build targets produce — and runs the in-box Docker scanner against each at the release version, restricted to critical and high severity. It fails closed in both directions: it aborts up front when the scanner plugin is absent rather than passing silently, and it exits non-zero on the first image with findings left over after subtracting a small committed acceptance list of four identifiers, which is what makes the choice's word "unaddressed" accurate. The stage sits between the test suite and the push in the release chain, so a failing scan stops the release before anything is published, and no separately installed scanner appears anywhere.
