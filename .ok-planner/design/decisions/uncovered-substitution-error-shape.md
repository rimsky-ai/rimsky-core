---
decision: uncovered-substitution-error-shape
---

# Uncovered-substitution registration error carries a structured envelope entry

## Choice

Registration rejection for an uncovered substitution ref returns a structured `validation_errors` entry, kind-discriminated as an uncovered-substitution rejection, that identifies the offending ref and where it appears and carries a copy-pasteable subscription-entry suggestion — a valid drop-in object with its upstream-refresh flag defaulted conservatively — plus a separate one-sentence note explaining that flag's implications.

## Rationale

Programmatic fix-suggestion. Both human authors and LLM agents writing templates consume registration errors; a structured envelope lets them apply the fix mechanically. Keeping the suggested entry as a valid drop-in JSON object preserves its copy-pasteability.

## Alternatives

- A prose `{path, msg}` entry describing the fix — rejected: consumers would have to reconstruct the subscription entry by parsing prose, defeating mechanical application.
- Embedding the explanatory note inside the suggested entry itself — rejected: an extra explanatory field would make the suggestion no longer a valid drop-in object.
