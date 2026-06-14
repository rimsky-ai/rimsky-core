---
decision: timestamp-format
status: adopted
---

# timestamp-format

## Choice

ISO 8601 UTC with colons replaced by hyphens: `YYYY-MM-DDTHH-MM-SSZ`.

## Rationale

ISO 8601 is lexicographically chronological (so directory listings sort correctly). Hyphen-for-colon makes the path filesystem-safe on every platform; the format remains human-readable.
