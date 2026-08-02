---
audit: rimsky-run-self-hosts-templates
artifact: decision:rimsky-run-self-hosts-templates
determination: supported
commit: b767a27d
audited: 2026-08-02T09:40:22Z
---

# `rimsky run` self-hosts when no target endpoint is present, with the stated escape hatches and usage errors

Supported. Given a template file and no resolved endpoint, `rimsky run` boots an in-process all-in-one stack by calling the same run-directory, synthetic-config, and role-stack-startup functions `compose run` uses for its own self-host path, drives the template to terminal against that local control-api, then tears the stack down on exit. An explicit or context-configured endpoint suppresses self-hosting and dispatches remotely instead; an explicit `--self-host` flag bypasses a configured context endpoint (confirmed by a test that reaches the self-host branch's own rejection path rather than attempting a remote dial against the configured URL), and combining `--self-host` with an explicit `--endpoint` is a checked usage error (exit 2). Because self-host is one-shot, both `--template <name>` (which presupposes a pre-existing template registry) and `--keep` (which presupposes a surviving instance row) are rejected as usage errors specifically under the self-host branch, each covered by its own test.
