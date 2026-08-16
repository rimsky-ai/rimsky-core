---
audit: licensing-enforced-by-license-lint
artifact: decision:licensing-enforced-by-license-lint
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:44:49Z
---

# A build-step license check constrains the permissive surface's imports to stdlib plus permissively licensed dependencies

Supported. A dedicated checker under the tools group loads the repo's licensing map and, for every non-test Go file classified permissive, parses its imports and rules on each one: standard-library imports pass on the no-dot-in-first-segment test, an internal import is resolved back to a repo path and rejected outright when that path classifies copyleft, and a third-party import must resolve to a module entry in the map whose license expression is drawn entirely from the permitted set — an unlisted module is a violation rather than a pass. A second pass walks the full build closure of each permissive module and applies the same two rules to every module in it, so a dependency reached transitively rather than by a direct import cannot slip through. The map currently permits five license identifiers and classifies eight third-party modules. The check is wired as a build step: it is a make target of its own and a prerequisite of the plain lint target, so it runs on every lint and inside the release chain, and a pin test asserts both the target's existence and that lint still depends on it.
