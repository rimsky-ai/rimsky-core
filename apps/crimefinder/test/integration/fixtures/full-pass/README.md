# full-pass integration fixture

Small TypeScript-ish source tree used by `integration/full-pass.test.ts`.
Carries one planted class-1 candidate (`src/unsafe.ts` — a function that
silently swallows errors) so the stub executor can emit a finding that
cites a file actually present in the tree.

The harness copies this directory into a fresh tmp dir and `git init`s
it before pointing the producer at it.
