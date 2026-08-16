---
audit: run-name
artifact: decision:run-name
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:44:00Z
---

# The run name's default from the manifest project field and the unvalidated override

Supported. The compose one-shot takes its run name from the name flag when set and otherwise from the manifest's project field, then passes it straight into the run-directory name. The project field carries the validation the rationale relies on: the manifest loader requires it and rejects anything not matching the shared lowercase identifier pattern, which admits only letters, digits, and hyphens up to sixty-three characters — filesystem-safe by construction. The override goes through no check at all: it is trimmed of nothing, matched against no pattern, and reaches the directory name exactly as typed, which is what the second rejected alternative asks for. The first rejected alternative is equally absent, since the flag is optional.
