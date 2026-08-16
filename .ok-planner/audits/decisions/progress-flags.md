---
audit: progress-flags
artifact: decision:progress-flags
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T08:52:00Z
---

# The three progress flags, what each actually changes, and whether the format and volume axes compose

Unsupported. All three flags exist and parse on the compose one-shot, and two of the three described behaviours are absent. The quiet flag alone does collapse output to the final summary, and the json flag alone does switch every line to a JSON object one per line. The verbose flag changes nothing at runtime: it selects a printer whose only difference from the default is a frame-tick method, and no production code path in the tree calls that method — the only callers are the printer's own unit tests — so the verbose printer is behaviourally identical to the default, and the flag's help text further promises claim events for which no printer method exists at all. The two axes do not compose either: the printer constructor tests the json flag before the quiet flag and returns the JSON printer outright, so quiet has no effect under json and the combination the rationale names as the structured CI shape emits the same event set as a default run rather than collapsing to the summary. A test carrying this decision's own annotation pins that precedence, so it is deliberate. Rejecting quiet and verbose together at parse time is the only composition rule the code enforces, and it is a mutual exclusion rather than the composition the rationale claims.
