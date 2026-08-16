---
audit: cli-spawn-mechanism
artifact: decision:cli-spawn-mechanism
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# The agent CLI is spawned as a direct child through the Go standard subprocess call

Supported. The claude-agent runner constructs the CLI command through the Go standard library's process package at exactly one site — the only such call anywhere in the executor's sources — and starts it as a direct child of the handler process, with the binary defaulting to the CLI's own name and overridable by one operator variable. Arguments and environment are both composed per dispatch: the argument builder runs on every spawn and every resume from the dispatch's own configuration, and the child's environment is assembled fresh from the exposed values, the auth material, and the callback plumbing rather than inherited. There is no intervening runtime anywhere on that path — no interpreter, no embedded agent SDK, no supervisor process — and the executor's own image installs the CLI's self-contained native distribution rather than a package requiring a language runtime, so the shipped deployment matches the choice. A test spawns a real subprocess through the runner and reads back the files the runner wrote, and further tests cover the argument composition on both the start and the resume legs.
