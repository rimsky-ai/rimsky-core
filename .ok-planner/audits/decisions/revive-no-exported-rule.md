---
audit: revive-no-exported-rule
artifact: decision:revive-no-exported-rule
determination: supported
commit: b767a27d
audited: 2026-08-02T09:38:50Z
---

# Revive's exported-comment rule is disabled

Supported. `.golangci.yml`'s `linters-settings.revive.rules` carries a single entry, `name: exported` with `disabled: true`, and `test/plumbline/depguard_boundaries_test.go`'s `TestReviveExportedRuleDisabled` reads that same config and fails if the rule is ever absent or re-enabled, giving the choice a standing mechanical check beyond the static config file.
