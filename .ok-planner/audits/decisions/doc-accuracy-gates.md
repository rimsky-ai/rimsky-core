---
audit: doc-accuracy-gates
artifact: decision:doc-accuracy-gates
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:41:50Z
---

# Whether both named documentation gates exist and mechanically diff prose against code facts

Supported, and both instances are real gates rather than conventions. The rules gate reads the project's own rules document, pulls every backtick-quoted token that looks like a repository path, and stats each against the tree, failing with the list of paths that do not exist; it also refuses four named dead references and asserts the image-rebuild instruction still names the current target, and a companion table test pins the path-recognition heuristic across fourteen cases so the gate cannot quietly stop recognizing anything. The substitution gate parses the resolver's source, extracts the source kinds the package doc's bullets enumerate and the arms of the resolver's dispatch switch, and diffs them in both directions, so a documented kind with no arm and an arm with no bullet each fail with both sets printed. Both are ordinary Go tests in the root module, which the repository's test target runs across every package, so "build-time" is accurate rather than aspirational. The count of two also holds under an adversarial reading: the one other mechanically-checked enumerating document in the tree is the environment-variable table, and that one is generated from the code rather than hand-authored — the approach this decision's Alternatives explicitly set aside — so it belongs to its own decision, not to this pattern. No other hand-authored document in the tree is gated, which matches what the decision claims rather than contradicting it: the standing rule it states is about surfaces that adopt the pattern, not a claim that every markdown file already has.
