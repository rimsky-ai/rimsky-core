---
error: schedule_cron_parse_failure
surfaced_to: cli-user
---

# Schedule cron parse failure

## What it means

A template's schedule declaration carries a `cron:` field that the cron parser could not parse.

## When it happens

At template registration (`POST /templates`) or compose-apply when the scheduler validates the template's schedule blocks.

## What to do

Verify the cron expression against the [robfig/cron/v3](https://pkg.go.dev/github.com/robfig/cron/v3) syntax — that's the parser Rimsky uses. Typical mistakes: 6-field expressions where 5 are expected, named-shortcut typos (`@hourl` instead of `@hourly`), invalid day-of-week range. Test the expression locally before registering.

## See also

- [`../../concepts/template.md`](../../concepts/template.md)
