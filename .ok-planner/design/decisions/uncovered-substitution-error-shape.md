---
decision: uncovered-substitution-error-shape
status: as-is
---

# Uncovered-substitution registration error carries a structured envelope entry

## Choice

Registration rejection for an uncovered substitution ref returns a structured `validation_errors` entry of kind `substitution_ref_uncovered` carrying the receiver node-type, the literal ref text, the schema property path the ref appears in, a copy-pasteable subscription-entry suggestion with both flags set to `false`, and a separate one-sentence note explaining the flag implications.

## Rationale

Programmatic fix-suggestion. Both human authors and LLM agents writing templates consume registration errors; a structured envelope lets them apply the fix mechanically. Keeping the suggested entry as a valid drop-in JSON object preserves its copy-pasteability.
