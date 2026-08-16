---
audit: config-enforced-fitness-tests
artifact: decision:config-enforced-fitness-tests
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T04:47:25Z
checked: 40
unaccounted: 1
---

# Whether every decision enforced only by a configuration surface is proved by an annotated grouped fitness test, and no tags sit in configuration

Unsupported by one member. Enumerating the 237 live decisions and reading each whose enforcement point is a configuration file rather than Go code gives a population of 40: 10 riding the dependency-lint and linter-enablement configuration, 11 riding the module manifests, 15 riding the Makefile, the image definitions, and the release configuration, and 4 more riding the repo layout, the CI workflow, and the test-runner flags. Thirty-nine are proved by a grouped fitness test in the pin-test package that asserts the rule's presence and shape and carries the decision's annotation — nine such files, holding 41 decision annotations between them, each read to confirm the annotation sits on a test that actually asserts the config. The second half of the choice holds outright: a search across every YAML, module manifest, Dockerfile, and the Makefile found zero citation tags, so nothing is stamped where the per-edit lint cannot see it.

## Unaccounted

- The test-parallelism decision: its caps live only in the Makefile's test recipes, and no fitness test asserts either the admitted saturation cap or the absence of caps elsewhere.

## Remediation

- The Makefile carries a bare, untagged prose reference to that same decision's slug in a comment, which is the unpoliced rot the choice's rationale gives as its reason for banning tags in configuration.
- The grouping is looser than "one test file per enforcement surface": the Makefile is asserted by two pin-test files, the module manifests by two, and the CI workflow by three.
