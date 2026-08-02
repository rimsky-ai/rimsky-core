---
issue: expand-folder-fanout-e2e-coverage-missing
kind: audit
category: test-coverage
artifacts:
  - story:fs-fanout-expand-folder
status: repaired
opened: 2026-08-02T09:58:06Z
---

# Does `expand_folder` fan-out have an automated, CI-wired end-to-end test?

`story:fs-fanout-expand-folder` promises a template author can fan out over
a picked folder's contents against the bundled filesystem store.
Re-verification confirmed the `expand_folder` `SplitScope` handler is real
and unit-tested in isolation, but the only end-to-end artifact was a manual
shell demo with no `go test` wrapper — no test drove a declared fan-out
node's `expand_folder` request through the real runtime.

Rule that determined the fix: the story is already true in the running
code (confirmed by the new test below); only automated coverage was
missing, mirroring a gap already closed for the sibling story
`story:fanout-list-array` via `fs_fanout_list_e2e_test.go` — outcome 2
(add the missing test), no commitment change.

What changed: added
`lib/services/test/scenarios/claim_producers/fs_fanout_expand_folder_e2e_test.go`
(`TestFSFanOutExpandFolderE2E`), mirroring `fs_fanout_list_e2e_test.go`'s
shape: seeds a filesystem claim-producer folder with 3 files, fans out via
`partition_request: {"expand_folder":{"filter":"*.txt"}}`, and asserts one
`fanout_partition` RunScope and one sub-claim row per matched file, keyed
by the files' relative paths.

Verified: `RIMSKY_IMAGE_TAG=src-e6de8d2bdb1c go test
./lib/services/test/scenarios/claim_producers/ -run
TestFSFanOutExpandFolderE2E -count=1` passes against a real containerized
stack (4.9s). `go build ./lib/services/...` and `go vet
./lib/services/test/scenarios/claim_producers/...` are clean.
