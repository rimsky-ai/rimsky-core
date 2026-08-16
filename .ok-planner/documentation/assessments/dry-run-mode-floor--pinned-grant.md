---
assessment: dry-run-mode-floor--pinned-grant
subject: story:dry-run-mode-floor
way: pinned-grant
release: d977250c
outcome: held
warrant: experiment:dry-run-mode-floor
---
# Minting a key whose grant pins a write action to dry-run

Three keys were minted through `catalog:cli-verbs/rimsky auth create-key` against a fresh deployment whose authentication had been established with `catalog:cli-verbs/rimsky auth init`: one granting `catalog:permission-actions/tag:create` pinned to dry-run mode, one granting the same action unpinned, and one holding both the pinned grant and a wildcard grant covering it. The pinned key's plain tag creation through `catalog:http-routes/POST /v1/tags` — carrying no mode flag of any kind — came back as a synthetic envelope naming the tag it would have created, and the tag was absent from the store afterwards. Repeating the same request with its own dry-run flag set to false produced the same envelope and the same absence, so the holder cannot lift the floor its own grant sets. The unpinned control key performed the real write and its tag persisted, so the floor is the grant's doing and not the deployment's. The pinned key kept its read grant throughout and stayed a working credential for what it was granted. An operator can therefore hand an autonomous agent a credential that can only ever attempt the write.

## Unverified remainder

The key holding both the pinned grant and a wildcard grant over the same action performed the real write. That is the proviso the promise states — the floor holds only while no other grant on the key authorizes execute mode on that action — so a key carrying a second, broader grant is not attempt-only, and pinning one grant does not by itself make a key safe to hand out.
