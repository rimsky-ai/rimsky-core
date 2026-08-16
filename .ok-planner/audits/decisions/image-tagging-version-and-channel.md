---
audit: image-tagging-version-and-channel
artifact: decision:image-tagging-version-and-channel
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 15
unaccounted: 0
---

# Whether every published image carries an immutable version tag plus one mutable channel tag, with dev separated from release

Supported. All 15 published images — 4 core and 11 bundled services — are pushed with two tags each: the derived version and a channel variable, checked line by line in the push target with no image carrying only one. The channel variable defaults to the release channel and the mechanical dev-release path overrides it to the dev channel for the whole chain, so the two streams never share a pointer. Local builds add a third, content-addressed source-tree tag alongside those two, which is a verification handle rather than a channel and is what the test harnesses resolve. A fitness test asserts the version tag, the release channel tag, and the dev channel override all remain.
