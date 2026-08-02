---
issue: story-rimsky-deployment-bootstrap-unknown-command-home
kind: sprint
category: stories-prescriptive
artifacts:
  - story:rimsky-deployment-bootstrap
  - decision:image-entrypoint-role-selection
status: open
opened: 2026-08-01T22:32:10Z
---

# Entrypoint unknown-command error path is stated only in story prose

## Problem

`story:rimsky-deployment-bootstrap`'s prose commits that an unknown container command exits non-zero; `decision:image-entrypoint-role-selection` documents only the no-command / single-role dichotomy.

## Candidates

- Amend decision:image-entrypoint-role-selection to name the unknown-command error path, then reduce the story.
- Rule it below corpus altitude and reduce.
