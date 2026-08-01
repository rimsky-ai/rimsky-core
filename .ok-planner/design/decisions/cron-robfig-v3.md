---
decision: cron-robfig-v3
status: as-is
---

# Cron expression parser

## Choice

Cron expressions are parsed by `robfig/cron/v3`, pinned to that major line.

## Rationale

A widely-used, maintained parser for the standard cron grammar. Hand-rolling cron parsing re-derives well-known edge cases for no benefit, and the major-line pin keeps the accepted expression grammar stable.

## Alternatives

- A hand-rolled parser — rejected: cron grammar (ranges, steps, names, descriptors) is all edge cases with an established open-source answer.
- A heavier scheduling framework — rejected: only expression parsing and next-fire computation are needed, and the project resists heavier dependencies.
