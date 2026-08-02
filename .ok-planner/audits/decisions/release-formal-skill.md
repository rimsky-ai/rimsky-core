---
audit: release-formal-skill
artifact: decision:release-formal-skill
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:22Z
---

# The formal-release skill's governing text carries SemVer judgment, notes drafting, and exactly one confirmation gate

Supported. Read `.claude/skills/release/SKILL.md` (481 lines) in full: step 2 performs diff-based SemVer classification, steps 3–5 bump `lib/protocols/package.json` and draft plus review `releases/vX.Y.Z.md`, step 6 is the single named operator checkpoint (`Reply: go | revise <what> | abort`) presenting the bump rationale, full notes, and the action manifest together, and steps 7–8 run the entire outward push (commit, tag, `make release`, git push, npm publish, GitHub Release, main fast-forward) without further prompting. The only other places the flow can stop are error-path bail-outs (step 1 preflight failures, step 7 sub-step 1 non-mechanical lint failures, step 7a non-mechanical CVE findings) — these are failure handling, not a second planned confirmation gate, consistent with the decision's rejected alternative of "a confirmation gate at every step."
