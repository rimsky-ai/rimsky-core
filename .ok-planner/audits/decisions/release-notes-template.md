---
audit: release-notes-template
artifact: decision:release-notes-template
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095817-release-notes-template-missing-cli-channel-section
---

# Notes shape

Unsupported for the "each distribution channel" clause. The template fixes seven sections, and all eleven shipped release-notes files follow that set exactly with no additions. A sibling decision names four distribution channels, but the template and every shipped release-notes file lack a section for the fourth — the CLI-archive channel appears, when mentioned at all, only as an ordinary bullet in an unrelated section, even in the release that introduced it. The rest of the decision — the fixed section set, an omitted-when-empty breaking-changes section, and a diff-traceability review step — is realized as described.
