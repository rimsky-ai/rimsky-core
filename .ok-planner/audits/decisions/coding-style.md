---
audit: coding-style
artifact: decision:coding-style
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:25Z
---

# Whether the Plumbline methodology is installed as a plugin with both checks, the docstring opt-in, and the two enforcement points

Supported on every clause. The methodology is vendored as a plugin estate with its lint binary committed alongside its configuration, and the per-session cheatsheet is committed under the project rules directory where a contributor without the plugin still reads it. Both checks run: the configuration declares no disabling key, and a fitness test fails if a reintroduced key ever sets one false. The docstring exemption is conditional exactly as described — the lint reads a file-level opt-in marker and only then accepts GoDoc-style and JSDoc-style documentation blocks, so a canonical doc shape in an unmarked file is still a violation. Both enforcement points exist: a PostToolUse hook on edits and writes runs the lint over the changed line ranges and exits with the blocking code that surfaces violations to the agent, and CI points the same binary at the tree through the test that shells it at the repo root, with a second fitness test failing if CI stops pointing at the vendored binary. The citation configuration declares the three design tags the decision names, each resolving to a file in the design corpus; two further suite tags are declared beside them, which the decision does not speak to either way.
