---
audit: release-notes-template
artifact: decision:release-notes-template
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:21:13Z
---

# Notes follow a fixed section set covering every distribution channel

Supported. Both governing texts — the formal-release skill and the release guide — carry the same skeleton: a summary paragraph, then breaking changes, what's new, fixes, internal, and one section per distribution channel, with the rule that an empty section is omitted and every entry references a real diff hunk. The project distributes through four channels and the skeleton carries a section for each: Hub images, the two Go module paths, the npm package, and the prebuilt CLI archives. Checked every release-notes file the project has shipped — thirteen of them — and all thirteen carry the section set in the template's order; the two whose bump had nothing to report omit exactly the empty sections (one has no breaking-changes section, the other neither breaking changes nor what's new). The CLI section appears in the three files cut after the CLI channel began shipping, which is the same "omit what does not apply" rule. Diff-traceability is enforced only by prose plus the reviewer subagent's rubric, whose first two criteria are that no entry lacks a diff hunk and no flagged breaking surface is missing.
