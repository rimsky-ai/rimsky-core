---
audit: release-formal-skill
artifact: decision:release-formal-skill
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:21:13Z
---

# The formal-release skill carries judgment, notes, and push behind one gate

Supported by reading the two governing texts. The formal-release skill exists as a slash-command skill guarded to explicit invocation, and its flow is exactly the three parts the decision names plus a preflight: diff inspection producing a SemVer bump with a written rationale, a notes draft, an internal reviewer-subagent loop, then a single consolidated confirmation presenting the bump, the full notes, the outward-action manifest, and any judgment questions, answered go / revise / abort. Every outward action — commit, tags, the build-scan-push chain, the git push, the npm publish, the GitHub release, the fast-forward of the stable branch — sits after that one confirmation with no further prompt, and the abort path reverts the working-tree mutations made before it. The release guide describes the same eight steps and the same single gate, and points at the skill as the full prose. The mechanical dev path is a separate build target the skill rejects by flag, so the two paths do not overlap. The only other operator interactions are exception bails — a failed preflight, a non-mechanical lint failure, an unremediable scan finding — not routine gates.
