---
decision: artifact-root-discovery
status: adopted
---

# artifact-root-discovery

## Choice

From cwd, walk parent directories for the first `.rimsky/`. Create in cwd if none found. `--workdir <path>` overrides discovery entirely.

## Rationale

A walk-up-to-first-marker discovery pattern lets operators run the verb from any subdirectory of a project and land on the same `.rimsky/`; new projects get one created on first run. The override exists for cases that need explicit placement (per-run, per-environment, scripted).
