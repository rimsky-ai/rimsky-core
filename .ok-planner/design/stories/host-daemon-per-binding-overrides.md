---
story: host-daemon-per-binding-overrides
---

# Per-binding env/args/cwd/timeout honored

## Story

As a template author declaring late-bind bindings for varied local binaries, I can specify per-binding env vars, command-line args, working directory, and spawn timeout, so that I run different binaries with different configuration through the same daemon without global config soup.
