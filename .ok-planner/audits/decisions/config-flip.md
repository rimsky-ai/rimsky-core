---
audit: config-flip
artifact: decision:config-flip
determination: supported
commit: b767a27d
audited: 2026-08-02T09:36:49Z
---

# Every historical Plumbline check activation in this repo landed with, never before, its clean sweep

Supported as a historical/process claim: this is a non-code-enforced practice, checked against the full history of the pre-migration `.plumbline.json` (predecessor of the current `.ok-plumbline/config.json`) via `git log --all --follow -p`. Across the file's whole history there are exactly 3 inactive-to-active check flips (`source_validity`, `blessed_invariant_test_coverage`, `comment_hygiene`); each one flips `false`→`true` in the same commit that drives that check's violation count to zero (commits `bf5ca897`, `c4f80d74`, `61e3b3b4` respectively — each commit message states the sweep completed and the check is now enabled, and the config diff for `true` lands in that identical commit, not a later one). No commit in the file's history shows a check flipped to active while a preceding commit still carried a nonzero backlog for it, so activation never precedes clean state for any of the 3 checks that were ever toggled in this project.
