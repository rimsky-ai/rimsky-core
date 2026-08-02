---
audit: template-error-policy
artifact: story:template-error-policy
determination: supported
commit: b767a27d
audited: 2026-08-02T09:34:15Z
---

# Per-error-class routing actions are honored at the runtime's error sites

Supported. The action vocabulary is closed to four values in code (`spec.ActionRetry`, `ActionGiveUp`, `ActionPass`, `ActionReleaseAndRequeue` in `lib/foundation/spec/policy.go`), consumed by the per-class lookup in `lib/runtime/runner_error_policy.go`. `TestTemplateErrorPolicy` (`test/scenarios/template_error_policy_e2e_test.go`) exercises all four as subtests against a real dispatch: `pass_settles_fresh_and_cascades` asserts a fresh (not failed) settle with no extra re-dispatch; `give_up_terminates_node_skips_downstream` asserts the terminal-error signal fires and a downstream subscriber never dispatches; `retry_re_dispatches` asserts at least `MaxRetries`+1 worker dispatches and one retry-audit event per retry; `release_and_requeue_abandons_claim_and_re_acquires` asserts the held claim is abandoned and a fresh `Open` backs the re-dispatch. All four of the closed vocabulary's members are covered by name.
