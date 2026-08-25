---
decision: short-flags-single-letter
---

# Short flags are single letters, one per token

## Choice

The `rimsky` CLI parses flags with the Go standard library's parser. A short flag is a one-letter alias registered beside its long form: `-y` for `--yes`, `-f` for `--follow`, `-o` for `--output`, `-v` for `--version`, `-h` for `--help`. Each short flag stands alone in its own token, with its value in the next token. Short flags do not cluster, and a value never attaches to its flag. `compose` keeps `-f` as the manifest path, so `--force` has no short form anywhere.

## Rationale

The standard library's parser cannot cluster short flags or attach values, and the project's rules resist a heavier command-line dependency. Registering the aliases operators type most gives them the habit that works. Stating the grammar keeps the parser's limit from reading as a bug. `-f` for `--follow` and `-f` for the manifest path cannot coexist under one letter, so `--follow` takes the letter on the verbs that stream and `--force` stays long. A long-only flag on a destructive verb costs one habit; a wrong short flag on one costs data.

## Alternatives

- A POSIX flag parser as a dependency, for clustering and attached values — rejected: the project resists heavier command-line libraries, and clustering is the only thing the dependency buys.
- No short flags beyond `-h` and `-v` — rejected: `-y` and `-f` are the two an operator types by habit, and their absence is the surprise.
- `-f` for `--force` and a different letter for `--follow` — rejected: `--force` on a destructive verb is the flag a mistyped letter should not reach.
