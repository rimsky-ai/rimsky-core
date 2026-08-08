---
issue: auth-login-stores-unread-api-key
kind: human
category: bug
artifacts:
  - concept:api-key
  - concept:control-api
  - story:api-key-management
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:53:15Z
github: https://github.com/rimsky-ai/rimsky-core/issues/91
---

# `rimsky auth login` stores an api-key that nothing ever reads

`rimsky auth login` verifies your key against the deployment, prints success, and
writes the key into the CLI's config file. The very next command then fails with
401, because nothing reads the key back. Every verb resolves its key from the
`--key` flag or an environment variable and stops there.

What makes this land as a bug rather than a rough edge is the asymmetry. The same
config file also stores the deployment's endpoint, and endpoint resolution *does*
fall through to it. So after login the CLI reaches the right deployment with no
credential — the user sees a command that found the server and was refused,
having just been told they were logged in.

Verified in the current tree: login persists the key and its test asserts only
that the write happened; the two resolution paths (one for control-API verbs,
one for the auth verbs themselves) read flag and environment only. The single
other reader of the stored field is the config redactor that hides it from
`-o json` output.

## Options

- **Resolve the key from the stored context**, after flag and environment,
  exactly as the endpoint already resolves. Makes login mean what it says. Means
  a bearer token on disk is picked up automatically rather than named explicitly
  on each invocation — though the token is already on disk today, so this changes
  what the CLI reads, not what is exposed.
- **Stop persisting the key** and say so in the command's help. Keeps the
  credential out of the config file entirely, at the cost of making `auth login`
  close to ceremonial: it would verify a key and then discard it, leaving the
  user to supply it again on every subsequent command.

The ruling decides whether the CLI has a login that logs you in, or drops the
stored credential.

## Ruling

> Read it. Key resolution falls through to the stored context, after the flag and
> the environment variable, the way endpoint resolution already does. Add a test
> that asserts a command run after login carries the key — the existing test
> asserts the write, which is why this survived.
>
> Rationale: the alternative buys no security. The plaintext key is already
> written to disk today; not reading it back leaves the exposure and removes the
> benefit. And the command's own shape argues for this reading — verifying a key
> against the deployment before storing it is only worth doing if something later
> uses what was stored. The endpoint precedent settles the ergonomics question:
> the CLI has already decided a stored context is an acceptable source for
> connection details, and a credential is the half of that pair the user is most
> likely to expect to persist.
