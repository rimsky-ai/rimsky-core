---
concept: dry-run
---

# Dry-run

## What it is

Dry-run is a request mode — preview without commit — that asks what a write would do without doing it. A request resolves to dry-run because the caller asks for dry-run on the request, or because the grant that authorizes the action is itself dry-run (see `concept:permission`); with neither, the request executes. A write resolved to dry-run runs its validation, including the side-effect-free calls out to validating services (see `concept:validation`), skips the mutation, and returns a preview of what the write would have produced. A grant's dry-run mode is a floor: every write the grant covers runs in dry-run, and the caller cannot escalate past it. Where more than one grant entry matches the same action, the most permissive matched mode governs, so a coexisting execute-mode entry lifts the floor.

## Purpose

Dry-run separates a write's validation from its effect on one surface. A caller establishes that a request is well-formed, authorized, and structurally valid before anything changes, and sees what the write would produce, without the project maintaining a second, weaker path for the check. Binding the mode to an identity, rather than to the request, lets a deployment issue an authority that can only ever attempt a write.

## Boundaries

Dry-run owns the resolution of a request's mode, the preview a suppressed write returns in place of its result, and the dry-run branch of each write path. It covers every write action (see `story:dry-run-request-flag`, `story:dry-run-mode-floor`). It does not own the read path, because a read has no mutation to skip. It does not own the grant mode, which belongs to `concept:permission`: the resolved mode is the more restrictive of the request's ask and the matched grant entry's mode, and dry-run owns that resolution and the branches it drives. The audit record of a request carries the resolved mode (see `concept:event-log`).

see also: `permission`, `event-log`, `validation`
