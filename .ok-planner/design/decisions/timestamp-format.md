---
decision: timestamp-format
status: adopted
---

# Filesystem-safe ISO 8601 timestamp format

## Choice

Timestamps embedded in file and directory names are ISO 8601 UTC with colons replaced by hyphens: `YYYY-MM-DDTHH-MM-SSZ`.

## Rationale

ISO 8601 is lexicographically chronological (so directory listings sort correctly). Hyphen-for-colon makes the path filesystem-safe on every platform; the format remains human-readable.

## Alternatives

- Unmodified ISO 8601 (`HH:MM:SS`) — rejected: colons are illegal or hazardous in paths on common platforms.
- Unix epoch seconds — rejected: sorts correctly but is not human-readable in a listing.
