---
issue: root-help-command-lines-misstate-surface
kind: human
category: cli
artifacts:
  - decision:cli-verb
  - decision:rimsky-run-self-hosts-templates
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:53:17Z
github: https://github.com/rimsky-ai/rimsky-core/issues/94
---

# Three root-help command lines understate what their commands do

Three lines in the CLI's root help describe a narrower command than the binary
implements. None is wrong in a way that errors — each is wrong in a way that
hides a capability, and the root help is the source the published CLI reference
is generated from, so each omission propagates.

All three were re-verified against the current tree and still hold.

**The `run` line hides the whole self-hosting mode.** It shows `run <file>`
against a remote deployment. In fact the verb also accepts a named template in
place of the file, and — when no endpoint resolves from a flag, the environment,
or a stored context, or when self-hosting is requested explicitly — it boots an
in-process stack on a loopback port, drives the instance to completion, and
tears it down. That is a deliberate, documented mode of the product
(`decision:rimsky-run-self-hosts-templates`). The verb's own usage string
already describes both forms; the root help is the least informative of the
three surfaces.

**The `rm-instance` line says `<id>`; the command takes an id or a key.** The
handler's own usage string and the REST route both resolve either. The line's
other half — that the instance must already be terminal — is correct.

**The `auth create-key` line misstates what's required and omits most of the
flags.** It presents a role flag as mandatory; it is required only when no
role-file is given. The line omits the role-file flag, an expiry flag, and the
repeatable grant-patch flags — which together are most of the reason to reach
for the command rather than the defaults.

## Ruling

> Generated ruling (/verify-issues): correct all three lines to match the
> commands — the run line to show both input forms and name the self-hosting
> fallback, the delete line to say it takes an id or a key, and the key-creation
> line to state that the role flag is required only without a role file and to
> name the flags it currently omits. Each is a single-valued correction of help
> text against verified behavior, and the self-hosting mode in particular is a
> standing project decision the help simply fails to mention.
> Verified against the tree as it stands; nothing was applied.
