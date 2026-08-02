---
audit: mandatory-instantiation-gate
artifact: story:mandatory-instantiation-gate
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095807-instantiation-static-config-gate-untested
---

# Instance create validates value constraints

Unsupported. The create-time validation gate the story describes exists and is wired into instance creation, correctly composing and validating statically-knowable config against the referenced executor's schema, including value constraints. But neither of its two constituent functions is referenced by any test in the repository. The one test whose name suggests coverage in fact never reaches instance creation in its rejection path — that rejection is satisfied by an earlier, separate registration-time validator — and the one scenario where the create-time gate alone would matter, a node with no node-level schema relying solely on template-level defaults, has no test at all.
