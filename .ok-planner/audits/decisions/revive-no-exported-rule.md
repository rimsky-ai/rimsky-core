---
audit: revive-no-exported-rule
artifact: decision:revive-no-exported-rule
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# Whether the exported-symbol comment rule is disabled in the lint configuration

Supported. The linter is enabled in the configuration and carries exactly one rule entry, which names the exported-symbol rule and marks it disabled. A fitness test reads the configuration and fails in both failure modes that matter: if the entry is present but re-enabled, and if the entry disappears entirely, since the linter's default posture would then reinstate the rule silently. The conflict the rationale describes is real in this tree — the comment-hygiene lint runs on every edit and admits documentation comments only in files carrying the opt-in marker, so an enabled rule would demand comments the other check rejects.
