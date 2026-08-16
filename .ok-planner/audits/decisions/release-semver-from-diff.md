---
audit: release-semver-from-diff
artifact: decision:release-semver-from-diff
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T10:05:00Z
---

# The bump is diff-derived, but two of the five named surfaces are not read

Unsupported: the mechanism exists but is partial on two of the five consumer-visible surfaces the decision names. The formal-release skill does derive the bump from the diff since the last stable tag, and it does classify against five surface classes matching the decision's list. Two of them do not reach their subject. CLI flags are inspected by grepping the eight binary entrypoint files for the top-level flag-declaration calls; none of those eight files contains a single reference to the flag package, and every CLI flag declaration — around eighty across the CLI library beside the main binary — uses the flag-set variable form the grep does not match, so no CLI flag change can ever move the bump. Exported symbols are inspected in the protocols and foundation modules only, which omits the root module — the module the release notes themselves tell consumers to fetch, and the one holding the graph, runtime, and control library layers — so a breaking change to the largest shipped Go surface is classified as a patch. The remaining three surfaces resolve correctly: the proto directory, both migration directories, and the repo-wide environment-variable grep all name real locations. The skill calls the rule best-effort and routes misses to the gate as questions, but two of five named surfaces are unreachable rather than imprecise.
