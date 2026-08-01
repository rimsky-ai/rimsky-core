---
decision: artifact-root-discovery
status: adopted
---

# artifact-root-discovery

## Choice

From the current working directory, walk parent directories for the first existing rimsky workdir marker. Create one in the current working directory if none is found. An explicit workdir override bypasses discovery entirely.

## Rationale

A walk-up-to-first-marker discovery pattern lets operators run the verb from any subdirectory of a project and land on the same workdir; new projects get one created on first run. The override exists for cases that need explicit placement (per-run, per-environment, scripted).

## Alternatives

- Always the current working directory — rejected: running from a subdirectory silently creates a second workdir for the same project.
- A fixed per-user location — rejected: workdirs are per-project; one shared location mingles unrelated projects' runs.
