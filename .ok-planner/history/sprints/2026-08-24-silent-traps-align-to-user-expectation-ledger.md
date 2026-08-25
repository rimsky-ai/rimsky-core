# Relay ledger: Align twenty silent traps to what a user expects

The session writes the standing reviewer's open ledger and the open claimed forks here on every relay. A replacement session or reviewer reads this file from disk.

Relay 10: fix-only round 1 reviewed; L28 and L29 verified closed against the tree. The ledger is empty, so the build is code complete and the team retires. `/certify-work` runs next, cold.

## Open ledger

(empty)

## Closed

- L1–L10 (stage 1): verified closed at relay 2.
- L11–L15 (stage 2): verified closed at relay 4.
- L16–L21 (stage 3): verified closed at relay 6.
- L22–L27 (stage 4 and its fix round): verified closed at relay 8; L27 answered by fork F3.
- L28–L29 (stage 5): verified closed at relay 10.

## Open claimed forks

(none from the reviewer; F1–F4 are in the completion report's `## Divergences`)
