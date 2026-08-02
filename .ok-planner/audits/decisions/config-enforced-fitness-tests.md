---
audit: config-enforced-fitness-tests
artifact: decision:config-enforced-fitness-tests
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:35:29Z
---

# Config-only decisions are proven by grouped Go fitness tests, never tagged in the config itself

Supported. `test/plumbline/` carries 8 grouped fitness-test files proving config-enforced surfaces from Go — `depguard_boundaries_test.go` (the `.golangci.yml` depguard rules), `module_manifests_test.go` (`go.work`/`go.mod` library pins), `build_chain_test.go` (Makefile targets and image definitions together, as one build-chain surface), `env_var_registry_test.go`, `jcs_pin_freeze_test.go`, `clean_test.go`, `claude_agent_image_hardening_test.go`, and `sensor_deploy_artifacts_test.go` — each asserting the presence/shape of the rules it covers, and the ones tied to a named decision (`depguard_boundaries_test.go`, `module_manifests_test.go`, `build_chain_test.go`, `clean_test.go`) carry the `@decision:` tags for what they prove, including this decision's own tag on `TestDepguardBoundaryRulesPresentAndShaped`. A repo-wide search for `@decision:` across every `.yml`/`.yaml`/`Makefile`/`Dockerfile*`/`go.mod`/`go.work` file (the four surfaces the choice names) found zero matches, confirming citation tags are never stamped into the configs themselves.
