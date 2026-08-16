---
audit: pre-v1-break-freely
artifact: decision:pre-v1-break-freely
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:21:13Z
---

# No compatibility guarantee is carried on any of the four named surfaces

Supported. Checked each of the four surfaces the decision names. The wire protocol declares a single protobuf package version and the operator HTTP surface a single route version, with no negotiation path, no parallel shapes, and no deprecated-field accessors. The two persistence drivers carry 28 and 29 numbered migration files; nine of them are named for dropping or retiring columns and tables outright, which is the decision's stance executed rather than shimmed. The unified config loader decodes with unknown fields rejected, so a retired key fails the load instead of being tolerated. Dead-code deletion is mechanically enforced: the shared lint configuration enables the unused analyzer across all four modules, and the lint gate runs on every module. The project's rules surface states the same stance in the same terms, including the migration-numbering caveat. The one construct named "legacy" in shipping code marks a template that declares no graph block — a currently valid template shape, not a retained compatibility form.
